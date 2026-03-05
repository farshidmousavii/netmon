package report

import "github.com/farshidmousavii/netmon/internal/snmp"

type DeviceReport struct {
    Name       string
    IP         string
    Type       string
    Online     bool
    PingTime   string
    SNMPInfo   snmp.SNMPResult  
    BackupPath string
    Error      error
}