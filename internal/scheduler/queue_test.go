package scheduler

// Tests for the Phase 2 polling queue: due-ness math (pure), enqueue
// passes, and claim/execute cycles against real Postgres via the testdb
// harness. Executors are recording fakes — provider internals have their
// own tests. Gated on BIDAR_TEST_DATABASE_URL.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshidmousavii/bidar/internal/domain"
	"github.com/farshidmousavii/bidar/internal/store"
	"github.com/farshidmousavii/bidar/internal/testdb"
)

type recordingExecutor struct {
	mu    sync.Mutex
	calls []int64
	err   error
	panic bool
}

func (r *recordingExecutor) execute(ctx context.Context, deviceID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, deviceID)
	if r.panic {
		panic("fixture panic")
	}
	return r.err
}

func (r *recordingExecutor) recorded() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.calls...)
}

type queueHarness struct {
	pool *pgxpool.Pool
	st   *store.Store
	q    *QueueRunner
	exe  *recordingExecutor
	now  time.Time
	seq  int
}

func newQueueHarness(t *testing.T, exe *recordingExecutor) *queueHarness {
	t.Helper()
	pool := testdb.Open(t, testdb.ScratchURL(t, testdb.BaseURL(t)))
	st := store.New(pool)
	h := &queueHarness{
		pool: pool,
		st:   st,
		exe:  exe,
		now:  time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC),
	}
	q, err := NewQueueRunner(st, slog.Default(), QueueConfig{
		idle: time.Millisecond,
	}, map[string]Executor{
		"snmp":     exe.execute,
		"mikrotik": exe.execute,
	})
	if err != nil {
		t.Fatalf("NewQueueRunner: %v", err)
	}
	q.now = func() time.Time { return h.now }
	h.q = q
	return h
}

// seedDevice inserts a polled device with tunable health/interval state.
func (h *queueHarness) seedDevice(t *testing.T, name, family string, intervalSec int, failures int, lastPoll *time.Time) int64 {
	t.Helper()
	h.seq++
	ip := fmt.Sprintf("192.0.2.%d", 10+h.seq)
	var id int64
	if err := h.pool.QueryRow(context.Background(), `
		INSERT INTO network_devices
		    (name, protocol_family, role, mgmt_ip, enabled, poll_interval_sec,
		     consecutive_failures, last_poll_at)
		VALUES ($1, $2, 'unassigned', $3::inet, true, $4, $5, $6)
		RETURNING id`, name, family, ip, intervalSec, failures, lastPoll).Scan(&id); err != nil {
		t.Fatalf("seed device %s: %v", name, err)
	}
	return id
}

func TestDeviceDue(t *testing.T) {
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	mk := func(failures int, lastPollAgo time.Duration) domain.Device {
		lp := base.Add(-lastPollAgo)
		return domain.Device{
			PollIntervalSec:     600,
			ConsecutiveFailures: failures,
			LastPollAt:          &lp,
		}
	}
	cases := []struct {
		name string
		dev  domain.Device
		want bool
	}{
		{"never polled is due", domain.Device{PollIntervalSec: 600}, true},
		{"recently polled is not due", mk(0, 5*time.Minute), false},
		{"stale poll is due", mk(0, 11*time.Minute), true},
		{"below threshold keeps normal interval", mk(3, 15*time.Minute), true},
		{"breaker doubles interval", mk(5, 15*time.Minute), false},
		{"breaker satisfied after doubling", mk(5, 21*time.Minute), true},
		{"breaker quadruples at 6", mk(6, 39*time.Minute), false},
		{"breaker cap at one hour", mk(20, 30*time.Minute), false},
		{"breaker cap reached", mk(20, 61*time.Minute), true},
	}
	for _, c := range cases {
		if got := deviceDue(c.dev, base, 5, time.Hour); got != c.want {
			t.Errorf("%s: due = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEnqueueDueDedupeAndFiltering(t *testing.T) {
	h := newQueueHarness(t, &recordingExecutor{})
	ctx := context.Background()

	h.seedDevice(t, "cisco-on", "cisco_snmp", 600, 0, nil)
	disabled := h.seedDevice(t, "cisco-off", "cisco_snmp", 600, 0, nil)
	h.pool.Exec(ctx, `UPDATE network_devices SET enabled = false WHERE id = $1`, disabled)
	h.seedDevice(t, "ros-on", "mikrotik_routeros", 600, 0, nil)

	n, err := h.q.enqueueDue(ctx)
	if err != nil {
		t.Fatalf("enqueueDue: %v", err)
	}
	if n != 2 {
		t.Fatalf("enqueued = %d, want 2 (one per family)", n)
	}

	// Second pass dedupes against active jobs.
	if n, err := h.q.enqueueDue(ctx); err != nil || n != 0 {
		t.Fatalf("second pass = %d/%v, want 0", n, err)
	}

	var providers []string
	rows, err := h.pool.Query(ctx, `SELECT provider FROM discovery_jobs ORDER BY provider`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		providers = append(providers, p)
	}
	if len(providers) != 2 || providers[0] != "mikrotik" || providers[1] != "snmp" {
		t.Errorf("job providers = %v, want [mikrotik snmp]", providers)
	}
}

func TestEnqueueSkipsBreakeredDevice(t *testing.T) {
	h := newQueueHarness(t, &recordingExecutor{})
	recent := h.now.Add(-5 * time.Minute)
	h.seedDevice(t, "flapping-sw", "cisco_snmp", 600, 5, &recent) // backoff window

	n, err := h.q.enqueueDue(context.Background())
	if err != nil {
		t.Fatalf("enqueueDue: %v", err)
	}
	if n != 0 {
		t.Errorf("enqueued = %d, want 0 (breaker backoff)", n)
	}
}

func TestClaimAndExecuteSuccess(t *testing.T) {
	exe := &recordingExecutor{}
	h := newQueueHarness(t, exe)
	devID := h.seedDevice(t, "exec-sw", "cisco_snmp", 600, 0, nil)

	if _, err := h.q.enqueueDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	processed, err := h.q.claimAndExecute(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("claimAndExecute: %v", err)
	}
	if !processed {
		t.Fatal("expected a job to be processed")
	}
	calls := exe.recorded()
	if len(calls) != 1 || calls[0] != devID {
		t.Errorf("executor calls = %v, want [%d]", calls, devID)
	}
	var status string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM discovery_jobs`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" {
		t.Errorf("job status = %s, want succeeded", status)
	}
}

func TestClaimAndExecuteFailureRecordsOutcome(t *testing.T) {
	exe := &recordingExecutor{err: errors.New("device unreachable")}
	h := newQueueHarness(t, exe)
	h.seedDevice(t, "down-sw", "cisco_snmp", 600, 0, nil)

	if _, err := h.q.enqueueDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if processed, err := h.q.claimAndExecute(context.Background(), "worker-1"); err != nil || !processed {
		t.Fatalf("claimAndExecute = %v/%v", processed, err)
	}

	var status, errMsg string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT status, error_message FROM discovery_jobs`).Scan(&status, &errMsg); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || errMsg != "device unreachable" {
		t.Errorf("failed job = %s/%q", status, errMsg)
	}
	// The queue records outcomes; breaker state belongs to the provider.
	var failures int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT consecutive_failures FROM network_devices`).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != 0 {
		t.Errorf("consecutive_failures = %d, want 0 (provider-owned)", failures)
	}
}

func TestUnknownProviderFailsJob(t *testing.T) {
	exe := &recordingExecutor{}
	h := newQueueHarness(t, exe)
	devID := h.seedDevice(t, "odd-dev", "cisco_snmp", 600, 0, nil)

	// A job whose provider has no executor wired.
	if _, _, err := h.st.EnqueueJob(context.Background(), "ad", "device", &devID, h.now); err != nil {
		t.Fatal(err)
	}
	if processed, err := h.q.claimAndExecute(context.Background(), "worker-1"); err != nil || !processed {
		t.Fatalf("claimAndExecute = %v/%v", processed, err)
	}
	var errMsg string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT error_message FROM discovery_jobs`).Scan(&errMsg); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains([]byte(errMsg), []byte("no executor")) {
		t.Errorf("error_message = %q, want no-executor message", errMsg)
	}
	if len(exe.recorded()) != 0 {
		t.Error("executor must not be called for an unknown provider")
	}
}

func TestPanickingExecutorFailsJob(t *testing.T) {
	exe := &recordingExecutor{panic: true}
	h := newQueueHarness(t, exe)
	h.seedDevice(t, "panic-sw", "cisco_snmp", 600, 0, nil)

	if _, err := h.q.enqueueDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Must not crash the test goroutine; the recover in execute turns the
	// panic into a job failure.
	if processed, err := h.q.claimAndExecute(context.Background(), "worker-1"); err != nil || !processed {
		t.Fatalf("claimAndExecute = %v/%v", processed, err)
	}
	var status string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM discovery_jobs`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Errorf("job status = %s, want failed (panic recovered)", status)
	}
}
