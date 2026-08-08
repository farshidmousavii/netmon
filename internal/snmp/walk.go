package snmp

// Additive API for the daemon side (Phase 1 ARP collector, Phase 2 SNMP
// provider). Everything here lives alongside the legacy SnmpWalk without
// changing it: internal/device keeps working untouched.
//
// The key type is Varbind: each walked row is the OID suffix (the table's
// index — VLAN, MAC, ifIndex, IP address, whatever the MIB indexes by)
// plus the typed gosnmp-decoded value. The same shape serves
// ipNetToPhysicalTable / ipNetToMediaTable (Phase 1), and BRIDGE-MIB /
// Q-BRIDGE-MIB / LLDP / CDP walks (Phase 2).

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// Varbind is one typed value returned from a table walk.
//
// The walk base OID is the column being read (e.g.
// 1.3.6.1.2.1.4.22.1.2 = ipNetToMediaIfIndex); Suffix is the instance
// index relative to that base (e.g. [10, 0, 0, 1] for the ARP entry of
// 10.0.0.1, or six octets for a MAC-keyed table). Value keeps gosnmp's
// decoded Go type: OctetString -> []byte, ObjectIdentifier -> string,
// Integer -> int, TimeTicks/Counter/Gauge -> uint32, Counter64 -> uint64,
// IPAddress -> string, Null -> nil.
type Varbind struct {
	// OID is the full instance OID in dotless dotted-decimal form
	// (normalized: gosnmp itself emits a leading dot).
	OID string
	// Suffix is the table index: the components of OID after the walked
	// base OID.
	Suffix []int
	// Type is the ASN.1 BER type of Value.
	Type gosnmp.Asn1BER
	// Value is the typed value; see the struct comment.
	Value any
}

// V3Config carries the SNMPv3 USM parameters. It mirrors the columns of
// snmp_profiles (docs/database-schema.md): v3_username, v3_auth_protocol,
// v3_auth_key, v3_priv_protocol, v3_priv_key. The DB stores protocol names
// as text; use ParseAuthProtocol / ParsePrivProtocol to map them.
type V3Config struct {
	Username     string
	AuthProtocol gosnmp.SnmpV3AuthProtocol
	AuthKey      string
	PrivProtocol gosnmp.SnmpV3PrivProtocol
	PrivKey      string
}

// Config configures a Client. Zero fields take defaults (Port 161,
// Timeout 2s, Retries 2, MaxOids 60, Version defaults to v2c).
type Config struct {
	Target    string
	Port      uint16
	Version   gosnmp.SnmpVersion
	Community string // v2c
	Security  *V3Config
	Timeout   time.Duration
	Retries   int
	// MaxOids caps the GetBulk max-repetitions used by table walks
	// (gosnmp calls this MaxOids). Some agents misbehave on large
	// repetition counts; 60 is the safe default.
	MaxOids int
}

// Client is a connected SNMP session, one per device. It is NOT safe for
// concurrent use; each provider goroutine owns its own Client.
type Client struct {
	g *gosnmp.GoSNMP
}

// NewClient builds the gosnmp client from cfg (applying v3 USM parameters
// when Version is Version3) and connects. ctx is honoured at entry; the
// underlying dial is not context-cancellable.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("snmp connect cancelled: %w", err)
	}
	if cfg.Target == "" {
		return nil, fmt.Errorf("snmp target is empty")
	}
	if cfg.Version == 0 {
		cfg.Version = gosnmp.Version2c
	}
	if cfg.Port == 0 {
		cfg.Port = 161
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Second
	}
	if cfg.Retries == 0 {
		cfg.Retries = 2
	}
	if cfg.MaxOids == 0 {
		cfg.MaxOids = gosnmp.Default.MaxOids
	}

	g := &gosnmp.GoSNMP{
		Target:    cfg.Target,
		Port:      cfg.Port,
		Version:   cfg.Version,
		Community: cfg.Community,
		Timeout:   cfg.Timeout,
		Retries:   cfg.Retries,
		MaxOids:   cfg.MaxOids,
	}

	if cfg.Version == gosnmp.Version3 {
		if cfg.Security == nil {
			return nil, fmt.Errorf("SNMPv3 requires Security config")
		}
		if cfg.Security.Username == "" {
			return nil, fmt.Errorf("SNMPv3 requires a username")
		}
		// gosnmp's USM validation rejects protocol constants left at the
		// Go zero value (0); NoAuth/NoPriv are 1. Default explicitly so a
		// caller omitting either field still gets a valid client.
		if cfg.Security.AuthProtocol == 0 {
			cfg.Security.AuthProtocol = gosnmp.NoAuth
		}
		if cfg.Security.PrivProtocol == 0 {
			cfg.Security.PrivProtocol = gosnmp.NoPriv
		}
		if cfg.Security.PrivProtocol != gosnmp.NoPriv && cfg.Security.AuthProtocol == gosnmp.NoAuth {
			return nil, fmt.Errorf("SNMPv3 privacy requires authentication (auth-protocol cannot be NoAuth)")
		}
		g.SecurityModel = gosnmp.UserSecurityModel
		g.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 cfg.Security.Username,
			AuthenticationProtocol:   cfg.Security.AuthProtocol,
			AuthenticationPassphrase: cfg.Security.AuthKey,
			PrivacyProtocol:          cfg.Security.PrivProtocol,
			PrivacyPassphrase:        cfg.Security.PrivKey,
		}
		switch {
		case cfg.Security.AuthProtocol == gosnmp.NoAuth:
			g.MsgFlags = gosnmp.NoAuthNoPriv
		case cfg.Security.PrivProtocol != gosnmp.NoPriv:
			g.MsgFlags = gosnmp.AuthPriv
		default:
			g.MsgFlags = gosnmp.AuthNoPriv
		}
	} else if cfg.Security != nil {
		return nil, fmt.Errorf("Security config given for a non-v3 SNMP version")
	}

	if err := g.Connect(); err != nil {
		return nil, fmt.Errorf("connect to %s:%d: %w", cfg.Target, cfg.Port, err)
	}
	return &Client{g: g}, nil
}

// Close releases the client's connection. Safe to call once when done.
func (c *Client) Close() error {
	return c.g.Conn.Close()
}

// WalkTable walks baseOID (a table column root) and returns every value
// with its OID suffix, until the agent signals end of MIB. ctx is checked
// before and between varbinds, so cancelling it aborts the walk.
func (c *Client) WalkTable(ctx context.Context, baseOID string) ([]Varbind, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("walk table %s cancelled: %w", baseOID, err)
	}

	base, err := oidComponents(baseOID)
	if err != nil {
		return nil, fmt.Errorf("parse base OID %q: %w", baseOID, err)
	}

	var rows []Varbind
	err = c.g.Walk(baseOID, func(pdu gosnmp.SnmpPDU) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		comps, err := oidComponents(pdu.Name)
		if err != nil {
			return fmt.Errorf("parse varbind OID %q: %w", pdu.Name, err)
		}
		if len(comps) < len(base) {
			// Walk guarantees the base prefix; treat a violation as
			// data corruption rather than guessing.
			return fmt.Errorf("varbind OID %q is not under base %q", pdu.Name, baseOID)
		}

		rows = append(rows, Varbind{
			OID:    strings.Join(toStrings(comps), "."),
			Suffix: append([]int(nil), comps[len(base):]...),
			Type:   pdu.Type,
			Value:  pdu.Value,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", baseOID, err)
	}
	return rows, nil
}

// WalkTable is a convenience wrapper: connect with cfg, walk baseOID, close.
func WalkTable(ctx context.Context, cfg Config, baseOID string) ([]Varbind, error) {
	c, err := NewClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.WalkTable(ctx, baseOID)
}

// ParseAuthProtocol maps an snmp_profiles.v3_auth_protocol text value
// ("MD5", "SHA", "SHA224", ..., or "" / "NOAUTH") to the gosnmp constant.
func ParseAuthProtocol(s string) (gosnmp.SnmpV3AuthProtocol, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "", "NOAUTH", "NONE":
		return gosnmp.NoAuth, nil
	case "MD5":
		return gosnmp.MD5, nil
	case "SHA":
		return gosnmp.SHA, nil
	case "SHA224":
		return gosnmp.SHA224, nil
	case "SHA256":
		return gosnmp.SHA256, nil
	case "SHA384":
		return gosnmp.SHA384, nil
	case "SHA512":
		return gosnmp.SHA512, nil
	default:
		return 0, fmt.Errorf("unknown SNMPv3 auth protocol %q", s)
	}
}

// ParsePrivProtocol maps an snmp_profiles.v3_priv_protocol text value
// ("DES", "AES", "AES192", "AES192C", "AES256", "AES256C", or "" /
// "NOPRIV") to the gosnmp constant.
func ParsePrivProtocol(s string) (gosnmp.SnmpV3PrivProtocol, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "", "NOPRIV", "NONE":
		return gosnmp.NoPriv, nil
	case "DES":
		return gosnmp.DES, nil
	case "AES":
		return gosnmp.AES, nil
	case "AES192":
		return gosnmp.AES192, nil
	case "AES192C":
		return gosnmp.AES192C, nil
	case "AES256":
		return gosnmp.AES256, nil
	case "AES256C":
		return gosnmp.AES256C, nil
	default:
		return 0, fmt.Errorf("unknown SNMPv3 priv protocol %q", s)
	}
}

// oidComponents splits a dotted-decimal OID into ints, tolerating the
// leading dot gosnmp emits on varbind names.
func oidComponents(oid string) ([]int, error) {
	oid = strings.TrimPrefix(oid, ".")
	if oid == "" {
		return nil, fmt.Errorf("empty OID")
	}
	parts := strings.Split(oid, ".")
	comps := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("component %q of %q is not an integer", p, oid)
		}
		comps[i] = n
	}
	return comps, nil
}

func toStrings(ints []int) []string {
	out := make([]string, len(ints))
	for i, n := range ints {
		out[i] = strconv.Itoa(n)
	}
	return out
}
