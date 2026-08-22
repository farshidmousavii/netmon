// Package snmp is the Phase 2 Cisco polling provider: for every enabled
// cisco_snmp device it reads system info (sysDescr/sysName/sysUpTime),
// the interface table (IF-MIB ifXTable + ifTable columns joined by
// ifIndex), and VLANs (Q-BRIDGE static names unioned with port PVIDs),
// writing device_interfaces, device_vlans, and per-device poll health.
//
// Role plays no part here — it is an ARP-collector-only concept; this
// provider polls every enabled cisco_snmp row. MAC tables and LLDP/CDP
// neighbors are deliberately later tasks in this phase (4b/4c).
//
// Read-only like every provider; must never import internal/device. The
// SNMP wire protocol is internal/snmp's responsibility; this package
// programs against a small client interface so tests feed recorded
// fixtures without live switches.
package snmp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/domain"
	"github.com/farshidmousavii/bidar/internal/providers"
	"github.com/farshidmousavii/bidar/internal/snmp"
	"github.com/farshidmousavii/bidar/internal/store"
)

// snmpClient is the slice of internal/snmp.Client this provider uses.
// Production passes *snmp.Client; tests fake it.
type snmpClient interface {
	Get(ctx context.Context, oids ...string) ([]snmp.Varbind, error)
	WalkTable(ctx context.Context, baseOID string) ([]snmp.Varbind, error)
	WalkTableColumns(ctx context.Context, cols ...string) ([]snmp.TableRow, error)
	Close() error
}

type dialFunc func(ctx context.Context, cfg snmp.Config) (snmpClient, error)

func realDial(ctx context.Context, cfg snmp.Config) (snmpClient, error) {
	return snmp.NewClient(ctx, cfg)
}

// Provider implements providers.Provider for Cisco SNMP polling.
type Provider struct {
	store  *store.Store
	enc    *crypto.Encryptor
	logger *slog.Logger
	now    func() time.Time
	dial   dialFunc

	health providers.Health
}

// New returns an SNMP provider using real SNMP clients.
func New(st *store.Store, enc *crypto.Encryptor, logger *slog.Logger) (*Provider, error) {
	return newWithDial(st, enc, logger, time.Now, realDial)
}

// newWithDial is New with an injectable clock and client factory.
func newWithDial(st *store.Store, enc *crypto.Encryptor, logger *slog.Logger,
	now func() time.Time, dial dialFunc,
) (*Provider, error) {
	if st == nil || enc == nil {
		return nil, fmt.Errorf("snmp: store and encryptor are required")
	}
	if dial == nil {
		return nil, fmt.Errorf("snmp: dial is required")
	}
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{store: st, enc: enc, logger: logger, now: now, dial: dial}, nil
}

// Name implements providers.Provider.
func (p *Provider) Name() string { return "snmp" }

// Health implements providers.Provider.
func (p *Provider) Health() providers.Health { return p.health }

func (p *Provider) fail(err error) {
	p.health.Healthy = false
	p.health.LastError = err.Error()
}

// Run polls every enabled cisco_snmp device. Device-level failures are
// logged and isolated — one unreachable switch must not stop the others.
// Run errors only when nothing at all could be polled.
func (p *Provider) Run(ctx context.Context) (providers.Result, error) {
	p.health = providers.Health{LastRunAt: time.Now()}
	if err := ctx.Err(); err != nil {
		p.fail(err)
		return providers.Result{}, fmt.Errorf("snmp run cancelled: %w", err)
	}

	devices, err := p.store.ListEnabledDevicesByFamily(ctx, familyCisco)
	if err != nil {
		p.fail(err)
		return providers.Result{}, err
	}

	now := p.now().UTC()
	var total, failures int
	for _, dev := range devices {
		select {
		case <-ctx.Done():
			p.fail(ctx.Err())
			return providers.Result{}, fmt.Errorf("snmp run cancelled: %w", ctx.Err())
		default:
		}

		n, err := p.pollDevice(ctx, dev, now)
		if err != nil {
			failures++
			p.logger.Error("snmp: device poll failed", "device_id", dev.ID, "err", err)
			continue
		}
		total += n
	}

	if failures == len(devices) && len(devices) > 0 {
		err := fmt.Errorf("snmp: all %d devices failed to poll", len(devices))
		p.fail(err)
		return providers.Result{}, err
	}

	p.health = providers.Health{Healthy: true, LastRunAt: time.Now()}
	return providers.Result{ItemsFound: total}, nil
}

// pollDevice collects system info, interfaces, and VLANs from one device.
// Returns the number of items found (interfaces + VLANs).
func (p *Provider) pollDevice(ctx context.Context, dev domain.Device, now time.Time) (int, error) {
	if dev.SNMPProfileID == nil {
		p.logger.Warn("snmp: device has no snmp profile, skipping", "device_id", dev.ID)
		return 0, nil
	}
	profile, err := p.store.GetSNMPProfile(ctx, *dev.SNMPProfileID)
	if err != nil {
		return p.pollFailed(ctx, dev, now, fmt.Errorf("load snmp profile: %w", err))
	}
	cfg, err := p.clientConfig(profile, dev)
	if err != nil {
		return p.pollFailed(ctx, dev, now, err)
	}
	client, err := p.dial(ctx, cfg)
	if err != nil {
		return p.pollFailed(ctx, dev, now, fmt.Errorf("connect %s: %w", dev.MgmtIP, err))
	}
	defer client.Close()

	items := 0

	// System info: scalars, one GET.
	sys, err := client.Get(ctx, oidSysDescr, oidSysUpTime, oidSysName)
	if err != nil {
		return p.pollFailed(ctx, dev, now, fmt.Errorf("read system info: %w", err))
	}
	upTimeTicks, hasUpTime := sysUpTimeTicks(sys)
	if fw := parseFirmwareVersion(sysDescrText(sys)); fw != "" {
		// Best-effort enrichment; a storage hiccup must not fail the poll.
		if err := p.store.UpdateDeviceFirmwareVersion(ctx, dev.ID, fw); err != nil {
			p.logger.Warn("snmp: could not store firmware version", "device_id", dev.ID, "err", err)
		} else {
			p.logger.Info("snmp: system info",
				"device_id", dev.ID, "firmware", fw, "sys_name", sysNameText(sys))
		}
	}

	// Interfaces: IF-MIB columns joined by ifIndex.
	rows, err := client.WalkTableColumns(ctx, interfaceCols...)
	if err != nil {
		return p.pollFailed(ctx, dev, now, fmt.Errorf("walk interface table: %w", err))
	}
	ifaces, pvids := buildDeviceInterfaces(rows, upTimeTicks, hasUpTime, now)
	if _, err := p.store.UpsertDeviceInterfaces(ctx, dev.ID, ifaces, now); err != nil {
		return p.pollFailed(ctx, dev, now, fmt.Errorf("store interfaces: %w", err))
	}
	items += len(ifaces)

	// VLANs: Q-BRIDGE static names best-effort, unioned with port PVIDs
	// (many IOS builds only answer Q-BRIDGE under community@vlan indexing;
	// access-port PVIDs still reveal the VLANs in use).
	staticRows, err := client.WalkTable(ctx, oidVlanStaticName)
	if err != nil {
		p.logger.Warn("snmp: vlan static table unavailable; deriving from port PVIDs",
			"device_id", dev.ID, "err", err)
	}
	vlans := buildVLANs(staticRows, pvids)
	if len(vlans) > 0 {
		if err := p.store.UpsertDeviceVLANs(ctx, dev.ID, vlans, now); err != nil {
			return p.pollFailed(ctx, dev, now, fmt.Errorf("store vlans: %w", err))
		}
		items += len(vlans)
	}

	if err := p.store.UpdateDevicePollHealth(ctx, dev.ID, nil, now); err != nil {
		return items, fmt.Errorf("record poll health: %w", err)
	}
	return items, nil
}

// pollFailed records the failure on the device's circuit-breaker counters
// before returning it.
func (p *Provider) pollFailed(ctx context.Context, dev domain.Device, now time.Time, err error) (int, error) {
	msg := err.Error()
	if herr := p.store.UpdateDevicePollHealth(ctx, dev.ID, &msg, now); herr != nil {
		p.logger.Warn("snmp: could not record poll failure", "device_id", dev.ID, "err", herr)
	}
	return 0, err
}

// clientConfig decrypts a profile row into a wire config for the device.
// This package never logs decrypted secrets.
func (p *Provider) clientConfig(profile *domain.SNMPProfile, dev domain.Device) (snmp.Config, error) {
	pr := snmp.Profile{
		Version:        profile.Version,
		V3Username:     profile.V3Username,
		V3AuthProtocol: profile.V3AuthProtocol,
		V3PrivProtocol: profile.V3PrivProtocol,
		TimeoutMS:      profile.TimeoutMS,
		Retries:        profile.Retries,
	}
	switch strings.ToLower(strings.TrimSpace(profile.Version)) {
	case "v2c":
		community, err := p.enc.Decrypt(profile.CommunityEncrypted)
		if err != nil {
			return snmp.Config{}, fmt.Errorf("decrypt community: %w", err)
		}
		pr.Community = string(community)
	case "v3":
		if len(profile.V3AuthKeyEncrypted) > 0 {
			key, err := p.enc.Decrypt(profile.V3AuthKeyEncrypted)
			if err != nil {
				return snmp.Config{}, fmt.Errorf("decrypt v3 auth key: %w", err)
			}
			pr.V3AuthKey = string(key)
		}
		if len(profile.V3PrivKeyEncrypted) > 0 {
			key, err := p.enc.Decrypt(profile.V3PrivKeyEncrypted)
			if err != nil {
				return snmp.Config{}, fmt.Errorf("decrypt v3 priv key: %w", err)
			}
			pr.V3PrivKey = string(key)
		}
	default:
		return snmp.Config{}, fmt.Errorf("unsupported snmp profile version %q", profile.Version)
	}
	return snmp.ConfigFromProfile(dev.MgmtIP.String(), pr)
}
