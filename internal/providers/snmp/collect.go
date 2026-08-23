package snmp

// MIB OIDs and varbind-to-domain translation for the Cisco SNMP provider.

import (
	"context"
	"fmt"
	"net"
	"net/netip"
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

// InterfaceCols is the column order BuildDeviceInterfaces expects; pass
// it to WalkTableColumns verbatim. Exported for sibling providers
// (MikroTik SNMP stats) polling the same IF-MIB shape.
var InterfaceCols = []string{
	oidIfName, oidIfDescr, oidIfPhysAddr,
	oidIfAdmin, oidIfOper, oidIfLastChange, oidDot1qPvid,
}

const (
	ColIfName = iota
	ColIfDescr
	ColIfPhysAddr
	ColIfAdmin
	ColIfOper
	ColIfLastChange
	ColPvid
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
// when absent or blank. Real-world agents put raw binary in "string"
// fields (CDP device ids especially): trailing NULs are stripped and
// invalid UTF-8 replaced rather than passed through — Postgres would
// reject the row outright.
func octetStringPtr(v any) *string {
	b, ok := v.([]byte)
	if !ok {
		return nil
	}
	s := strings.TrimRight(string(b), "\x00")
	s = strings.ToValidUTF8(s, "\uFFFD")
	s = strings.TrimSpace(s)
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

func SysUpTimeTicks(sys []snmp.Varbind) (uint32, bool) {
	for _, vb := range sys {
		if vb.OID == oidSysUpTime {
			if n, ok := vb.Value.(uint32); ok {
				return n, true
			}
		}
	}
	return 0, false
}

func SysDescrText(sys []snmp.Varbind) string {
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

// BuildDeviceInterfaces joins one WalkTableColumns result (columns in
// InterfaceCols order) into domain rows. pvids collects every access-port
// PVID seen, feeding the VLAN union. ifLastChange is converted from
// boot-relative TimeTicks to absolute time using the sysUpTime anchor
// read in the same poll; anything inconsistent stays NULL.
func BuildDeviceInterfaces(rows []snmp.TableRow, sysUpTime uint32, hasUpTime bool, now time.Time) ([]domain.DeviceInterface, map[int32]bool) {
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
		_ = colCount

		iface.IfName = octetStringPtr(val(ColIfName))
		iface.IfDesc = octetStringPtr(val(ColIfDescr))
		if m, ok := macValue(val(ColIfPhysAddr)); ok {
			iface.MAC = &m
		}
		iface.AdminStatus = statusString(val(ColIfAdmin))
		iface.OperStatus = statusString(val(ColIfOper))

		if lcRaw := val(ColIfLastChange); lcRaw != nil {
			if lc, ok := lcRaw.(uint32); ok && hasUpTime && lc <= sysUpTime {
				t := now.Add(-time.Duration(sysUpTime-lc) * 10 * time.Millisecond)
				iface.LastChangeAt = &t
			}
		}

		if pvRaw := val(ColPvid); pvRaw != nil {
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

// Neighbor-discovery OIDs.
const (
	oidLldpRemChassisSubtype = "1.0.8802.1.1.2.1.4.1.1.3" // lldpRemChassisIdSubtype
	oidLldpRemChassisId      = "1.0.8802.1.1.2.1.4.1.1.4" // lldpRemChassisId
	oidLldpRemPortId         = "1.0.8802.1.1.2.1.4.1.1.6" // lldpRemPortId
	oidLldpRemSysName        = "1.0.8802.1.1.2.1.4.1.1.7" // lldpRemSysName

	oidCdpCacheDeviceId   = "1.3.6.1.4.1.9.9.23.1.2.1.1.3" // cdpCacheDeviceId
	oidCdpCacheDevicePort = "1.3.6.1.4.1.9.9.23.1.2.1.1.4" // cdpCacheDevicePort
	oidCdpCacheAddress    = "1.3.6.1.4.1.9.9.23.1.2.1.1.6" // cdpCacheAddress
)

// Column orders buildLLDP / buildCDP expect.
var (
	lldpCols = []string{oidLldpRemChassisSubtype, oidLldpRemChassisId, oidLldpRemPortId, oidLldpRemSysName}
	cdpCols  = []string{oidCdpCacheDeviceId, oidCdpCacheDevicePort, oidCdpCacheAddress}
)

const (
	lldpColChassisSubtype = iota
	lldpColChassisId
	lldpColPortID
	lldpColSysName
)

const (
	cdpColDeviceID = iota
	cdpColDevicePort
	cdpColAddress
)

func colVal(r snmp.TableRow, col int) any {
	if col >= len(r.Values) {
		return nil
	}
	return r.Values[col]
}

// collectLLDP reads the LLDP remote-neighbors table. The remote
// management address is not a column of its own: when the chassis ID
// subtype is networkAddress(5), the chassis ID octets carry it as
// [address-family, address] — extracted here; otherwise it stays NULL.
func collectLLDP(ctx context.Context, c snmpClient, ifaceIDs map[int]int64) ([]domain.Neighbor, error) {
	rows, err := c.WalkTableColumns(ctx, lldpCols...)
	if err != nil {
		return nil, fmt.Errorf("walk lldpRemTable: %w", err)
	}

	out := make([]domain.Neighbor, 0, len(rows))
	for _, r := range rows {
		// INDEX { timeMark, localPortNum, destMac(6) }.
		if len(r.Index) < 8 {
			continue
		}
		localPort := r.Index[1]

		n := domain.Neighbor{Protocol: "lldp"}
		if id, ok := ifaceIDs[localPort]; ok {
			n.LocalInterfaceID = &id
		}
		if s := octetStringPtr(colVal(r, lldpColPortID)); s != nil {
			n.RemotePortID = s
		}
		if s := octetStringPtr(colVal(r, lldpColSysName)); s != nil {
			n.RemoteSystemName = s
		}
		if st, ok := intValue(colVal(r, lldpColChassisSubtype)); ok && st == 5 {
			if b, ok := colVal(r, lldpColChassisId).([]byte); ok {
				n.RemoteMgmtIP = parseIANAAddress(b)
			}
		}
		out = append(out, n)
	}
	return out, nil
}

// parseIANAAddress decodes an IANA AddressFamilyNumbers-prefixed address
// (LLDP networkAddress form): [family, address...]. Family 1 = IPv4,
// 2 = IPv6.
func parseIANAAddress(b []byte) *netip.Addr {
	if len(b) < 5 {
		return nil
	}
	var lo, hi int
	switch b[0] {
	case 1:
		lo, hi = 1, 5
	case 2:
		if len(b) < 17 {
			return nil
		}
		lo, hi = 1, 17
	default:
		return nil
	}
	ip, ok := netip.AddrFromSlice(b[lo:hi])
	if !ok {
		return nil
	}
	return &ip
}

// collectCDP reads Cisco's CDP cache. cdpCacheAddress uses the NABBPE
// layout ([protocol-type, protocol-len, address-len(2), address]); the
// first entry is extracted when it is IPv4 (type 0xCC).
func collectCDP(ctx context.Context, c snmpClient, ifaceIDs map[int]int64) ([]domain.Neighbor, error) {
	rows, err := c.WalkTableColumns(ctx, cdpCols...)
	if err != nil {
		return nil, fmt.Errorf("walk cdpCacheTable: %w", err)
	}

	out := make([]domain.Neighbor, 0, len(rows))
	for _, r := range rows {
		// INDEX { cdpCacheIfIndex, cdpCacheDeviceIndex }.
		if len(r.Index) < 2 {
			continue
		}
		localIf := r.Index[0]

		n := domain.Neighbor{Protocol: "cdp"}
		if id, ok := ifaceIDs[localIf]; ok {
			n.LocalInterfaceID = &id
		}
		if s := octetStringPtr(colVal(r, cdpColDeviceID)); s != nil {
			n.RemoteSystemName = s
		}
		if s := octetStringPtr(colVal(r, cdpColDevicePort)); s != nil {
			n.RemotePortID = s
		}
		if b, ok := colVal(r, cdpColAddress).([]byte); ok {
			n.RemoteMgmtIP = parseCDPAddress(b)
		}
		out = append(out, n)
	}
	return out, nil
}

// parseCDPAddress extracts the first IPv4 entry from cdpCacheAddress's
// NABBPE octets: [0xCC, protoLen, addrLen(2 big-endian), addr...].
func parseCDPAddress(b []byte) *netip.Addr {
	if len(b) < 8 || b[0] != 0xCC {
		return nil
	}
	addrLen := int(b[2])<<8 | int(b[3])
	if addrLen != 4 || len(b) < 4+addrLen {
		return nil
	}
	ip, ok := netip.AddrFromSlice(b[4 : 4+addrLen])
	if !ok {
		return nil
	}
	return &ip
}
