package snmp

import "testing"

// Regression test for the ParseVendorSNMP enterprise-ID index bug.
//
// Real reference values, sourced from the vendors' own MIBs:
//   - Cisco 3725:      CISCO-PRODUCTS-MIB  "cisco3725 ::= { ciscoProducts 414 }"
//     => sysObjectID 1.3.6.1.4.1.9.1.414
//   - Catalyst 2950-24: CISCO-PRODUCTS-MIB "catalyst295024 ::= { ciscoProducts 324 }"
//     => sysObjectID 1.3.6.1.4.1.9.1.324
//   - MikroTik RouterOS: MIKROTIK-MIB "mikrotik ::= { enterprises 14988 }",
//     "mtXRouterOs ::= { mikrotikExperimentalModule 1 }",
//     "mikrotikExperimentalModule ::= { mikrotik 1 }"
//     => sysObjectID 1.3.6.1.4.1.14988.1
//
// Both forms are covered because gosnmp v1.43.2 decodes OID values with a
// leading dot (parseObjectIdentifier prepends '.'), whereas OIDs written
// down conventionally (and by other SNMP libraries) have none.
func TestParseVendorSNMP(t *testing.T) {
	tests := []struct {
		name string
		oid  string
		want string
	}{
		// Real Cisco products (dotless, conventional form).
		{"cisco 3725 router", "1.3.6.1.4.1.9.1.414", "cisco"},
		{"catalyst 2950-24 switch", "1.3.6.1.4.1.9.1.324", "cisco"},
		// Same OIDs as gosnmp actually returns them (leading dot).
		{"cisco 3725 router (gosnmp form)", ".1.3.6.1.4.1.9.1.414", "cisco"},
		{"catalyst 2950-24 switch (gosnmp form)", ".1.3.6.1.4.1.9.1.324", "cisco"},
		// Real MikroTik RouterOS root, both forms.
		{"mikrotik routeros", "1.3.6.1.4.1.14988.1", "mikrotik"},
		{"mikrotik routeros (gosnmp form)", ".1.3.6.1.4.1.14988.1", "mikrotik"},
		// Other mapped enterprises.
		{"juniper", "1.3.6.1.4.1.2636.1.1.1.2.1", "juniper"},
		{"huawei", "1.3.6.1.4.1.2011.6.10.1", "huawei"},
		{"fortinet", "1.3.6.1.4.1.12356.101.1", "fortinet"},
		{"hp", "1.3.6.1.4.1.11.2.3.7.11.1", "hp"},
		// Enterprise root exactly (no sub-identifier after it) must still parse.
		{"cisco enterprise root", "1.3.6.1.4.1.9", "cisco"},
		// Unmapped enterprise.
		{"unknown enterprise", "1.3.6.1.4.1.99999.1", "unknown"},
		// Malformed / too short inputs.
		{"empty", "", "unknown"},
		{"single component", "9", "unknown"},
		{"iana root only", "1.3.6.1.4.1", "unknown"},
		{"not an oid", "not.an.oid", "unknown"},
		{"leading-dot garbage", ".not.an.oid", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseVendorSNMP(tt.oid); got != tt.want {
				t.Errorf("ParseVendorSNMP(%q) = %q, want %q", tt.oid, got, tt.want)
			}
		})
	}
}
