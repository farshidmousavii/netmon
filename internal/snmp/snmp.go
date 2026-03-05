package snmp

import (
	"fmt"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

func SnmpWalk(ip string, oid []string, community string, timeout int) (string, error) {
	g := &gosnmp.GoSNMP{
		Target:    ip,
		Port:      161,
		Community: community,
		Version:   gosnmp.Version2c,
		Timeout:   time.Duration(timeout) * time.Second,
		Retries:   3,
	}

	err := g.Connect()

	if err != nil {
		return "", fmt.Errorf("Connecting SNMP error : %w", err)
	}
	defer g.Conn.Close()

	result, err := g.Get(oid)
	if err != nil {
		return "", fmt.Errorf("Can not get OID %w", err)
	}
	var output string
	for _, variable := range result.Variables {
		switch variable.Type {
		case gosnmp.OctetString:
			bytes := variable.Value.([]byte)
			output = string(bytes)

		case gosnmp.ObjectIdentifier:
			output = variable.Value.(string)
		case gosnmp.TimeTicks:
			t := variable.Value.(uint32) / 100
			duration := time.Duration(t) * time.Second
			days := duration / (24 * time.Hour)
			remaining := duration % (24 * time.Hour)

			hours := remaining / time.Hour
			remaining %= time.Hour

			minutes := remaining / time.Minute
			remaining %= time.Minute

			seconds := remaining / time.Second

			output = fmt.Sprintf("%d days, %02d:%02d:%02d",
				days, hours, minutes, seconds)

		default:
			output = gosnmp.ToBigInt(variable.Value).String()
		}
	}
	return output, nil
}

func ParseVendorSNMP(oid string) string {
	enterpriseMAP := map[string]string{
		"9":     "cisco",
		"14988": "mikrotik",
		"2636":  "juniper",
		"2011":  "huawei",
		"12356": "fortinet",
		"11":    "hp",
	}

	parts := strings.Split(oid, ".")
    if len(parts) < 8 {
        return "unknown" 
	} 

    enterpriseID := parts[7]
    if vendor, ok := enterpriseMAP[enterpriseID]; ok {
        return vendor
    }
    return "unknown"

}

