package mikrotik

// Inventory reads beyond DHCP leases: the ARP table and the wireless
// registration table, added in Phase 2 (docs/roadmap.md task 3). Purely
// additive — the DHCP-lease path live in production since Phase 1 stays
// untouched in mikrotik.go, and every read goes through the same
// RunCommand wire path.

import (
	"context"
	"net"
	"net/netip"
)

// ARP is one /ip/arp/print row: an IP↔MAC mapping as seen by the router.
type ARP struct {
	ID        string
	Address   netip.Addr
	MAC       net.HardwareAddr
	Interface string
	DHCP      bool // learned from DHCP rather than discovered on the wire
}

// ARPs prints the router's ARP table. Rows without a usable address or
// MAC are skipped — incomplete entries are not presence evidence.
func (c *Client) ARPs(ctx context.Context) ([]ARP, error) {
	rows, err := c.RunCommand(ctx, "/ip/arp/print", map[string]string{
		".proplist": ".id,address,mac-address,interface,dhcp",
	})
	if err != nil {
		return nil, err
	}

	out := make([]ARP, 0, len(rows))
	for _, row := range rows {
		a := ARP{
			ID:        row["id"],
			Interface: row["interface"],
			DHCP:      row["dhcp"] == "yes" || row["dhcp"] == "true",
		}
		addr, err := netip.ParseAddr(row["address"])
		if err != nil {
			continue // an entry without an address is noise
		}
		a.Address = addr
		mac, err := net.ParseMAC(row["mac-address"])
		if err != nil {
			continue // same for an entry without a MAC
		}
		a.MAC = mac
		out = append(out, a)
	}
	return out, nil
}

// WirelessRegistration is one /interface/wireless/registration-table row:
// an associated wireless client.
type WirelessRegistration struct {
	ID        string
	Interface string
	MAC       net.HardwareAddr
	Service   string // e.g. "802.11"
	Signal    string // raw RouterOS form, e.g. "-58dBm@6Mbps"
	LastIP    netip.Addr
}

// WirelessRegistrations prints associated wireless clients. Rows without
// a usable MAC are skipped; LastIP is optional evidence.
//
// Legacy path (/interface/wireless/...): RouterOS 7 wifi drivers expose
// /interface/wifi/registration-table instead — probing between the two
// is the polling provider's job (Phase 2 task 5b), keeping this client
// method single-purpose.
func (c *Client) WirelessRegistrations(ctx context.Context) ([]WirelessRegistration, error) {
	rows, err := c.RunCommand(ctx, "/interface/wireless/registration-table/print", map[string]string{
		".proplist": ".id,interface,mac-address,service,signal-strength,last-ip",
	})
	if err != nil {
		return nil, err
	}

	out := make([]WirelessRegistration, 0, len(rows))
	for _, row := range rows {
		w := WirelessRegistration{
			ID:        row["id"],
			Interface: row["interface"],
			Service:   row["service"],
			Signal:    row["signal-strength"],
		}
		mac, err := net.ParseMAC(row["mac-address"])
		if err != nil {
			continue // the MAC is the identity; without it the row is noise
		}
		w.MAC = mac
		if ip, err := netip.ParseAddr(row["last-ip"]); err == nil {
			w.LastIP = ip
		}
		out = append(out, w)
	}
	return out, nil
}
