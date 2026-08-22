package snmp

// MIB OIDs and varbind-to-domain translation for the Cisco SNMP provider.

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/farshidmousavii/bidar/internal/domain"
	"github.com/farshidmousavii/bidar/internal/snmp"
)

const familyCisco = "cisco_snmp"

// MIB OIDs read by the provider.
const (
	oidSysDescr  = "1.3.6.1.2.1.1.1.0"
	oidSysUpTime = "1.3.6.1.2.1.1.3.0"
	oidSysName   = "1.3.6.1.2.1.1.5.0"

	oidIfName       = "1.3.6.1.2.1.31.1.1.1.1"     // ifXTable.ifName
	oidIfDescr      = "1.3.6.1.2.1.2.2.1.2"        // ifTable.ifDescr
	oidIfPhysAddr   = "1.3.6.1.2.1.2.2.1.6"        // ifTable.ifPhysAddress
	oidIfAdmin      = "1.3.6.1.2.1.2.2.1.7"        // ifTable.ifAdminStatus
	oidIfOper       = "1.3.6.1.2.1.2.2.1.8"        // ifTable.ifOperStatus
	oidIfLastChange = "1.3.6.1.2.1.2.2.1.9"        // ifTable.ifLastChange (TimeTicks)
	oidDot1qPvid    = "1.3.6.1.2.1.17.7.1.4.5.1.1" // Q-BRIDGE dot1qPvid (ifIndex-indexed)

	oidVlanStaticName = "1.3.6.1.2.1.17.7.1.4.3.1.1" // Q-BRIDGE dot1qVlanStaticName (vlan-indexed)

	// MAC-table OIDs.
	oidDot1dTpFdbPort     = "1.3.6.1.2.1.17.4.3.1.2"     // BRIDGE-MIB dot1dTpFdbPort; suffix ends in the 6 MAC octets
	oidDot1dBasePortIfIdx = "1.3.6.1.2.1.17.1.4.1.2"     // dot1dBasePortIfIndex; suffix: bridge port; value: ifIndex
	oidDot1qTpFdbPort     = "1.3.6.1.2.1.17.7.1.2.2.1.1" // Q-BRIDGE dot1qTpFdbPort; suffix: vlan + 6 MAC octets
)

// interfaceCols is the column order buildDeviceInterfaces expects; pass
// it to WalkTableColumns verbatim.
var interfaceCols = []string{
	oidIfName, oidIfDescr, oidIfPhysAddr,
	oidIfAdmin, oidIfOper, oidIfLastChange, oidDot1qPvid,
}

const (
	colIfName = iota
	colIfDescr
	colIfPhysAddr
	colIfAdmin
	colIfOper
	colIfLastChange
	colPvid
	colCount
)

var firmwareRe = regexp.MustCompile(`(?i)\bversion\s+([^,\r\n]+)`)

// parseFirmwareVersion extracts the IOS version token from a Cisco-style
// sysDescr ("... Version 12.2(55)SE11, RELEASE SOFTWARE ..."). Returns ""
// when nothing matches — callers treat that as missing evidence.
func parseFirmwareVersion(sysDescr string) string {
	m := firmwareRe.FindStringSubmatch(sysDescr)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// statusString maps IF-MIB link-status integers to text. An absent value
// maps to "" (stored as NULL-adjacent empty text).
func statusString(v any) string {
	i, ok := v.(int)
	if !ok {
		return ""
	}
	switch i {
	case 1:
		return "up"
	case 2:
		return "down"
	case 3:
		return "testing"
	default:
		return "unknown"
	}
}

// octetStringPtr converts an OctetString value to a trimmed string, nil
// when absent or blank.
func octetStringPtr(v any) *string {
	b, ok := v.([]byte)
	if !ok {
		return nil
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil
	}
	return &s
}

// macValue converts a 6-octet ifPhysAddress to a HardwareAddr.
func macValue(v any) (net.HardwareAddr, bool) {
	b, ok := v.([]byte)
	if !ok || len(b) != 6 {
		return nil, false
	}
	mac := make(net.HardwareAddr, 6)
	copy(mac, b)
	return mac, true
}

// intValue accepts gosnmp's Integer (int) and TimeTicks/Gauge/Counter
// (uint32) decodings.
func intValue(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case uint32:
		return int64(n), true
	default:
		return 0, false
	}
}

func sysUpTimeTicks(sys []snmp.Varbind) (uint32, bool) {
	for _, vb := range sys {
		if vb.OID == oidSysUpTime {
			if n, ok := vb.Value.(uint32); ok {
				return n, true
			}
		}
	}
	return 0, false
}

func sysDescrText(sys []snmp.Varbind) string {
	for _, vb := range sys {
		if vb.OID == oidSysDescr {
			if b, ok := vb.Value.([]byte); ok {
				return string(b)
			}
		}
	}
	return ""
}

func sysNameText(sys []snmp.Varbind) string {
	for _, vb := range sys {
		if vb.OID == oidSysName {
			if s := octetStringPtr(vb.Value); s != nil {
				return *s
			}
		}
	}
	return ""
}

// buildDeviceInterfaces joins one WalkTableColumns result (columns in
// interfaceCols order) into domain rows. pvids collects every access-port
// PVID seen, feeding the VLAN union. ifLastChange is converted from
// boot-relative TimeTicks to absolute time using the sysUpTime anchor
// read in the same poll; anything inconsistent stays NULL.
func buildDeviceInterfaces(rows []snmp.TableRow, sysUpTime uint32, hasUpTime bool, now time.Time) ([]domain.DeviceInterface, map[int32]bool) {
	out := make([]domain.DeviceInterface, 0, len(rows))
	pvids := make(map[int32]bool)

	for _, r := range rows {
		if len(r.Index) == 0 {
			continue
		}
		iface := domain.DeviceInterface{IfIndex: r.Index[0]}

		val := func(col int) any {
			if col >= len(r.Values) {
				return nil
			}
			return r.Values[col]
		}

		iface.IfName = octetStringPtr(val(colIfName))
		iface.IfDesc = octetStringPtr(val(colIfDescr))
		if m, ok := macValue(val(colIfPhysAddr)); ok {
			iface.MAC = &m
		}
		iface.AdminStatus = statusString(val(colIfAdmin))
		iface.OperStatus = statusString(val(colIfOper))

		if lcRaw := val(colIfLastChange); lcRaw != nil {
			if lc, ok := lcRaw.(uint32); ok && hasUpTime && lc <= sysUpTime {
				t := now.Add(-time.Duration(sysUpTime-lc) * 10 * time.Millisecond)
				iface.LastChangeAt = &t
			}
		}

		if pvRaw := val(colPvid); pvRaw != nil {
			if n, ok := intValue(pvRaw); ok && n > 0 && n <= 4094 {
				p := int32(n)
				iface.PVID = &p
				pvids[p] = true
			}
		}

		out = append(out, iface)
	}
	return out, pvids
}

// buildVLANs merges Q-BRIDGE static VLAN names with VLANs only evidenced
// by access-port PVIDs, sorted by number.
func buildVLANs(staticRows []snmp.Varbind, pvids map[int32]bool) []domain.DeviceVLAN {
	seen := make(map[int32]bool)
	var out []domain.DeviceVLAN

	for _, vb := range staticRows {
		if len(vb.Suffix) == 0 {
			continue
		}
		vlan := int32(vb.Suffix[len(vb.Suffix)-1])
		if seen[vlan] || vlan == 0 {
			continue
		}
		out = append(out, domain.DeviceVLAN{VlanNumber: vlan, Name: octetStringPtr(vb.Value)})
		seen[vlan] = true
	}
	for p := range pvids {
		if seen[p] {
			continue
		}
		out = append(out, domain.DeviceVLAN{VlanNumber: p})
		seen[p] = true
	}

	sort.Slice(out, func(i, j int) bool { return out[i].VlanNumber < out[j].VlanNumber })
	return out
}

// macFromSuffix converts the last six OID suffix components into a MAC.
func macFromSuffix(sfx []int) (net.HardwareAddr, bool) {
	if len(sfx) < 6 {
		return nil, false
	}
	tail := sfx[len(sfx)-6:]
	mac := make(net.HardwareAddr, 6)
	for i, n := range tail {
		if n < 0 || n > 255 {
			return nil, false
		}
		mac[i] = byte(n)
	}
	return mac, true
}

// bridgePortMap reads dot1dBasePortIfIndex and maps bridge port number
// -> interface id (via the if_index->id map from this poll's interface
// sync). Bridge ports are not ifIndexes; BRIDGE-MIB speaks in its own
// port numbering.
func bridgePortMap(ctx context.Context, c snmpClient, ifaceIDs map[int]int64) (map[int]int64, error) {
	rows, err := c.WalkTable(ctx, oidDot1dBasePortIfIdx)
	if err != nil {
		return nil, fmt.Errorf("walk dot1dBasePortIfIndex: %w", err)
	}
	m := make(map[int]int64, len(rows))
	for _, r := range rows {
		if len(r.Suffix) != 1 {
			continue
		}
		ifIdx, ok := intValue(r.Value)
		if !ok {
			continue
		}
		if id, ok := ifaceIDs[int(ifIdx)]; ok {
			m[r.Suffix[0]] = id
		}
	}
	return m, nil
}

// collectBridgeVlan reads one VLAN's forwarding table from a client
// already bound to that VLAN's context (community@vlan indexing).
func collectBridgeVlan(ctx context.Context, c snmpClient, vlan int32, ifaceIDs map[int]int64) ([]domain.MACTableEntry, error) {
	ports, err := c.WalkTable(ctx, oidDot1dTpFdbPort)
	if err != nil {
		return nil, fmt.Errorf("walk dot1dTpFdbPort: %w", err)
	}
	bpToIf, err := bridgePortMap(ctx, c, ifaceIDs)
	if err != nil {
		return nil, err
	}

	out := make([]domain.MACTableEntry, 0, len(ports))
	for _, r := range ports {
		mac, ok := macFromSuffix(r.Suffix)
		if !ok {
			continue
		}
		e := domain.MACTableEntry{VLANNumber: &vlan, MAC: mac}
		if bp, ok := intValue(r.Value); ok {
			if id, ok := bpToIf[int(bp)]; ok {
				e.InterfaceID = &id
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// collectQbridge reads the whole forwarding database in one walk on
// platforms that answer Q-BRIDGE without per-VLAN contexts.
func collectQbridge(ctx context.Context, c snmpClient, ifaceIDs map[int]int64) ([]domain.MACTableEntry, error) {
	rows, err := c.WalkTable(ctx, oidDot1qTpFdbPort)
	if err != nil {
		return nil, fmt.Errorf("walk dot1qTpFdbPort: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	bpToIf, err := bridgePortMap(ctx, c, ifaceIDs)
	if err != nil {
		return nil, err
	}

	out := make([]domain.MACTableEntry, 0, len(rows))
	for _, r := range rows {
		if len(r.Suffix) < 7 { // vlan + 6 MAC octets
			continue
		}
		vlan := int32(r.Suffix[0])
		mac, ok := macFromSuffix(r.Suffix[1:])
		if !ok {
			continue
		}
		e := domain.MACTableEntry{VLANNumber: &vlan, MAC: mac}
		if bp, ok := intValue(r.Value); ok {
			if id, ok := bpToIf[int(bp)]; ok {
				e.InterfaceID = &id
			}
		}
		out = append(out, e)
	}
	return out, nil
}
