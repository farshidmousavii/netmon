// Package icmpsweep is the Phase 1 liveness provider: it pings every
// enabled subnet's host range and writes host_observations (source='icmp').
//
// On a successful ping it does a best-effort reverse-DNS (PTR) lookup and
// records the hostname on the observation when available; a failed lookup
// is just missing evidence, never a provider error (relies on the DHCP
// server's dynamic DNS registration, already enabled in this environment).
//
// ICMP carries no identity of its own — no MAC, no hostname, no VLAN — so
// it never CREATES hosts. An IP that answers ping updates an existing
// host's liveness (matched by IP) or records an unlinked observation for
// later ARP/DHCP evidence to build on. A bare responding IP (gateway,
// VIP, the daemon's own gateway) is not, by itself, a device.
//
// The pinger is an injected function so tests need no real network; the
// production backend execs the system ping binary (standard on Linux;
// needs a working ping on the host — see the deployment note).
package icmpsweep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/farshidmousavii/bidar/internal/domain"
	"github.com/farshidmousavii/bidar/internal/providers"
	"github.com/farshidmousavii/bidar/internal/providers/reconcile"
	"github.com/farshidmousavii/bidar/internal/store"
)

// Defaults (flag-worthy choices, constructor-overridable):
const (
	// DefaultConcurrency bounds parallel pings: 32 workers keep a /22
	// (~1000 hosts) under a minute while never flooding the network or
	// the process table.
	DefaultConcurrency = 32
	// DefaultTimeout is the per-host ping timeout.
	DefaultTimeout = 1 * time.Second
	// DefaultMaxSubnetHosts refuses to sweep ranges larger than a /16:
	// a misconfigured /8 would otherwise mean 16M pings.
	DefaultMaxSubnetHosts = 65536
)

// Config is the ICMP sweep's own configuration.
type Config struct {
	Concurrency    int
	Timeout        time.Duration
	MaxSubnetHosts int
}

// pinger reports whether one IP answered a ping.
type pinger func(ctx context.Context, ip netip.Addr) bool

// lookupAddr does reverse DNS; injectable for tests.
type lookupAddr func(ctx context.Context, addr string) ([]string, error)

// Provider implements providers.Provider for the ICMP sweep.
type Provider struct {
	store  *store.Store
	logger *slog.Logger
	cfg    Config
	ping   pinger
	lookup lookupAddr

	health providers.Health
}

// New returns an ICMP sweep provider using the system ping binary.
func New(cfg Config, st *store.Store, logger *slog.Logger) (*Provider, error) {
	p, err := newWithPinger(cfg, st, logger, nil)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// newWithPinger is New with an injectable pinger (and lookup) for tests.
func newWithPinger(cfg Config, st *store.Store, logger *slog.Logger, ping pinger) (*Provider, error) {
	return newWithLookup(cfg, st, logger, ping, nil)
}

func newWithLookup(cfg Config, st *store.Store, logger *slog.Logger, ping pinger, lookup lookupAddr) (*Provider, error) {
	if st == nil {
		return nil, fmt.Errorf("icmpsweep: store is required")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = DefaultConcurrency
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxSubnetHosts <= 0 {
		cfg.MaxSubnetHosts = DefaultMaxSubnetHosts
	}
	if logger == nil {
		logger = slog.Default()
	}
	p := &Provider{store: st, logger: logger, cfg: cfg}
	if ping == nil {
		ping = p.realPing
	}
	p.ping = ping
	if lookup == nil {
		lookup = func(ctx context.Context, addr string) ([]string, error) {
			var r net.Resolver
			return r.LookupAddr(ctx, addr)
		}
	}
	p.lookup = lookup
	return p, nil
}

// Name implements providers.Provider.
func (p *Provider) Name() string { return "icmp" }

// Health implements providers.Provider.
func (p *Provider) Health() providers.Health { return p.health }

// Run implements providers.Provider: sweep every enabled subnet, bounded
// by the worker count. Subnet-level failures (e.g. too-large ranges) are
// logged and skipped, never fatal.
func (p *Provider) Run(ctx context.Context) (providers.Result, error) {
	p.health = providers.Health{LastRunAt: time.Now()}
	if err := ctx.Err(); err != nil {
		p.fail(err)
		return providers.Result{}, fmt.Errorf("icmp run cancelled: %w", err)
	}

	subnets, err := p.store.ListEnabledSubnets(ctx)
	if err != nil {
		p.fail(err)
		return providers.Result{}, err
	}

	now := time.Now().UTC()
	var total int
	for _, sn := range subnets {
		select {
		case <-ctx.Done():
			p.fail(ctx.Err())
			return providers.Result{}, fmt.Errorf("icmp run cancelled: %w", ctx.Err())
		default:
		}

		n, err := p.sweepSubnet(ctx, sn, now)
		if err != nil {
			p.logger.Error("icmpsweep: subnet sweep failed", "subnet_id", sn.ID, "cidr", sn.CIDR, "err", err)
			continue
		}
		total += n
	}

	p.health = providers.Health{Healthy: true, LastRunAt: time.Now()}
	return providers.Result{ItemsFound: total}, nil
}

// sweepSubnet pings one subnet's usable addresses and writes observations.
func (p *Provider) sweepSubnet(ctx context.Context, sn domain.Subnet, now time.Time) (int, error) {
	if n := hostCount(sn.CIDR); n > p.cfg.MaxSubnetHosts {
		p.logger.Warn("icmpsweep: subnet larger than the sweep limit, skipping",
			"cidr", sn.CIDR, "hosts", n, "max", p.cfg.MaxSubnetHosts)
		return 0, nil
	}
	addrs := usableAddresses(sn.CIDR)

	detail, err := json.Marshal(map[string]any{"subnet_id": sn.ID, "subnet": sn.CIDR.String()})
	if err != nil {
		return 0, fmt.Errorf("marshal icmp detail: %w", err)
	}

	results := make(chan netip.Addr, len(addrs))

	sem := make(chan struct{}, p.cfg.Concurrency)
	var wg sync.WaitGroup
	for _, ip := range addrs {
		select {
		case <-ctx.Done():
			wg.Wait()
			return 0, ctx.Err()
		default:
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(ip netip.Addr) {
			defer wg.Done()
			defer func() { <-sem }()
			if p.ping(ctx, ip) {
				results <- ip
			}
		}(ip)
	}
	wg.Wait()
	close(results)

	// Write observations after the sweep, in subnet order, so a cancelled
	// run never half-writes.
	count := 0
	for ip := range results {
		select {
		case <-ctx.Done():
			return count, ctx.Err()
		default:
		}
		if err := p.reconcilePing(ctx, ip, detail, now); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// reconcilePing links a live IP to an existing host (never creates one)
// and appends the observation. On success it does a best-effort PTR
// lookup and records the hostname when available.
func (p *Provider) reconcilePing(ctx context.Context, ip netip.Addr, detail []byte, now time.Time) error {
	ipCopy := ip

	// Best-effort reverse DNS (depends on DHCP server's dynamic DNS
	// registration; already enabled in this environment). A failed lookup
	// is just missing evidence.
	var ptrHostname *string
	if names, err := p.lookupAddrWithTimeout(ctx, ip); err == nil && len(names) > 0 {
		h := strings.TrimSuffix(names[0], ".")
		if h != "" {
			ptrHostname = &h
		}
	}

	host, err := reconcile.Existing(ctx, p.store, &ipCopy)
	if err != nil {
		return err
	}
	if host == nil {
		return reconcile.UnlinkedObservation(ctx, p.store, "icmp", &ipCopy, ptrHostname, detail, now)
	}

	// Pass PTR hostname to fill an empty host.hostname, but never
	// overwrite an AD-assigned name (COALESCE in the store query).
	if err := p.store.UpdateHostFromPresence(ctx, host.ID, ptrHostname, &ipCopy, nil, nil, nil, now); err != nil {
		return err
	}
	return reconcile.Observation(ctx, p.store, host.ID, "icmp", ptrHostname, &ipCopy, nil, nil, detail, now)
}

// hostCount is the number of pingable addresses in a prefix (network and
// broadcast excluded for IPv4 prefixes < 31) — used to guard sweep size
// BEFORE enumerating anything.
func hostCount(p netip.Prefix) int {
	p = p.Masked()
	if !p.IsValid() {
		return 0
	}
	bits := p.Addr().BitLen()
	total := 1 << (bits - p.Bits())
	if bits == 32 && p.Bits() < 31 {
		total -= 2 // network + broadcast
	}
	if total < 0 {
		return 0
	}
	return total
}

// usableAddresses lists the pingable addresses of a prefix.
func usableAddresses(p netip.Prefix) []netip.Addr {
	p = p.Masked()
	if !p.IsValid() {
		return nil
	}
	bits := p.Addr().BitLen()

	var out []netip.Addr
	for a := p.Addr(); p.Contains(a); a = a.Next() {
		if a.IsMulticast() || a.IsUnspecified() {
			continue
		}
		if bits == 32 && p.Bits() < 31 {
			if a == p.Addr() { // network address
				continue
			}
			// Broadcast = network + (2^(32-bits) - 1).
			last := p.Addr()
			for i := 0; i < (1<<(32-p.Bits()))-1; i++ {
				last = last.Next()
			}
			if a == last { // broadcast address
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

// realPing execs the system ping binary: `ping -n -c 1 -W <timeout>`.
// The exit code is the liveness signal; a failure other than "host did
// not answer" (missing binary, permission denied, ...) is logged at
// debug so a misconfigured sweep is diagnosable instead of silently
// reporting every host as dead. Requires a working ping on the host
// (standard on Linux distributions; the container needs the binary or a
// raw-socket capability — NET_RAW is granted in docker-compose.yml).
func (p *Provider) realPing(ctx context.Context, ip netip.Addr) bool {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ping", "-n", "-c", "1",
		"-W", strconv.Itoa(int(p.cfg.Timeout.Seconds())), ip.String())
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			// Not a normal "no reply" exit: the ping itself is broken.
			p.logger.Debug("icmpsweep: ping invocation failed", "ip", ip, "err", err)
		}
		return false
	}
	return true
}

func (p *Provider) lookupAddrWithTimeout(ctx context.Context, ip netip.Addr) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return p.lookup(ctx, ip.String())
}

func (p *Provider) fail(err error) {
	p.health.Healthy = false
	p.health.LastError = err.Error()
}
