package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/db"
	"github.com/farshidmousavii/bidar/internal/dlog"
	"github.com/farshidmousavii/bidar/internal/envconfig"
	"github.com/farshidmousavii/bidar/internal/providers/ad"
	"github.com/farshidmousavii/bidar/internal/providers/arp"
	"github.com/farshidmousavii/bidar/internal/providers/dhcp"
	"github.com/farshidmousavii/bidar/internal/providers/icmpsweep"
	mikrotikpoll "github.com/farshidmousavii/bidar/internal/providers/mikrotik"
	snmppoll "github.com/farshidmousavii/bidar/internal/providers/snmp"
	"github.com/farshidmousavii/bidar/internal/scheduler"
	"github.com/farshidmousavii/bidar/internal/store"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the inventory daemon",
	Long: `Run the inventory daemon: the four Phase 1 providers (AD, ARP,
DHCP, ICMP) as scheduled loops, each run logged to provider_runs, plus
the Phase 2 polling queue — Cisco and MikroTik devices polled through
the discovery_jobs table by a worker pool, with per-device health and
circuit-breaker backoff on network_devices.

Environment (the daemon is env-configured, not --config):
  BIDAR_DATABASE_URL      Postgres connection string (required)
  BIDAR_MASTER_KEY        base64 32-byte key (required; validated at startup)
  BIDAR_LOG_LEVEL         debug|info|warn|error (default info)
  BIDAR_AD_INTERVAL       AD sync cadence (default 24h; AD is optional —
                          skipped with a warning when BIDAR_AD_* unset)
  BIDAR_ARP_INTERVAL      ARP poll cadence (default 5m)
  BIDAR_DHCP_INTERVAL     DHCP poll cadence (default 5m)
  BIDAR_ICMP_INTERVAL     ICMP sweep cadence (default 5m)
  BIDAR_JOB_WORKERS       polling-queue worker count (default 5)
  BIDAR_ENQUEUE_INTERVAL  queue enqueue-pass cadence (default 1m)
  BIDAR_JOB_LEASE         max duration of one claimed job (default 10m)

The schema must already be migrated with ` + "`bidar migrate`" + ` — serve
never applies migrations itself and fails clearly if the schema is
missing, dirty, or at the wrong version.`,
	Run: runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) {
	if err := serve(cmd.Context()); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

// serve runs the daemon until ctx is cancelled (SIGINT/SIGTERM via
// cmd/bidar/main.go's signal wiring). Every configuration input is
// validated BEFORE the startup message is logged, so a misconfigured
// daemon fails loud and immediately instead of hanging ambiguously.
func serve(ctx context.Context) error {
	logger, err := dlog.NewFromEnv()
	if err != nil {
		return err
	}

	databaseURL, err := db.DatabaseURLFromEnv()
	if err != nil {
		return err
	}

	// Master key: validated at startup. Every provider that touches
	// encrypted data (ARP communities, MikroTik credentials) needs it;
	// a daemon without its key fails now, not mid-operation.
	enc, err := crypto.NewFromEnv()
	if err != nil {
		return err
	}

	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	version, err := db.EnsureSchemaUpToDate(ctx, pool)
	if err != nil {
		return err
	}

	level, err := dlog.LevelFromEnv()
	if err != nil {
		return err
	}
	logger.Info("starting",
		"log_level", level.String(),
		"database_reachable", true,
		"schema_version", version,
	)

	st := store.New(pool)
	jobs, err := buildJobs(ctx, logger, st, enc)
	if err != nil {
		return err
	}
	sched := scheduler.New(st, logger, jobs)

	queue, err := buildQueue(logger, st, enc)
	if err != nil {
		return err
	}

	// Decision D (architecture.md): Phase 2 device polling runs entirely
	// through discovery_jobs; the Phase 1 collectors keep their simple
	// loops. Both families run until ctx is cancelled.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sched.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		if err := queue.Run(ctx); err != nil {
			logger.Error("polling queue exited with error", "err", err)
		}
	}()
	wg.Wait()

	logger.Info("shutting down", "reason", ctx.Err().Error())
	return nil
}

// buildQueue constructs the Phase 2 polling queue: Cisco devices via the
// SNMP provider, MikroTik devices via the RouterOS provider, both as
// per-device executors over discovery_jobs.
func buildQueue(logger *slog.Logger, st *store.Store, enc *crypto.Encryptor) (*scheduler.QueueRunner, error) {
	workers, err := intFromEnv(envconfig.JobWorkers, 5)
	if err != nil {
		return nil, err
	}
	enqInterval, err := intervalFromEnv(envconfig.EnqueueInterval, time.Minute)
	if err != nil {
		return nil, err
	}
	lease, err := intervalFromEnv(envconfig.JobLease, 10*time.Minute)
	if err != nil {
		return nil, err
	}

	snmpP, err := snmppoll.New(st, enc, logger)
	if err != nil {
		return nil, fmt.Errorf("build snmp provider: %w", err)
	}
	mtP, err := mikrotikpoll.New(st, enc, logger)
	if err != nil {
		return nil, fmt.Errorf("build mikrotik provider: %w", err)
	}

	return scheduler.NewQueueRunner(st, logger, scheduler.QueueConfig{
		Workers:         workers,
		EnqueueInterval: enqInterval,
		Lease:           lease,
	}, map[string]scheduler.Executor{
		"snmp":     snmpP.PollDeviceByID,
		"mikrotik": mtP.PollDeviceByID,
	})
}

// intFromEnv parses a positive integer env var, defaulting when unset.
func intFromEnv(name string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s: invalid positive integer %q", name, raw)
	}
	return n, nil
}

// buildJobs constructs the four provider jobs. AD is optional: without
// BIDAR_AD_* configuration the daemon still runs (log warning) — AD being
// down must never take the rest of the system with it, and an
// unconfigured directory is the same category of problem. The other three
// providers read their targets from the database and are always
// scheduled; empty target lists make them cheap no-op runs.
func buildJobs(ctx context.Context, logger *slog.Logger, st *store.Store, enc *crypto.Encryptor) ([]scheduler.Job, error) {
	var jobs []scheduler.Job

	adInterval, err := intervalFromEnv(envconfig.ADInterval, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	if adCfg, cfgErr := ad.ConfigFromEnv(); cfgErr == nil {
		p, err := ad.New(adCfg, st, logger)
		if err != nil {
			return nil, fmt.Errorf("build ad provider: %w", err)
		}
		jobs = append(jobs, scheduler.Job{Provider: p, Interval: adInterval})
	} else {
		logger.Warn("AD provider not configured; skipping (set BIDAR_AD_URL, BIDAR_AD_BIND_DN, BIDAR_AD_BIND_PASSWORD, BIDAR_AD_BASE_DN to enable)", "err", cfgErr)
	}

	arpInterval, err := intervalFromEnv(envconfig.ARPInterval, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	arpP, err := arp.New(st, enc, logger)
	if err != nil {
		return nil, fmt.Errorf("build arp provider: %w", err)
	}
	jobs = append(jobs, scheduler.Job{Provider: arpP, Interval: arpInterval})

	dhcpInterval, err := intervalFromEnv(envconfig.DHCPInterval, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	dhcpCfg, err := dhcp.ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	dhcpP, err := dhcp.New(dhcpCfg, st, enc, logger)
	if err != nil {
		return nil, fmt.Errorf("build dhcp provider: %w", err)
	}
	jobs = append(jobs, scheduler.Job{Provider: dhcpP, Interval: dhcpInterval})

	icmpInterval, err := intervalFromEnv(envconfig.ICMPInterval, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	icmpP, err := icmpsweep.New(icmpsweep.Config{}, st, logger)
	if err != nil {
		return nil, fmt.Errorf("build icmp provider: %w", err)
	}
	jobs = append(jobs, scheduler.Job{Provider: icmpP, Interval: icmpInterval})

	return jobs, nil
}

// intervalFromEnv parses a BIDAR_* interval duration, defaulting when
// unset and failing loudly on garbage.
func intervalFromEnv(name string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s: invalid duration %q (use e.g. \"5m\"): %w", name, raw, err)
	}
	return d, nil
}
