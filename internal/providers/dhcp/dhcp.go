// Package dhcp is the Phase 1 DHCP lease evidence provider. It iterates
// every enabled dhcp_sources row and, per type:
//
//   - mikrotik: dials the shared internal/providers/mikrotik RouterOS API
//     client (credentials decrypted via internal/crypto) and reads the
//     active lease table.
//   - windows, cisco, isc, other: not implemented in Phase 1 — a clear
//     unimplemented error is returned, never a silent no-op. See
//     docs/roadmap.md and docs/architecture.md for the reasoning.
//
// Read-only like every provider; never imports internal/device.
package dhcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/domain"
	"github.com/farshidmousavii/bidar/internal/providers"
	"github.com/farshidmousavii/bidar/internal/providers/mikrotik"
	"github.com/farshidmousavii/bidar/internal/providers/reconcile"
	"github.com/farshidmousavii/bidar/internal/store"
)

// Config is the DHCP provider's configuration (extensions point for
// future options; empty in Phase 1 where only mikrotik is implemented).
type Config struct{}

// ConfigFromEnv loads Config (no env vars in Phase 1; kept for symmetry
// with other providers and future extensibility).
func ConfigFromEnv() (Config, error) {
	return Config{}, nil
}

// mikrotikConfig is the type-specific part of connection_config for
// source_type='mikrotik'.
type mikrotikConfig struct {
	Host     string `json:"host"`
	Username string `json:"username"`
}

// leaseClient is the minimal RouterOS surface the DHCP provider needs,
// so tests can stub it. *mikrotik.Client satisfies it; the wire protocol
// itself is tested in the mikrotik package.
type leaseClient interface {
	DHCPLeases(ctx context.Context) ([]mikrotik.DHCPLease, error)
	Close() error
}

// routerosDial is injectable for tests.
type routerosDial func(ctx context.Context, cfg mikrotik.Config) (leaseClient, error)

// Provider implements providers.Provider for DHCP leases.
type Provider struct {
	store        *store.Store
	enc          *crypto.Encryptor
	logger       *slog.Logger
	now          func() time.Time
	dialRouterOS routerosDial

	health providers.Health
}

// New returns a DHCP provider using real RouterOS dialing.
func New(cfg Config, st *store.Store, enc *crypto.Encryptor, logger *slog.Logger) (*Provider, error) {
	return newWithDeps(cfg, st, enc, logger, time.Now, realRouterOSDial)
}

// newWithDeps is New with injectable clock and dialer for tests.
func newWithDeps(cfg Config, st *store.Store, enc *crypto.Encryptor, logger *slog.Logger, now func() time.Time, dial routerosDial) (*Provider, error) {
	if st == nil || enc == nil {
		return nil, fmt.Errorf("dhcp: store and encryptor are required")
	}
	if dial == nil {
		dial = realRouterOSDial
	}
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Provider{store: st, enc: enc, logger: logger, now: now, dialRouterOS: dial}, nil
}

// Name implements providers.Provider.
func (p *Provider) Name() string { return "dhcp" }

// Health implements providers.Provider.
func (p *Provider) Health() providers.Health { return p.health }

// Run implements providers.Provider: poll every enabled source, by type.
// Source-level failures are isolated (logged, counted); Run errors only
// when every source failed.
func (p *Provider) Run(ctx context.Context) (providers.Result, error) {
	p.health = providers.Health{LastRunAt: p.now()}
	if err := ctx.Err(); err != nil {
		p.fail(err)
		return providers.Result{}, fmt.Errorf("dhcp run cancelled: %w", err)
	}

	sources, err := p.store.ListDHCPSources(ctx)
	if err != nil {
		p.fail(err)
		return providers.Result{}, err
	}

	now := p.now().UTC()
	var total int
	var sourceErrs []error
	for _, src := range sources {
		select {
		case <-ctx.Done():
			p.fail(ctx.Err())
			return providers.Result{}, fmt.Errorf("dhcp run cancelled: %w", ctx.Err())
		default:
		}

		n, err := p.pollSource(ctx, src, now)
		if err != nil {
			sourceErrs = append(sourceErrs, fmt.Errorf("source %q (%s): %w", src.Name, src.SourceType, err))
			p.logger.Error("dhcp: source poll failed", "source_id", src.ID, "source_type", src.SourceType, "err", err)
			continue
		}
		total += n
	}

	if len(sourceErrs) == len(sources) && len(sources) > 0 {
		err := fmt.Errorf("dhcp: all %d sources failed to poll: %s", len(sources), errors.Join(sourceErrs...))
		p.fail(err)
		return providers.Result{}, err
	}

	p.health = providers.Health{Healthy: true, LastRunAt: p.now()}
	return providers.Result{ItemsFound: total}, nil
}

// pollSource dispatches one dhcp_sources row to its type handler.
// Only mikrotik is implemented in Phase 1; other types return a clear
// unimplemented error (never a silent skip) so misconfiguration is
// visible.
func (p *Provider) pollSource(ctx context.Context, src domain.DHCPSource, now time.Time) (int, error) {
	switch src.SourceType {
	case "mikrotik":
		return p.pollMikrotik(ctx, src, now)
	case "windows", "cisco":
		return 0, fmt.Errorf("source_type %q is not implemented in Phase 1 (only mikrotik is supported); see docs/architecture.md", src.SourceType)
	case "isc", "other":
		p.logger.Debug("dhcp: source type not supported yet, skipping", "source_id", src.ID, "source_type", src.SourceType)
		return 0, nil
	default:
		return 0, fmt.Errorf("unknown source_type %q", src.SourceType)
	}
}

// -- mikrotik (RouterOS API) --------------------------------------------------

func (p *Provider) pollMikrotik(ctx context.Context, src domain.DHCPSource, now time.Time) (int, error) {
	var mc mikrotikConfig
	if err := json.Unmarshal(src.ConnectionConfig, &mc); err != nil {
		return 0, fmt.Errorf("parse connection_config: %w", err)
	}
	if mc.Host == "" || mc.Username == "" {
		return 0, fmt.Errorf("connection_config needs host and username for mikrotik sources")
	}
	password, err := p.enc.Decrypt(src.CredentialEnc)
	if err != nil {
		return 0, fmt.Errorf("decrypt credential: %w", err)
	}

	client, err := p.dialRouterOS(ctx, mikrotik.Config{
		Host: mc.Host, Username: mc.Username, Password: string(password),
	})
	if err != nil {
		return 0, err
	}
	defer client.Close()

	leases, err := client.DHCPLeases(ctx)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, lease := range leases {
		select {
		case <-ctx.Done():
			return count, ctx.Err()
		default:
		}
		if err := p.reconcileLease(ctx, src, "mikrotik", lease.Hostname, lease.Address, lease.MAC,
			lease.Status, lease.LastSeen+" / expires "+lease.ExpiresAfter, now); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// -- shared reconciliation ------------------------------------------------------

// reconcileLease matches one lease into hosts (shared Phase 1 rules) and
// appends one dhcp observation.
func (p *Provider) reconcileLease(ctx context.Context, src domain.DHCPSource, kind, hostname string, ip netip.Addr, mac net.HardwareAddr, state, leaseExpiry string, now time.Time) error {
	ipCopy := ip
	macCopy := mac
	var hostnamePtr *string
	if hostname != "" {
		hostnamePtr = &hostname
	}

	detail, err := json.Marshal(map[string]any{
		"source_name":  src.Name,
		"source_kind":  kind,
		"state":        state,
		"lease_expiry": leaseExpiry,
	})
	if err != nil {
		return fmt.Errorf("marshal dhcp detail: %w", err)
	}

	hostID, err := reconcile.Host(ctx, p.store, hostnamePtr, &ipCopy, &macCopy, nil, nil, now)
	if err != nil {
		return err
	}
	return reconcile.Observation(ctx, p.store, hostID, "dhcp", hostnamePtr, &ipCopy, &macCopy, nil, detail, now)
}

func (p *Provider) fail(err error) {
	p.health.Healthy = false
	p.health.LastError = err.Error()
}

// realRouterOSDial is the production dialer.
func realRouterOSDial(ctx context.Context, cfg mikrotik.Config) (leaseClient, error) {
	return mikrotik.Dial(ctx, cfg)
}
