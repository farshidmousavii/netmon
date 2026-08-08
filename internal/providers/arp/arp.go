// Package arp is the Phase 1 ARP evidence provider: it SNMP-walks the
// ARP tables (IP-MIB ipNetToPhysicalTable, falling back to
// ipNetToMediaTable) of every network_devices row with role='core',
// captures the VLAN of each entry from the SVI it was found on, and
// writes host_observations (source='arp') + hosts reconciliation.
//
// Read-only like every provider; must never import internal/device. The
// SNMP wire protocol is internal/snmp's responsibility (the Phase 0 typed
// table walk); this package programs against a walker function so tests
// feed it recorded varbind fixtures without live switches.
package arp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/domain"
	"github.com/farshidmousavii/bidar/internal/providers"
	"github.com/farshidmousavii/bidar/internal/providers/reconcile"
	"github.com/farshidmousavii/bidar/internal/snmp"
	"github.com/farshidmousavii/bidar/internal/store"
)

// MIB OIDs walked by the collector (see IP-MIB / IF-MIB):
const (
	oidIPNetToPhysicalPhysAddress = "1.3.6.1.2.1.4.35.1.4"   // suffix: ifIndex + ipAddress (4/16 octets)
	oidIPNetToMediaIfIndex        = "1.3.6.1.2.1.4.22.1.1"   // suffix: ipAddress; value: ifIndex
	oidIPNetToMediaPhysAddress    = "1.3.6.1.2.1.4.22.1.2"   // suffix: ipAddress; value: MAC
	oidIfName                     = "1.3.6.1.2.1.31.1.1.1.1" // suffix: ifIndex; value: "Vlan<N>"
)

// walkFunc walks one table column on one device. The real implementation
// is snmp.WalkTable; tests inject recorded fixtures.
type walkFunc func(ctx context.Context, cfg snmp.Config, baseOID string) ([]snmp.Varbind, error)

// Provider implements providers.Provider for the ARP collector.
type Provider struct {
	store  *store.Store
	enc    *crypto.Encryptor
	logger *slog.Logger
	walk   walkFunc

	health providers.Health
}

// New returns an ARP provider using real SNMP walks.
func New(st *store.Store, enc *crypto.Encryptor, logger *slog.Logger) (*Provider, error) {
	return newWithWalker(st, enc, logger, snmp.WalkTable)
}

// newWithWalker is New with an injectable walker (tests pass fixtures).
func newWithWalker(st *store.Store, enc *crypto.Encryptor, logger *slog.Logger, walk walkFunc) (*Provider, error) {
	if st == nil || enc == nil {
		return nil, fmt.Errorf("arp: store and encryptor are required")
	}
	if walk == nil {
		return nil, fmt.Errorf("arp: walker is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{store: st, enc: enc, logger: logger, walk: walk}, nil
}

// Name implements providers.Provider.
func (p *Provider) Name() string { return "arp" }

// Health implements providers.Provider.
func (p *Provider) Health() providers.Health { return p.health }

// Run implements providers.Provider: poll every role='core' device,
// reconcile hosts, append observations. Device-level failures are logged
// and isolated — one unreachable core must not stop the others. Run
// errors only when nothing at all could be polled.
func (p *Provider) Run(ctx context.Context) (providers.Result, error) {
	p.health = providers.Health{LastRunAt: time.Now()}
	if err := ctx.Err(); err != nil {
		p.fail(err)
		return providers.Result{}, fmt.Errorf("arp run cancelled: %w", err)
	}

	devices, err := p.store.ListCoreDevices(ctx)
	if err != nil {
		p.fail(err)
		return providers.Result{}, err
	}

	now := time.Now().UTC()
	var total int
	var deviceFailures int
	for _, dev := range devices {
		select {
		case <-ctx.Done():
			p.fail(ctx.Err())
			return providers.Result{}, fmt.Errorf("arp run cancelled: %w", ctx.Err())
		default:
		}

		n, err := p.pollDevice(ctx, dev, now)
		if err != nil {
			deviceFailures++
			p.logger.Error("arp: device poll failed", "device_id", dev.ID, "err", err)
			continue
		}
		total += n
	}

	if deviceFailures == len(devices) && len(devices) > 0 {
		err := fmt.Errorf("arp: all %d core devices failed to poll", len(devices))
		p.fail(err)
		return providers.Result{}, err
	}

	p.health = providers.Health{Healthy: true, LastRunAt: time.Now()}
	return providers.Result{ItemsFound: total}, nil
}

// pollDevice collects ARP entries from one core device and reconciles
// them into hosts + observations. Returns the number of entries found.
func (p *Provider) pollDevice(ctx context.Context, dev domain.Device, now time.Time) (int, error) {
	if dev.SNMPProfileID == nil {
		p.logger.Warn("arp: device has no snmp profile, skipping", "device_id", dev.ID)
		return 0, nil
	}
	profile, err := p.store.GetSNMPProfile(ctx, *dev.SNMPProfileID)
	if err != nil {
		return 0, fmt.Errorf("load snmp profile: %w", err)
	}
	if profile.Version != "v2c" {
		// Phase 1 collector supports v2c profiles; v3 mapping of the
		// encrypted auth/priv keys lands with Phase 2.
		return 0, fmt.Errorf("snmp profile version %q not supported by the ARP collector yet", profile.Version)
	}
	community, err := p.enc.Decrypt(profile.CommunityEncrypted)
	if err != nil {
		return 0, fmt.Errorf("decrypt snmp community: %w", err)
	}

	cfg := snmp.Config{
		Target:    dev.MgmtIP.String(),
		Community: string(community),
		Timeout:   time.Duration(profile.TimeoutMS) * time.Millisecond,
		Retries:   profile.Retries,
	}

	// ifIndex -> SVI info (VLAN inferred from the SVI name, per
	// architecture.md §Phase 1).
	ifNames, err := p.walk(ctx, cfg, oidIfName)
	if err != nil {
		p.logger.Warn("arp: ifName walk failed; VLANs will be NULL", "device_id", dev.ID, "err", err)
	}
	svis := buildSVIMap(ifNames)

	entries, err := p.walk(ctx, cfg, oidIPNetToPhysicalPhysAddress)
	if err != nil || len(entries) == 0 {
		// Older IOS lacks ipNetToPhysicalTable: fall back to the legacy
		// ipNetToMediaTable (two column walks keyed by IP).
		if err != nil {
			p.logger.Debug("arp: ipNetToPhysicalTable unavailable, falling back to ipNetToMediaTable",
				"device_id", dev.ID, "err", err)
		}
		entries, err = p.walkMediaTable(ctx, cfg)
		if err != nil {
			return 0, fmt.Errorf("walk arp table: %w", err)
		}
	}

	count := 0
	for _, vb := range entries {
		select {
		case <-ctx.Done():
			return count, ctx.Err()
		default:
		}

		ip, ifIndex, mac, ok := parsePhysicalVarbind(vb)
		if !ok {
			continue
		}
		if err := p.reconcile(ctx, dev, ip, ifIndex, mac, svis[ifIndex], now); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// walkMediaTable builds the same (ip, ifIndex, mac) entries from the
// legacy ipNetToMediaTable (indexed by IP address only).
func (p *Provider) walkMediaTable(ctx context.Context, cfg snmp.Config) ([]snmp.Varbind, error) {
	ifIndexRows, err := p.walk(ctx, cfg, oidIPNetToMediaIfIndex)
	if err != nil {
		return nil, err
	}
	macRows, err := p.walk(ctx, cfg, oidIPNetToMediaPhysAddress)
	if err != nil {
		return nil, err
	}

	// Convert media rows into the physical-table shape: suffix
	// [ifIndex, ip1..ip4], value = MAC bytes.
	ipToIf := make(map[string]int, len(ifIndexRows))
	for _, vb := range ifIndexRows {
		ip, ok := ipFromSuffix(vb.Suffix)
		if !ok {
			continue
		}
		if ifIdx, ok := vb.Value.(int); ok {
			ipToIf[ip.String()] = ifIdx
		}
	}

	var out []snmp.Varbind
	for _, vb := range macRows {
		ip, ok := ipFromSuffix(vb.Suffix)
		if !ok {
			continue
		}
		mac, ok := vb.Value.([]byte)
		if !ok || len(mac) != 6 {
			continue
		}
		ifIdx, ok := ipToIf[ip.String()]
		if !ok {
			continue
		}
		suffix := append([]int{ifIdx}, vb.Suffix...)
		out = append(out, snmp.Varbind{Suffix: suffix, Value: mac})
	}
	return out, nil
}

// reconcile applies the shared Phase 1 matching rules for one ARP entry
// (IP -> MAC -> insert) and appends one observation. host_observations is
// the history log; hosts holds current state — re-runs append observations
// but never duplicate hosts.
func (p *Provider) reconcile(ctx context.Context, dev domain.Device, ip netip.Addr, ifIndex int, mac net.HardwareAddr, svi sviInfo, now time.Time) error {
	ipCopy := ip
	macCopy := mac

	detail, err := json.Marshal(map[string]any{
		"device_id": dev.ID,
		"if_index":  ifIndex,
		"if_name":   svi.name,
		"vlan":      svi.vlan,
	})
	if err != nil {
		return fmt.Errorf("marshal arp detail: %w", err)
	}

	hostID, err := reconcile.Host(ctx, p.store, nil, &ipCopy, &macCopy, svi.vlan, vlanSource(svi.vlan), now)
	if err != nil {
		return err
	}
	return reconcile.Observation(ctx, p.store, hostID, "arp", nil, &ipCopy, &macCopy, svi.vlan, detail, now)
}

func vlanSource(vlan *int32) *string {
	if vlan == nil {
		return nil
	}
	s := "arp_svi"
	return &s
}

// sviInfo is the VLAN inferred from one SVI's ifName.
type sviInfo struct {
	name string
	vlan *int32
}

// buildSVIMap extracts ifIndex -> SVI name + VLAN from an ifName walk.
func buildSVIMap(varbinds []snmp.Varbind) map[int]sviInfo {
	out := make(map[int]sviInfo, len(varbinds))
	for _, vb := range varbinds {
		if len(vb.Suffix) != 1 {
			continue
		}
		name, ok := vb.Value.([]byte)
		if !ok {
			continue
		}
		out[vb.Suffix[0]] = sviInfo{name: string(name), vlan: parseVLANFromIfName(string(name))}
	}
	return out
}

// parseVLANFromIfName extracts the VLAN number from a Cisco-style SVI
// name ("Vlan20", "VLAN 20", "vlan20") — case- and space-insensitive.
// VLAN inference is best-effort: non-SVI names (Gi0/1, ether1), empty
// digits, and out-of-range values all yield nil, never an error — a weird
// ifName must not fail a whole device poll.
func parseVLANFromIfName(name string) *int32 {
	n := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(name), " ", ""))
	if !strings.HasPrefix(n, "VLAN") {
		return nil
	}
	digits := strings.TrimPrefix(n, "VLAN")
	if digits == "" {
		return nil
	}
	v, err := strconv.ParseInt(digits, 10, 32)
	if err != nil || v < 0 || v > 4094 {
		return nil
	}
	out := int32(v)
	return &out
}

// parsePhysicalVarbind extracts (ip, ifIndex, mac) from one
// ipNetToPhysicalTable row: suffix [ifIndex, ip1..ip4], value = MAC bytes.
func parsePhysicalVarbind(vb snmp.Varbind) (netip.Addr, int, net.HardwareAddr, bool) {
	if len(vb.Suffix) < 5 {
		return netip.Addr{}, 0, nil, false
	}
	ip, ok := ipFromSuffix(vb.Suffix[1:])
	if !ok {
		return netip.Addr{}, 0, nil, false
	}
	mac, ok := vb.Value.([]byte)
	if !ok || len(mac) != 6 {
		return netip.Addr{}, 0, nil, false
	}
	return ip, vb.Suffix[0], net.HardwareAddr(mac), true
}

// ipFromSuffix builds an IPv4 (4 octets) or IPv6 (16 octets) address from
// the index components.
func ipFromSuffix(suffix []int) (netip.Addr, bool) {
	if len(suffix) == 4 {
		return netip.AddrFrom4([4]byte{byte(suffix[0]), byte(suffix[1]), byte(suffix[2]), byte(suffix[3])}), true
	}
	if len(suffix) == 16 {
		var b [16]byte
		for i, c := range suffix {
			b[i] = byte(c)
		}
		return netip.AddrFrom16(b), true
	}
	return netip.Addr{}, false
}

func (p *Provider) fail(err error) {
	p.health.Healthy = false
	p.health.LastError = err.Error()
}
