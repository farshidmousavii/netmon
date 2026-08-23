package scheduler

// Phase 2 polling queue: a Postgres-backed job runner replacing simple
// per-provider interval loops for device-level polls. One goroutine
// enqueues due devices into discovery_jobs; a worker pool claims jobs
// with a lease (FOR UPDATE SKIP LOCKED) and executes them. All state
// lives in the database, so the queue is resumable across restarts:
// crashed workers leave expiring leases, not in-memory work.
//
// Retry/backoff is owned by the ENQUEUE side, not by job-level retries:
// after BreakerThreshold consecutive failures a device's effective poll
// interval doubles per extra failure, capped at MaxBackoff (decision E),
// resetting on success. A finished job is therefore terminal — succeeded
// or failed — and the next cycle gets a fresh job.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farshidmousavii/bidar/internal/domain"
	"github.com/farshidmousavii/bidar/internal/store"
)

// Executor polls one device: the per-provider entry point wired in by
// serve (snmp.PollDeviceByID / mikrotik.PollDeviceByID).
type Executor func(ctx context.Context, deviceID int64) error

// QueueConfig configures the polling queue.
type QueueConfig struct {
	// Workers is the number of concurrent job executors.
	Workers int
	// EnqueueInterval is how often the enqueue pass runs.
	EnqueueInterval time.Duration
	// Lease bounds one claimed job; an expired lease makes the job
	// claimable again (crashed-worker recovery).
	Lease time.Duration
	// BreakerThreshold is the consecutive-failure count at which a
	// device's effective poll interval starts doubling.
	BreakerThreshold int
	// MaxBackoff caps the circuit-breaker interval.
	MaxBackoff time.Duration
	// idle is how long a worker waits when nothing is claimable
	// (test injection; defaults to 2s).
	idle time.Duration
}

func (c *QueueConfig) fillDefaults() {
	if c.Workers <= 0 {
		c.Workers = 5
	}
	if c.EnqueueInterval <= 0 {
		c.EnqueueInterval = time.Minute
	}
	if c.Lease <= 0 {
		c.Lease = 10 * time.Minute
	}
	if c.BreakerThreshold <= 0 {
		c.BreakerThreshold = 5
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = time.Hour
	}
	if c.idle <= 0 {
		c.idle = 2 * time.Second
	}
}

// familyProviders maps a polled protocol_family to the executor name
// that handles it.
var familyProviders = map[string]string{
	"cisco_snmp":        "snmp",
	"mikrotik_routeros": "mikrotik",
}

// QueueRunner owns the enqueue loop and the worker pool.
type QueueRunner struct {
	store     *store.Store
	logger    *slog.Logger
	cfg       QueueConfig
	executors map[string]Executor
	now       func() time.Time
	tick      func(time.Duration) <-chan time.Time
}

// NewQueueRunner returns a queue runner for the given executors.
func NewQueueRunner(st *store.Store, logger *slog.Logger, cfg QueueConfig,
	executors map[string]Executor,
) (*QueueRunner, error) {
	if st == nil {
		return nil, fmt.Errorf("queue: store is required")
	}
	if len(executors) == 0 {
		return nil, fmt.Errorf("queue: no executors configured")
	}
	for name, exe := range executors {
		if exe == nil {
			return nil, fmt.Errorf("queue: executor %q is nil", name)
		}
	}
	cfg.fillDefaults()
	if logger == nil {
		logger = slog.Default()
	}
	return &QueueRunner{
		store:     st,
		logger:    logger,
		cfg:       cfg,
		executors: executors,
		now:       time.Now,
		tick:      time.After,
	}, nil
}

// Run blocks until ctx is cancelled: one enqueue loop plus Workers
// claiming/executing goroutines.
func (q *QueueRunner) Run(ctx context.Context) error {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-q.tick(q.cfg.EnqueueInterval):
				n, err := q.enqueueDue(ctx)
				if err != nil {
					q.logger.Error("queue: enqueue pass failed", "err", err)
					continue
				}
				if n > 0 {
					q.logger.Info("queue: enqueued due devices", "count", n)
				}
			}
		}
	}()

	for i := range q.cfg.Workers {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			owner := fmt.Sprintf("worker-%d", k+1)
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				processed, err := q.claimAndExecute(ctx, owner)
				if err != nil {
					q.logger.Error("queue: claim failed", "worker", owner, "err", err)
				}
				if processed {
					continue
				}
				select {
				case <-ctx.Done():
					return
				case <-q.tick(q.cfg.idle):
				}
			}
		}(i)
	}

	wg.Wait()
	return nil
}

// enqueueDue enqueues every enabled polled device whose next poll is
// due, applying circuit-breaker backoff. Active jobs dedupe.
func (q *QueueRunner) enqueueDue(ctx context.Context) (int, error) {
	enqueued := 0
	now := q.now().UTC()
	for family, provider := range familyProviders {
		exe, ok := q.executors[provider]
		_ = exe
		if !ok {
			continue // no executor wired for this family
		}
		devices, err := q.store.ListEnabledDevicesByFamily(ctx, family)
		if err != nil {
			return enqueued, fmt.Errorf("list %s devices: %w", family, err)
		}
		for _, d := range devices {
			if !deviceDue(d, now, q.cfg.BreakerThreshold, q.cfg.MaxBackoff) {
				continue
			}
			id := d.ID
			_, added, err := q.store.EnqueueJob(ctx, provider, "device", &id, now)
			if err != nil {
				q.logger.Warn("queue: enqueue failed", "device_id", d.ID, "err", err)
				continue
			}
			if added {
				enqueued++
			}
		}
	}
	return enqueued, nil
}

// deviceDue reports whether a device's next poll is due. After
// threshold consecutive failures the effective interval doubles per
// extra failure up to maxBackoff; a success resets the counter and with
// it the interval.
func deviceDue(d domain.Device, now time.Time, threshold int, maxBackoff time.Duration) bool {
	interval := time.Duration(d.PollIntervalSec) * time.Second
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	if d.ConsecutiveFailures >= threshold {
		extra := d.ConsecutiveFailures - threshold + 1
		if extra > 20 { // shift guard; cap dominates long before this
			extra = 20
		}
		if bi := interval * (1 << uint(extra)); bi < maxBackoff {
			interval = bi
		} else {
			interval = maxBackoff
		}
	}
	return d.LastPollAt == nil || now.Sub(*d.LastPollAt) >= interval
}

// claimAndExecute claims one due job and runs it to a terminal state.
// Returns false when nothing was claimable.
func (q *QueueRunner) claimAndExecute(ctx context.Context, owner string) (bool, error) {
	job, err := q.store.ClaimDueJob(ctx, owner, q.cfg.Lease, q.now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}

	started := q.now()
	err = q.execute(ctx, job)
	var msg *string
	if err != nil {
		s := err.Error()
		msg = &s
		q.logger.Warn("queue: job failed",
			"job_id", job.ID, "provider", job.Provider, "target", job.TargetID, "err", err)
	}

	// Finish out-of-band of cancellation: a shutdown racing the finish
	// must not strand the job as running (the lease would cover it, but
	// recording the outcome immediately is cleaner).
	if ferr := q.store.FinishJob(context.WithoutCancel(ctx), job.ID, owner, msg, q.now().UTC()); ferr != nil {
		q.logger.Error("queue: could not finish job", "job_id", job.ID, "err", ferr)
	}
	q.logger.Info("queue: job finished",
		"job_id", job.ID, "provider", job.Provider, "target", job.TargetID,
		"ok", err == nil, "duration", q.now().Sub(started).Round(time.Millisecond))
	return true, nil
}

// execute dispatches one job to its provider executor.
func (q *QueueRunner) execute(ctx context.Context, job *domain.DiscoveryJob) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("executor panic: %v", r)
			q.logger.Error("queue: executor panic",
				"job_id", job.ID, "provider", job.Provider, "panic", r)
		}
	}()

	exe, ok := q.executors[job.Provider]
	if !ok {
		return fmt.Errorf("no executor for provider %q", job.Provider)
	}
	if job.TargetType != "device" || job.TargetID == nil {
		return fmt.Errorf("unsupported target %s/%v", job.TargetType, job.TargetID)
	}

	tctx, cancel := context.WithTimeout(ctx, q.cfg.Lease)
	defer cancel()
	return exe(tctx, *job.TargetID)
}
