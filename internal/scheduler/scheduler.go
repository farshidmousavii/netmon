// Package scheduler runs the Phase 1 providers as simple scheduled
// goroutines (one loop per provider; the lease-based discovery_jobs queue
// is Phase 2). Every run is logged to provider_runs; a panicking or
// failing provider is recovered and logged — it never takes down the
// daemon or blocks the other loops, and it retries on its next tick.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/farshidmousavii/bidar/internal/providers"
	"github.com/farshidmousavii/bidar/internal/store"
)

// Job binds one provider to its cadence. Each provider runs immediately
// at startup, then every Interval.
type Job struct {
	Provider providers.Provider
	Interval time.Duration
}

// Scheduler owns the provider loops.
type Scheduler struct {
	store  *store.Store
	logger *slog.Logger
	jobs   []Job
	// tick yields the channel a loop waits on between runs. Injectable
	// so tests drive cadence deterministically (no real-time sleeps);
	// the default uses time.After. The job is passed so tests can give
	// each provider its own channel.
	tick func(job Job, d time.Duration) <-chan time.Time
}

// New returns a scheduler for the given jobs.
func New(st *store.Store, logger *slog.Logger, jobs []Job) *Scheduler {
	return newWithTick(st, logger, jobs, func(job Job, d time.Duration) <-chan time.Time {
		return time.After(d)
	})
}

// newWithTick is New with an injectable tick source for tests.
func newWithTick(st *store.Store, logger *slog.Logger, jobs []Job, tick func(job Job, d time.Duration) <-chan time.Time) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{store: st, logger: logger, jobs: jobs, tick: tick}
}

// Run starts one goroutine per job and blocks until ctx is cancelled.
// Each loop stops promptly on cancellation (checked between runs and
// between ticks), so graceful shutdown never waits out a long poll.
func (s *Scheduler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, job := range s.jobs {
		if job.Interval <= 0 || job.Provider == nil {
			s.logger.Error("scheduler: skipping invalid job", "provider", job.Provider.Name())
			continue
		}
		wg.Add(1)
		go func(job Job) {
			defer wg.Done()
			s.runLoop(ctx, job)
		}(job)
	}
	wg.Wait()
}

func (s *Scheduler) runLoop(ctx context.Context, job Job) {
	s.runOnce(ctx, job)

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.tick(job, job.Interval):
			s.runOnce(ctx, job)
		}
	}
}

// runOnce executes one provider run with panic recovery and writes the
// provider_runs row. The recording is best-effort: a DB failure while
// logging the run is itself logged, never fatal.
func (s *Scheduler) runOnce(ctx context.Context, job Job) {
	started := time.Now().UTC()

	res, err := s.runSafely(ctx, job.Provider)
	finished := time.Now().UTC()

	status := "succeeded"
	var errMsg *string
	if err != nil {
		status = "failed"
		msg := err.Error()
		errMsg = &msg
		s.logger.Error("provider run failed", "provider", job.Provider.Name(), "err", err)
	} else {
		s.logger.Info("provider run succeeded", "provider", job.Provider.Name(),
			"items_found", res.ItemsFound, "duration", finished.Sub(started).Round(time.Millisecond))
	}

	if err := s.store.RecordProviderRun(ctx, job.Provider.Name(), status, res.ItemsFound, errMsg, started, finished); err != nil {
		s.logger.Error("scheduler: failed to record provider run", "provider", job.Provider.Name(), "err", err)
	}
}

// runSafely is the goroutine-boundary guard: a panicking provider is
// turned into an error (and a failed provider_runs row) instead of
// killing the daemon.
func (s *Scheduler) runSafely(ctx context.Context, p providers.Provider) (res providers.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("provider %s panicked: %v", p.Name(), r)
			s.logger.Error("scheduler: recovered provider panic", "provider", p.Name(), "panic", r)
		}
	}()
	return p.Run(ctx)
}
