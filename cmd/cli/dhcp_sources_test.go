package cli

// Unit tests for the Windows-share -> container-path translation behind
// dhcp-sources set-path.

import "testing"

func TestTranslateDHCPPath(t *testing.T) {
	cases := []struct {
		name       string
		shareSrc   string
		given      string
		wantStored string
		wantWarn   bool
	}{
		{
			name:       "container form unchanged",
			shareSrc:   "//dc01/dhcp$",
			given:      "/mnt/dhcp/leases.json",
			wantStored: "/mnt/dhcp/leases.json",
		},
		{
			name:       "UNC path translated",
			shareSrc:   "//dc01/dhcp$",
			given:      `\\dc01\dhcp$\leases.json`,
			wantStored: "/mnt/dhcp/leases.json",
		},
		{
			name:       "UNC forward-slash form translated",
			shareSrc:   "//dc01/dhcp$",
			given:      "//dc01/dhcp$/sub/leases.json",
			wantStored: "/mnt/dhcp/sub/leases.json",
		},
		{
			name:       "case-insensitive share match",
			shareSrc:   "//DC01/DHCP$",
			given:      `\\dc01\dhcp$\leases.json`,
			wantStored: "/mnt/dhcp/leases.json",
		},
		{
			name:       "drive letter translated",
			shareSrc:   "Z:/dhcp",
			given:      `Z:\dhcp\leases.json`,
			wantStored: "/mnt/dhcp/leases.json",
		},
		{
			name:       "share root itself",
			shareSrc:   "//dc01/dhcp$",
			given:      `\\dc01\dhcp$`,
			wantStored: "/mnt/dhcp",
		},
		{
			name:       "unmatched windows path warns",
			shareSrc:   "//dc01/dhcp$",
			given:      `\\other\share\leases.json`,
			wantStored: `\\other\share\leases.json`,
			wantWarn:   true,
		},
		{
			name:       "no share configured keeps path",
			shareSrc:   "",
			given:      `\\dc01\dhcp$\leases.json`,
			wantStored: `\\dc01\dhcp$\leases.json`,
		},
		{
			name:       "default ./dhcp share keeps path",
			shareSrc:   "./dhcp",
			given:      "some/relative/path.json",
			wantStored: "some/relative/path.json",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stored, warn := translateDHCPPath(c.shareSrc, dhcpMountPoint, c.given)
			if stored != c.wantStored {
				t.Errorf("stored = %q, want %q", stored, c.wantStored)
			}
			if (warn != "") != c.wantWarn {
				t.Errorf("warn = %q, want warn=%v", warn, c.wantWarn)
			}
		})
	}
}
