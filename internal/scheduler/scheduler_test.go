package scheduler

// Scheduler tests with stub providers, driven by an injected tick
// channel — fully deterministic, no real-time sleeps, immune to
// parallel-package CPU load. Uses testdb for the provider_runs log.

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farshidmousavii/bidar/internal/providers"
	"github.com/farshidmousavii/bidar/internal/store"
	"github.com/farshidmousavii/bidar/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stubProvider counts runs; err/panic make it fail or blow up.
type stubProvider struct {
	name  string
	runs  atomic.Int64
	err   error
	panic bool
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Run(ctx context.Context) (providers.Result, error) {
	s.runs.Add(1)
	if s.panic {
		panic("boom")
	}
	if s.err != nil {
		return providers.Result{}, s.err
	}
	return providers.Result{ItemsFound: 3}, nil
}

func (s *stubProvider) Health() providers.Health { return providers.Health{} }

type testHarness struct {
	t      *testing.T
	sched  *Scheduler
	pool   *pgxpool.Pool
	tickCh map[string]chan time.Time
}

func newTestScheduler(t *testing.T, jobs []Job) *testHarness {
	t.Helper()
	pool := testdb.Open(t, testdb.ScratchURL(t, testdb.BaseURL(t)))
	chans := make(map[string]chan time.Time, len(jobs))
	for _, j := range jobs {
		chans[j.Provider.Name()] = make(chan time.Time, 16)
	}
	sched := newWithTick(store.New(pool), slog.Default(), jobs, func(job Job, d time.Duration) <-chan time.Time {
		return chans[job.Provider.Name()]
	})
	return &testHarness{t: t, sched: sched, pool: pool, tickCh: chans}
}

// run starts the scheduler in the background; the returned stop function
// cancels and waits for every loop to actually exit.
func (h *testHarness) run(t *testing.T) (stop func(), anyJobDone func() <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.sched.Run(ctx)
		close(done)
	}()
	stop = func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("scheduler did not stop after cancellation")
		}
	}
	t.Cleanup(stop)
	return stop, func() <-chan struct{} { return done }
}

// tick delivers n ticks to one provider's loop.
func (h *testHarness) tick(name string, n int) {
	for i := 0; i < n; i++ {
		h.tickCh[name] <- time.Now()
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRunsImmediatelyAndOnTick(t *testing.T) {
	p := &stubProvider{name: "stub"}
	h := newTestScheduler(t, []Job{{Provider: p, Interval: time.Hour}})
	stop, _ := h.run(t)
	defer stop()

	waitFor(t, func() bool { return p.runs.Load() >= 1 }, "first run")
	h.tick("stub", 4)
	// Wait for BOTH the run and its provider_runs record: the counter
	// bumps inside Run, the record lands right after — never race it.
	waitFor(t, func() bool { return p.runs.Load() >= 5 }, "5 runs (immediate + 4 ticks)")
	waitFor(t, func() bool {
		var rows int
		if err := h.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM provider_runs WHERE provider='stub' AND status='succeeded' AND items_found=3`).Scan(&rows); err != nil {
			return false
		}
		return rows == int(p.runs.Load())
	}, "provider_runs rows to match runs")
}

func TestPanicIsolatedAndRecorded(t *testing.T) {
	panicker := &stubProvider{name: "panicker", panic: true}
	healthy := &stubProvider{name: "healthy"}
	h := newTestScheduler(t, []Job{
		{Provider: panicker, Interval: time.Hour},
		{Provider: healthy, Interval: time.Hour},
	})

	stop, _ := h.run(t)
	defer stop()

	waitFor(t, func() bool { return panicker.runs.Load() >= 1 && healthy.runs.Load() >= 1 }, "first runs")
	h.tick("panicker", 2)
	h.tick("healthy", 2)
	waitFor(t, func() bool { return panicker.runs.Load() >= 3 && healthy.runs.Load() >= 3 }, "3 runs each")

	waitFor(t, func() bool {
		var failed int
		if err := h.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM provider_runs WHERE provider='panicker' AND status='failed'`).Scan(&failed); err != nil {
			return false
		}
		return failed == int(panicker.runs.Load())
	}, "every panic run recorded as failed")
	var errMsg string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT error_message FROM provider_runs WHERE provider='panicker' AND status='failed' LIMIT 1`).Scan(&errMsg); err != nil {
		t.Fatalf("read error_message: %v", err)
	}
	if errMsg == "" || !strings.Contains(errMsg, "panicked") {
		t.Errorf("error_message = %q, want it to mention the panic", errMsg)
	}
}

func TestFailingProviderRecordedButDoesNotStopOthers(t *testing.T) {
	failing := &stubProvider{name: "failing", err: stubErr{}}
	ok := &stubProvider{name: "ok"}
	h := newTestScheduler(t, []Job{
		{Provider: failing, Interval: time.Hour},
		{Provider: ok, Interval: time.Hour},
	})

	stop, _ := h.run(t)
	defer stop()

	h.tick("failing", 2)
	h.tick("ok", 2)
	waitFor(t, func() bool { return failing.runs.Load() >= 3 && ok.runs.Load() >= 3 }, "3 runs each")

	waitFor(t, func() bool {
		var failed, okSucceeded int
		if err := h.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM provider_runs WHERE provider='failing' AND status='failed'`).Scan(&failed); err != nil {
			return false
		}
		if err := h.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM provider_runs WHERE provider='ok' AND status='succeeded'`).Scan(&okSucceeded); err != nil {
			return false
		}
		return failed == int(failing.runs.Load()) && okSucceeded == int(ok.runs.Load())
	}, "run log to match both providers")
}

func TestGracefulShutdown(t *testing.T) {
	p := &stubProvider{name: "stub"}
	h := newTestScheduler(t, []Job{{Provider: p, Interval: time.Hour}})
	stop, _ := h.run(t)

	waitFor(t, func() bool { return p.runs.Load() >= 1 }, "first run")
	h.tick("stub", 2)
	waitFor(t, func() bool { return p.runs.Load() >= 3 }, "3 runs")

	stop() // graceful shutdown: every loop exits
	runsAfter := p.runs.Load()
	h.tick("stub", 3) // ticks after shutdown are buffered, never processed
	time.Sleep(20 * time.Millisecond)
	if got := p.runs.Load(); got != runsAfter {
		t.Errorf("runs after shutdown = %d, want %d (loops must stop)", got, runsAfter)
	}
}

type stubErr struct{}

func (stubErr) Error() string { return "stub failure" }
