// Package dhcp is the Phase 1 DHCP lease evidence provider. It iterates
// every enabled dhcp_sources row and, per type:
//
//   - windows: reads a JSON lease-export file (produced on the DHCP server
//     by scripts/export-dhcp-leases.ps1, made reachable by the operator —
//     e.g. an OS-level SMB mount; no SMB client lives in this codebase).
//     The file must carry an exported_at timestamp newer than the
//     configured staleness threshold — an old or missing file is a source
//     failure, never silently treated as fresh.
//   - mikrotik: dials the shared internal/providers/mikrotik RouterOS API
//     client (credentials decrypted via internal/crypto) and prints the
//     active lease table.
//   - isc/other: skipped cleanly (not needed for this deployment).
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
	"os"
	"strings"
	"time"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/domain"
	"github.com/farshidmousavii/bidar/internal/envconfig"
	"github.com/farshidmousavii/bidar/internal/providers"
	"github.com/farshidmousavii/bidar/internal/providers/mikrotik"
	"github.com/farshidmousavii/bidar/internal/providers/reconcile"
	"github.com/farshidmousavii/bidar/internal/store"
)

// DefaultStaleness is how old a Windows lease-export file may be before
// the collector refuses it (configurable via BIDAR_DHCP_STALENESS).
// 24h matches a daily scheduled task with generous slack; a broken task
// shows up as a failed source within a day.
const DefaultStaleness = 24 * time.Hour

// Config is the DHCP provider's own configuration.
type Config struct {
	// Staleness bounds the age of Windows lease-export files.
	Staleness time.Duration
}

// ConfigFromEnv loads Config from BIDAR_DHCP_STALENESS (Go duration,
// default 24h). An unparsable value fails loudly rather than silently
// accepting everything as fresh.
func ConfigFromEnv() (Config, error) {
	raw := strings.TrimSpace(os.Getenv(envconfig.DHCPStaleness))
	if raw == "" {
		return Config{Staleness: DefaultStaleness}, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return Config{}, fmt.Errorf("%s: invalid duration %q (use e.g. \"24h\"): %w", envconfig.DHCPStaleness, raw, err)
	}
	return Config{Staleness: d}, nil
}

// windowsConfig is the type-specific part of connection_config for
// source_type='windows': the lease-export file path.
type windowsConfig struct {
	Path string `json:"path"`
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
	staleness    time.Duration
	now          func() time.Time
	dialRouterOS routerosDial

	health providers.Health
}

// New returns a DHCP provider using real file reads and RouterOS dialing.
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
	if cfg.Staleness <= 0 {
		cfg.Staleness = DefaultStaleness
	}
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Provider{store: st, enc: enc, logger: logger, staleness: cfg.Staleness, now: now, dialRouterOS: dial}, nil
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
func (p *Provider) pollSource(ctx context.Context, src domain.DHCPSource, now time.Time) (int, error) {
	switch src.SourceType {
	case "windows":
		return p.pollWindowsFile(ctx, src, now)
	case "mikrotik":
		return p.pollMikrotik(ctx, src, now)
	case "isc", "other":
		p.logger.Debug("dhcp: source type not supported yet, skipping", "source_id", src.ID, "source_type", src.SourceType)
		return 0, nil
	default:
		return 0, fmt.Errorf("unknown source_type %q", src.SourceType)
	}
}

// -- windows (file export, Method B) -----------------------------------------

// windowsExport mirrors scripts/export-dhcp-leases.ps1's JSON envelope.
type windowsExport struct {
	ExportedAt string         `json:"exported_at"`
	Server     string         `json:"server"`
	Leases     []windowsLease `json:"leases"`
}

// windowsLease mirrors Get-DhcpServerv4Lease's serialized fields.
type windowsLease struct {
	AddressID       string `json:"AddressId"`
	ClientID        string `json:"ClientId"`
	HostName        string `json:"HostName"`
	AddressState    string `json:"AddressState"`
	LeaseExpiryTime string `json:"LeaseExpiryTime"`
}

func (p *Provider) pollWindowsFile(ctx context.Context, src domain.DHCPSource, now time.Time) (int, error) {
	var wc windowsConfig
	if err := json.Unmarshal(src.ConnectionConfig, &wc); err != nil {
		return 0, fmt.Errorf("parse connection_config: %w", err)
	}
	if wc.Path == "" {
		return 0, fmt.Errorf("connection_config.path is empty (lease export file)")
	}

	data, err := os.ReadFile(wc.Path)
	if err != nil {
		return 0, fmt.Errorf("read lease export %q: %w", wc.Path, err)
	}

	var export windowsExport
	if err := json.Unmarshal(data, &export); err != nil {
		return 0, fmt.Errorf("parse lease export %q: %w", wc.Path, err)
	}
	exportedAt, err := time.Parse(time.RFC3339, export.ExportedAt)
	if err != nil {
		return 0, fmt.Errorf("parse exported_at in %q: %w", wc.Path, err)
	}
	age := now.Sub(exportedAt)
	if age > p.staleness || age < 0 {
		return 0, fmt.Errorf("lease export %q is %s old (stale: threshold %s)", wc.Path, age.Round(time.Second), p.staleness)
	}

	count := 0
	for _, lease := range export.Leases {
		select {
		case <-ctx.Done():
			return count, ctx.Err()
		default:
		}
		// Only actively leased addresses are network-presence evidence;
		// "Offered"/"Expired" rows are noise for a live inventory.
		if !strings.EqualFold(lease.AddressState, "Active") {
			continue
		}
		ip, err := netip.ParseAddr(lease.AddressID)
		if err != nil {
			continue
		}
		mac, err := net.ParseMAC(normalizeWindowsMAC(lease.ClientID))
		if err != nil {
			continue
		}
		if err := p.reconcileLease(ctx, src, "windows", lease.HostName, ip, mac, lease.AddressState, lease.LeaseExpiryTime, now); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// normalizeWindowsMAC converts Get-DhcpServerv4Lease's "00-11-22-33-44-55"
// into net.ParseMAC's "00:11:22:33:44:55".
func normalizeWindowsMAC(clientID string) string {
	return strings.ReplaceAll(strings.TrimSpace(clientID), "-", ":")
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
