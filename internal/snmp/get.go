package snmp

// Scalar GET support alongside the table walks in walk.go. System info
// (sysDescr, sysName, sysUpTime), serial numbers, and similar scalars are
// single-OID reads, not walks. Additive only — the legacy SnmpWalk in
// snmp.go is untouched.

import (
	"context"
	"fmt"
	"strings"
)

// Get fetches one or more scalar OIDs in a single request. Results come
// back in request order; Suffix is nil (scalars have no table index).
//
// An OID the agent does not serve comes back as a Varbind with no value
// (gosnmp Null / EndOfMibView / NoSuch* type) rather than an error — the
// caller decides whether that is missing evidence or a problem.
func (c *Client) Get(ctx context.Context, oids ...string) ([]Varbind, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("snmp get cancelled: %w", err)
	}
	if len(oids) == 0 {
		return nil, fmt.Errorf("snmp get: no oids requested")
	}
	for _, oid := range oids {
		if strings.TrimPrefix(oid, ".") == "" {
			return nil, fmt.Errorf("snmp get: empty oid")
		}
	}

	pdus, err := c.g.Get(oids)
	if err != nil {
		return nil, fmt.Errorf("snmp get %s: %w", strings.Join(oids, ","), err)
	}

	out := make([]Varbind, 0, len(pdus.Variables))
	for _, pdu := range pdus.Variables {
		out = append(out, Varbind{
			OID:   strings.TrimPrefix(pdu.Name, "."),
			Type:  pdu.Type,
			Value: pdu.Value,
		})
	}
	return out, nil
}
