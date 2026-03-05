package snmp

type SNMPInfo interface {
	GetHostnameSNMP(community string, timeout int) (string, error)
	GetVendorSNMP(community string, timeout int) (string, error)
	GetUptimeSNMP(community string, timeout int) (string, error)
}

type SNMPResult struct {
	Hostname string
	Vendor   string
	Uptime   string
	Err      error
}