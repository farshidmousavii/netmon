// Phase 2 device-polling provider: for every enabled mikrotik_routeros
// row it reads the ARP table and the wireless registration table over
// the RouterOS API (credentials from network_devices.routeros_*,
// decrypted at use), writes mikrotik_leases snapshots per source, and
// reconciles every entry into hosts/host_observations as presence
// evidence (source='mikrotik'). Best-effort SNMP adds system info and
// IF-MIB interfaces via the same client code the Cisco provider uses.
//
// Role plays no part here — it is an ARP-collector-only concept. The
// DHCP-lease collector (providers/dhcp) keeps its own source-centric
// path; this provider is device-centric.
package mikrotik

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/domain"
	"github.com/farshidmousavii/bidar/internal/providers"
	"github.com/farshidmousavii/bidar/internal/providers/reconcile"
	snmppoll "github.com/farshidmousavii/bidar/internal/providers/snmp"
	snmplib "github.com/farshidmousavii/bidar/internal/snmp"
	"github.com/farshidmousavii/bidar/internal/store"
)

// System-info scalars (same OIDs every SNMP agent serves).
const (
	oidSysDescr  = "1.3.6.1.2.1.1.1.0"
	oidSysUpTime = "1.3.6.1.2.1.1.3.0"
	oidSysName   = "1.3.6.1.2.1.1.5.0"
)

// rosClient is the slice of *Client this provider uses; tests fake it.
// RunCommand is included for the RouterOS 7 wifi registration fallback.
type rosClient interface {
	ARPs(ctx context.Context) ([]ARP, error)
	WirelessRegistrations(ctx context.Context) ([]WirelessRegistration, error)
	RunCommand(ctx context.Context, command string, params map[string]string) ([]map[string]string, error)
	Close() error
}

type dialFunc func(ctx context.Context, host, username, password string) (rosClient, error)

func realDial(ctx context.Context, host, username, password string) (rosClient, error) {
	return Dial(ctx, Config{Host: host, Username: username, Password: password})
}

// statsClient is the slice of snmp.Client used for best-effort stats.
type statsClient interface {
	Get(ctx context.Context, oids ...string) ([]snmplib.Varbind, error)
	WalkTableColumns(ctx context.Context, cols ...string) ([]snmplib.TableRow, error)
	Close() error
}

type statsDialFunc func(ctx context.Context, cfg snmplib.Config) (statsClient, error)

func realStatsDial(ctx context.Context, cfg snmplib.Config) (statsClient, error) {
	return snmplib.NewClient(ctx, cfg)
}

// Provider implements providers.Provider for MikroTik device polling.
type Provider struct {
	store    *store.Store
	enc      *crypto.Encryptor
	logger   *slog.Logger
	now      func() time.Time
	dial     dialFunc
	statsDia statsDialFunc

	health providers.Health
}

// New returns a MikroTik polling provider using real connections.
func New(st *store.Store, enc *crypto.Encryptor, logger *slog.Logger) (*Provider, error) {
	return newWithDial(st, enc, logger, time.Now, realDial, realStatsDial)
}

// newWithDial is New with injectable connection factories for tests.
func newWithDial(st *store.Store, enc *crypto.Encryptor, logger *slog.Logger,
	now func() time.Time, dial dialFunc, statsDial statsDialFunc,
) (*Provider, error) {
	if st == nil || enc == nil {
		return nil, fmt.Errorf("mikrotik: store and encryptor are required")
	}
	if dial == nil || statsDial == nil {
		return nil, fmt.Errorf("mikrotik: dial functions are required")
	}
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{store: st, enc: enc, logger: logger, now: now, dial: dial, statsDia: statsDial}, nil
}

// Name implements providers.Provider.
func (p *Provider) Name() string { return "mikrotik" }

// Health implements providers.Provider.
func (p *Provider) Health() providers.Health { return p.health }

func (p *Provider) fail(err error) {
	p.health.Healthy = false
	p.health.LastError = err.Error()
}

// Run polls every enabled mikrotik_routeros device. Device-level failures
// are logged and isolated; Run errors only when nothing could be polled.
func (p *Provider) Run(ctx context.Context) (providers.Result, error) {
	p.health = providers.Health{LastRunAt: time.Now()}
	if err := ctx.Err(); err != nil {
		p.fail(err)
		return providers.Result{}, fmt.Errorf("mikrotik run cancelled: %w", err)
	}

	devices, err := p.store.ListEnabledDevicesByFamily(ctx, familyMikrotik)
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
			return providers.Result{}, fmt.Errorf("mikrotik run cancelled: %w", ctx.Err())
		default:
		}

		n, err := p.pollDevice(ctx, dev, now)
		if err != nil {
			failures++
			p.logger.Error("mikrotik: device poll failed", "device_id", dev.ID, "err", err)
			continue
		}
		total += n
	}

	if failures == len(devices) && len(devices) > 0 {
		err := fmt.Errorf("mikrotik: all %d devices failed to poll", len(devices))
		p.fail(err)
		return providers.Result{}, err
	}

	p.health = providers.Health{Healthy: true, LastRunAt: time.Now()}
	return providers.Result{ItemsFound: total}, nil
}

const familyMikrotik = "mikrotik_routeros"

// pollDevice collects one device's ARP + wireless evidence and
// best-effort SNMP stats.
func (p *Provider) pollDevice(ctx context.Context, dev domain.Device, now time.Time) (int, error) {
	if dev.RouterOSUsername == "" || len(dev.RouterOSPasswordEnc) == 0 {
		return p.pollFailed(ctx, dev, now, fmt.Errorf(
			"device has no RouterOS API credentials (set with bidar devices set-routeros-auth)"))
	}
	password, err := p.enc.Decrypt(dev.RouterOSPasswordEnc)
	if err != nil {
		return p.pollFailed(ctx, dev, now, fmt.Errorf("decrypt routeros password: %w", err))
	}
	c, err := p.dial(ctx, dev.MgmtIP.String(), dev.RouterOSUsername, string(password))
	if err != nil {
		return p.pollFailed(ctx, dev, now, fmt.Errorf("connect %s: %w", dev.MgmtIP, err))
	}
	defer c.Close()

	items := 0

	// ARP table: core presence evidence — failure fails the poll.
	arps, err := c.ARPs(ctx)
	if err != nil {
		return p.pollFailed(ctx, dev, now, fmt.Errorf("read arp table: %w", err))
	}
	rows := make([]domain.MikrotikEvidence, 0, len(arps))
	for _, a := range arps {
		addr := a.Address
		mac := a.MAC
		iface := a.Interface
		rows = append(rows, domain.MikrotikEvidence{
			MAC: mac, IP: &addr, Interface: &iface,
		})
	}
	if err := p.store.ReplaceMikrotikEvidence(ctx, dev.ID, "arp", rows, now); err != nil {
		return p.pollFailed(ctx, dev, now, fmt.Errorf("store arp evidence: %w", err))
	}
	for _, r := range rows {
		if err := p.recordPresence(ctx, r, "arp", now); err != nil {
			return items, fmt.Errorf("reconcile arp entry: %w", err)
		}
	}
	items += len(rows)

	// Wireless registrations: legacy table first, RouterOS 7 wifi path as
	// fallback. Best-effort like LLDP/CDP — devices without wireless must
	// not fail the poll, and a failed read never wipes the last snapshot.
	regs, legacyErr := c.WirelessRegistrations(ctx)
	if legacyErr != nil {
		p.logger.Debug("mikrotik: legacy wireless table unavailable, probing ROS7 wifi",
			"device_id", dev.ID, "err", legacyErr)
		regs, err = p.wifiRegistrations(ctx, c)
		if err != nil {
			p.logger.Warn("snmp: wireless evidence unavailable", "device_id", dev.ID, "err", err)
			regs = nil
		}
	}
	if legacyErr == nil || regs != nil {
		wrows := make([]domain.MikrotikEvidence, 0, len(regs))
		for _, w := range regs {
			mac := w.MAC
			e := domain.MikrotikEvidence{MAC: mac, Interface: &w.Interface}
			if w.LastIP.IsValid() {
				ip := w.LastIP
				e.IP = &ip
			}
			wrows = append(wrows, e)
		}
		if err := p.store.ReplaceMikrotikEvidence(ctx, dev.ID, "wireless_reg", wrows, now); err != nil {
			return p.pollFailed(ctx, dev, now, fmt.Errorf("store wireless evidence: %w", err))
		}
		for _, r := range wrows {
			if err := p.recordPresence(ctx, r, "wireless_reg", now); err != nil {
				return items, fmt.Errorf("reconcile wireless entry: %w", err)
			}
		}
		items += len(wrows)
	}

	// SNMP system info + interfaces: best-effort enrichment.
	snmpItems, err := p.pollSNMPStats(ctx, dev, now)
	if err != nil {
		p.logger.Warn("snmp: mikrotik stats unavailable", "device_id", dev.ID, "err", err)
	} else {
		items += snmpItems
	}

	if err := p.store.UpdateDevicePollHealth(ctx, dev.ID, nil, now); err != nil {
		return items, fmt.Errorf("record poll health: %w", err)
	}
	return items, nil
}

// wifiRegistrations probes the RouterOS 7 /interface/wifi path when the
// legacy wireless registration table is unavailable.
func (p *Provider) wifiRegistrations(ctx context.Context, c rosClient) ([]WirelessRegistration, error) {
	rows, err := c.RunCommand(ctx, "/interface/wifi/registration-table/print", map[string]string{
		".proplist": ".id,interface,mac-address,last-ip",
	})
	if err != nil {
		return nil, fmt.Errorf("wifi registration table: %w", err)
	}
	out := make([]WirelessRegistration, 0, len(rows))
	for _, row := range rows {
		w := WirelessRegistration{ID: row["id"], Interface: row["interface"]}
		mac, err := net.ParseMAC(row["mac-address"])
		if err != nil {
			continue
		}
		w.MAC = mac
		if ip, err := netip.ParseAddr(row["last-ip"]); err == nil {
			w.LastIP = ip
		}
		out = append(out, w)
	}
	return out, nil
}

// recordPresence reconciles one evidence row into hosts +
// host_observations (source='mikrotik'). RouterOS ARP/wifi rows carry no
// hostname, so matching runs on IP then MAC.
func (p *Provider) recordPresence(ctx context.Context, e domain.MikrotikEvidence, table string, now time.Time) error {
	hostID, err := reconcile.Host(ctx, p.store, nil, e.IP, &e.MAC, nil, nil, now)
	if err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]any{"table": table, "interface": e.Interface})
	if err != nil {
		detail = []byte("{}")
	}
	return reconcile.Observation(ctx, p.store, hostID, "mikrotik", nil, e.IP, &e.MAC, nil, detail, now)
}

var routerosVersionRe = regexp.MustCompile(`(?i)routeros\s+v?([0-9]+(?:\.[0-9]+)+)`)

// pollSNMPStats reads system info and IF-MIB interfaces over SNMP using
// the device's snmp_profile — the part of the picture the RouterOS API
// does not expose. Every failure here is a warning, never a poll failure.
func (p *Provider) pollSNMPStats(ctx context.Context, dev domain.Device, now time.Time) (int, error) {
	if dev.SNMPProfileID == nil {
		return 0, nil
	}
	profile, err := p.store.GetSNMPProfile(ctx, *dev.SNMPProfileID)
	if err != nil {
		return 0, fmt.Errorf("load snmp profile: %w", err)
	}
	pr := snmplib.Profile{
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
			return 0, fmt.Errorf("decrypt community: %w", err)
		}
		pr.Community = string(community)
	case "v3":
		if len(profile.V3AuthKeyEncrypted) > 0 {
			key, err := p.enc.Decrypt(profile.V3AuthKeyEncrypted)
			if err != nil {
				return 0, fmt.Errorf("decrypt v3 auth key: %w", err)
			}
			pr.V3AuthKey = string(key)
		}
		if len(profile.V3PrivKeyEncrypted) > 0 {
			key, err := p.enc.Decrypt(profile.V3PrivKeyEncrypted)
			if err != nil {
				return 0, fmt.Errorf("decrypt v3 priv key: %w", err)
			}
			pr.V3PrivKey = string(key)
		}
	default:
		return 0, fmt.Errorf("unsupported snmp profile version %q", profile.Version)
	}
	cfg, err := snmplib.ConfigFromProfile(dev.MgmtIP.String(), pr)
	if err != nil {
		return 0, err
	}
	c, err := p.statsDia(ctx, cfg)
	if err != nil {
		return 0, fmt.Errorf("connect snmp: %w", err)
	}
	defer c.Close()

	sys, err := c.Get(ctx, oidSysDescr, oidSysUpTime, oidSysName)
	if err != nil {
		return 0, fmt.Errorf("read system info: %w", err)
	}
	items := 0
	if fw := parseRouterOSVersion(snmppoll.SysDescrText(sys)); fw != "" {
		if err := p.store.UpdateDeviceFirmwareVersion(ctx, dev.ID, fw); err == nil {
			items++
		}
	}

	rows, err := c.WalkTableColumns(ctx, snmppoll.InterfaceCols...)
	if err != nil {
		return items, fmt.Errorf("walk interface table: %w", err)
	}
	upTicks, hasUp := snmppoll.SysUpTimeTicks(sys)
	ifaces, _ := snmppoll.BuildDeviceInterfaces(rows, upTicks, hasUp, now)
	if _, err := p.store.UpsertDeviceInterfaces(ctx, dev.ID, ifaces, now); err != nil {
		return items, fmt.Errorf("store interfaces: %w", err)
	}
	return items + len(ifaces), nil
}

func parseRouterOSVersion(sysDescr string) string {
	m := routerosVersionRe.FindStringSubmatch(sysDescr)
	if m == nil {
		return ""
	}
	return m[1]
}

// pollFailed records the failure on the device's circuit-breaker counters.
func (p *Provider) pollFailed(ctx context.Context, dev domain.Device, now time.Time, err error) (int, error) {
	msg := err.Error()
	if herr := p.store.UpdateDevicePollHealth(ctx, dev.ID, &msg, now); herr != nil {
		p.logger.Warn("mikrotik: could not record poll failure", "device_id", dev.ID, "err", herr)
	}
	return 0, err
}
