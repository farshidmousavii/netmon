package snmp

// Test-only fixture: a minimal SNMPv2c agent speaking just enough BER to
// serve the SnmpWalk / WalkTable tests over localhost UDP. No live network
// access is required by `go test ./...`.
//
// gosnmp v1.43.2 ships no agent-side implementation, so this fake encodes
// and decodes the handful of packet shapes the client uses:
//   - GET (0xA0) and GETNEXT (0xA1) requests
//   - GETBULK (0xA5) requests, with proper nonRepeaters/maxRepetitions
//     semantics
//   - GetResponse (0xA2) packets containing Integer, OctetString,
//     ObjectIdentifier, TimeTicks/Counter/Gauge (uint32), IPAddress and
//     EndOfMibView values

import (
	"encoding/binary"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

// fixtureEntry is one value the fake agent serves, keyed by instance OID
// (dotless dotted-decimal form).
type fixtureEntry struct {
	oid   string
	typ   gosnmp.Asn1BER
	value any
}

// fakeAgent is a single-threaded UDP SNMPv2c responder. It is not safe for
// concurrent use beyond its single serve goroutine.
type fakeAgent struct {
	t       *testing.T
	entries []fixtureEntry // sorted by OID (component-wise)
	delay   time.Duration  // optional per-request response delay
	conn    net.PacketConn
}

func newFakeAgent(t *testing.T, entries []fixtureEntry) *fakeAgent {
	t.Helper()
	a := &fakeAgent{t: t, entries: entries}
	sort.Slice(a.entries, func(i, j int) bool {
		return cmpOID(a.entries[i].oid, a.entries[j].oid) < 0
	})
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake agent listen: %v", err)
	}
	a.conn = conn
	go a.serve()
	t.Cleanup(func() { conn.Close() })
	return a
}

func (a *fakeAgent) port() int {
	return a.conn.LocalAddr().(*net.UDPAddr).Port
}

func (a *fakeAgent) serve() {
	buf := make([]byte, 65535)
	for {
		n, addr, err := a.conn.ReadFrom(buf)
		if err != nil {
			return // listener closed
		}
		if a.delay > 0 {
			time.Sleep(a.delay)
		}
		resp := a.handle(buf[:n])
		if resp != nil {
			if _, err := a.conn.WriteTo(resp, addr); err != nil {
				return
			}
		}
	}
}

type parsedRequest struct {
	requestID      int
	community      string
	pduType        byte
	nonRepeaters   int
	maxRepetitions int
	oids           []string
}

func (a *fakeAgent) handle(req []byte) []byte {
	pr, err := parseRequest(req)
	if err != nil {
		a.t.Errorf("fake agent: parse request: %v", err)
		return nil
	}

	var varbinds []berVarbind
	switch pr.pduType {
	case 0xA0: // GetRequest
		for _, oid := range pr.oids {
			if e, ok := a.lookup(oid); ok {
				varbinds = append(varbinds, berVarbind{oid: e.oid, typ: e.typ, value: e.value})
			} else {
				varbinds = append(varbinds, berVarbind{oid: oid, typ: gosnmp.EndOfMibView, value: nil})
			}
		}
	case 0xA1: // GetNextRequest
		for _, oid := range pr.oids {
			varbinds = append(varbinds, a.nextVarbind(oid))
		}
	case 0xA5: // GetBulkRequest
		for i, oid := range pr.oids {
			if i < pr.nonRepeaters {
				varbinds = append(varbinds, a.nextVarbind(oid))
				continue
			}
			for j := 0; j < pr.maxRepetitions; j++ {
				vb := a.nextVarbind(oid)
				varbinds = append(varbinds, vb)
				if vb.typ == gosnmp.EndOfMibView {
					break
				}
				oid = vb.oid // continue from the last returned OID
			}
		}
	default:
		a.t.Errorf("fake agent: unsupported request PDU 0x%02x", pr.pduType)
		return nil
	}

	return marshalGetResponse(pr.requestID, pr.community, varbinds)
}

func (a *fakeAgent) lookup(oid string) (fixtureEntry, bool) {
	for _, e := range a.entries {
		if e.oid == oid {
			return e, true
		}
	}
	return fixtureEntry{}, false
}

// nextVarbind returns the entry strictly after oid, or an EndOfMibView
// varbind if there is none.
func (a *fakeAgent) nextVarbind(oid string) berVarbind {
	for _, e := range a.entries {
		if cmpOID(e.oid, oid) > 0 {
			return berVarbind{oid: e.oid, typ: e.typ, value: e.value}
		}
	}
	return berVarbind{oid: oid, typ: gosnmp.EndOfMibView, value: nil}
}

// cmpOID compares two dotless dotted-decimal OIDs component-wise, the way
// SNMP lexicographic ordering actually works ("1.3.6.1.4.22.1.2.10..." >
// "1.3.6.1.4.22.1.2.9...", unlike string comparison).
func cmpOID(a, b string) int {
	ac := strings.Split(a, ".")
	bc := strings.Split(b, ".")
	for i := 0; i < len(ac) && i < len(bc); i++ {
		ai, errA := strconv.Atoi(ac[i])
		bi, errB := strconv.Atoi(bc[i])
		if errA != nil || errB != nil {
			return strings.Compare(a, b)
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return len(ac) - len(bc)
}

// -- BER parsing (requests) --------------------------------------------------

func parseRequest(data []byte) (*parsedRequest, error) {
	tag, value, _, err := readTLV(data, 0)
	if err != nil {
		return nil, err
	}
	if tag != 0x30 {
		return nil, fmt.Errorf("expected SEQUENCE, got 0x%02x", tag)
	}
	data = value

	// version INTEGER
	_, version, _, err := readTLV(data, 0)
	if err != nil {
		return nil, err
	}
	if len(version) > 0 && version[0] != 1 {
		return nil, fmt.Errorf("unsupported SNMP version %d", version[0])
	}
	off := 0
	_, _, off, err = readTLV(data, off)
	if err != nil {
		return nil, err
	}

	// community OCTET STRING
	_, community, off, err := readTLV(data, off)
	if err != nil {
		return nil, err
	}

	// PDU
	pduTag, pdu, off, err := readTLV(data, off)
	if err != nil {
		return nil, err
	}
	if pduTag != 0xA0 && pduTag != 0xA1 && pduTag != 0xA5 {
		return nil, fmt.Errorf("unexpected PDU tag 0x%02x", pduTag)
	}

	pr := &parsedRequest{pduType: pduTag, community: string(community)}

	// requestID INTEGER
	_, rawID, poff, err := readTLV(pdu, 0)
	if err != nil {
		return nil, err
	}
	pr.requestID, err = decodeInt(rawID)
	if err != nil {
		return nil, err
	}

	if pduTag == 0xA5 {
		// nonRepeaters, maxRepetitions
		var raw []byte
		_, raw, poff, err = readTLV(pdu, poff)
		if err != nil {
			return nil, err
		}
		pr.nonRepeaters, err = decodeInt(raw)
		if err != nil {
			return nil, err
		}
		_, raw, poff, err = readTLV(pdu, poff)
		if err != nil {
			return nil, err
		}
		pr.maxRepetitions, err = decodeInt(raw)
		if err != nil {
			return nil, err
		}
	} else {
		// errorStatus, errorIndex
		var raw []byte
		_, raw, poff, err = readTLV(pdu, poff)
		if err != nil {
			return nil, err
		}
		_, raw, poff, err = readTLV(pdu, poff)
		if err != nil {
			return nil, err
		}
		_ = raw
	}

	// varbind list SEQUENCE
	vbTag, vbl, _, err := readTLV(pdu, poff)
	if err != nil {
		return nil, err
	}
	if vbTag != 0x30 {
		return nil, fmt.Errorf("expected varbind list SEQUENCE, got 0x%02x", vbTag)
	}

	vo := 0
	for vo < len(vbl) {
		_, vb, nvo, err := readTLV(vbl, vo)
		if err != nil {
			return nil, err
		}
		vo = nvo
		// varbind SEQUENCE { OID, value }
		_, oidBytes, _, err := readTLV(vb, 0)
		if err != nil {
			return nil, err
		}
		oid, err := decodeOID(oidBytes)
		if err != nil {
			return nil, err
		}
		pr.oids = append(pr.oids, oid)
	}

	return pr, nil
}

// -- BER encoding (responses) -------------------------------------------------

type berVarbind struct {
	oid   string
	typ   gosnmp.Asn1BER
	value any
}

func marshalGetResponse(requestID int, community string, varbinds []berVarbind) []byte {
	var vbl []byte
	for _, vb := range varbinds {
		var oidValue []byte
		switch vb.typ {
		case gosnmp.Integer:
			oidValue = encodeTLV(0x02, encodeInt(int32(vb.value.(int))))
		case gosnmp.OctetString:
			oidValue = encodeTLV(0x04, []byte(vb.value.(string)))
		case gosnmp.ObjectIdentifier:
			oidValue = encodeTLV(0x06, encodeOID(vb.value.(string)))
		case gosnmp.TimeTicks, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.Uinteger32:
			oidValue = encodeTLV(byte(vb.typ), encodeUint32(vb.value.(uint32)))
		case gosnmp.IPAddress:
			oidValue = encodeTLV(0x40, net.ParseIP(vb.value.(string)).To4())
		case gosnmp.EndOfMibView:
			oidValue = []byte{0x82, 0x00}
		default:
			panic(fmt.Sprintf("fake agent: unsupported value type %v", vb.typ))
		}
		vbl = append(vbl, encodeTLV(0x30, append(encodeTLV(0x06, encodeOID(vb.oid)), oidValue...))...)
	}

	var pdu []byte
	pdu = append(pdu, encodeTLV(0x02, encodeInt(int32(requestID)))...)
	pdu = append(pdu, encodeTLV(0x02, []byte{0x00})...) // errorStatus
	pdu = append(pdu, encodeTLV(0x02, []byte{0x00})...) // errorIndex
	pdu = append(pdu, encodeTLV(0x30, vbl)...)

	var msg []byte
	msg = append(msg, encodeTLV(0x02, []byte{0x01})...) // version 2c
	msg = append(msg, encodeTLV(0x04, []byte(community))...)
	msg = append(msg, encodeTLV(0xA2, pdu)...) // GetResponse

	return encodeTLV(0x30, msg)
}

func encodeTLV(tag byte, value []byte) []byte {
	out := []byte{tag}
	out = append(out, encodeLength(len(value))...)
	return append(out, value...)
}

func encodeLength(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte(n & 0xFF)}, b...)
		n >>= 8
	}
	return append([]byte{0x80 | byte(len(b))}, b...)
}

func encodeInt(v int32) []byte {
	if v == 0 {
		return []byte{0x00}
	}
	n := uint32(v)
	var b []byte
	for n > 0 {
		b = append([]byte{byte(n & 0xFF)}, b...)
		n >>= 8
	}
	// strip redundant sign-extension bytes
	for len(b) > 1 &&
		((b[0] == 0x00 && b[1]&0x80 == 0) || (b[0] == 0xFF && b[1]&0x80 != 0)) {
		b = b[1:]
	}
	return b
}

func encodeUint32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// encodeOID encodes a dotless dotted-decimal OID into BER sub-identifier
// bytes (first two components combined as 40*a+b).
func encodeOID(oid string) []byte {
	parts := strings.Split(oid, ".")
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			panic(fmt.Sprintf("fake agent: bad OID component %q", p))
		}
		nums[i] = n
	}
	if len(nums) < 2 || nums[0] > 6 || nums[1] >= 40 {
		panic(fmt.Sprintf("fake agent: unsupported OID %q", oid))
	}
	var out []byte
	out = append(out, byte(nums[0]*40+nums[1]))
	for _, n := range nums[2:] {
		var sub []byte
		sub = append(sub, byte(n%128))
		n /= 128
		for n > 0 {
			sub = append([]byte{byte(n%128) | 0x80}, sub...)
			n /= 128
		}
		out = append(out, sub...)
	}
	return out
}

// -- shared helpers -----------------------------------------------------------
func readTLV(data []byte, off int) (byte, []byte, int, error) {
	if off >= len(data) {
		return 0, nil, 0, fmt.Errorf("truncated TLV at %d", off)
	}
	tag := data[off]
	off++
	if off >= len(data) {
		return 0, nil, 0, fmt.Errorf("truncated length at %d", off)
	}
	length := int(data[off])
	off++
	if length&0x80 != 0 {
		n := length & 0x7F
		if n == 0 || off+n > len(data) {
			return 0, nil, 0, fmt.Errorf("bad long-form length at %d", off-1)
		}
		length = 0
		for i := 0; i < n; i++ {
			length = length<<8 | int(data[off+i])
		}
		off += n
	}
	if off+length > len(data) {
		return 0, nil, 0, fmt.Errorf("truncated TLV value: need %d bytes at %d", length, off)
	}
	return tag, data[off : off+length], off + length, nil
}

func decodeInt(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, fmt.Errorf("empty integer")
	}
	var v int32
	if b[0]&0x80 != 0 {
		v = -1
	}
	for _, x := range b {
		v = v<<8 | int32(x)
	}
	return int(v), nil
}

func decodeOID(b []byte) (string, error) {
	if len(b) == 0 {
		return "", fmt.Errorf("empty OID")
	}
	var parts []string
	first := int(b[0])
	parts = append(parts, strconv.Itoa(first/40), strconv.Itoa(first%40))
	i := 1
	for i < len(b) {
		var v int
		for {
			if i >= len(b) {
				return "", fmt.Errorf("truncated OID")
			}
			v = v<<7 | int(b[i]&0x7F)
			cont := b[i]&0x80 != 0
			i++
			if !cont {
				break
			}
		}
		parts = append(parts, strconv.Itoa(v))
	}
	return strings.Join(parts, "."), nil
}
