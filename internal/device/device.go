package device

import (
	"fmt"
	"regexp"

	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/snmp"
)

func (d Device) GetHostName(config string, deviceType string) (string, error) {

	regex := ""

	switch deviceType {
	case "cisco":
		regex = `\bhostname\s+(\S+)`
	case "mikrotik":
		regex = `set\s+name=([A-Za-z0-9._-]+)`
	}
	re := regexp.MustCompile(regex)
	match := re.FindStringSubmatch(config)

	if len(match) < 2 {
		return "", fmt.Errorf("hostname not found in config")
	}
	return match[1], nil

}


func (d Device) Ping() (string, error) {

	return pingDevice(d.IP)

}
func (d Device) Type() string {
	if d.Vendor == "" {
		return "Unknown"
	}
	return d.Vendor
}

func (d Device) ShowCommand() (string, error) {
	sshClient, err := sshToDevice(d.IP, d.Port, d.Username, d.Password)
	if err != nil {
		return "", fmt.Errorf("Can not login to %s %s", d.IP, err)
	}
	defer sshClient.Close()

	session, err := sshClient.NewSession()

	if err != nil {
		return "", fmt.Errorf("Erro creating new session %s %s", d.IP, err)
	}
	defer session.Close()

	var (
		ciscooutput    string
		mikrotikOutput []byte
		cmErr          error
		cmd            string
	)

	switch d.Type() {
	case "cisco":
		ciscooutput, cmErr = runCisco(session, d.Password)

	case "mikrotik":
		cmd = "export compact"
		mikrotikOutput, cmErr = session.CombinedOutput(cmd)

	default:
		return "", fmt.Errorf("unknown device type")
	}

	if cmErr != nil {
		return "", fmt.Errorf("cannot run command %s on %s: %w", cmd, d.IP, err)
	}

	if ciscooutput != "" {
		return ciscooutput, nil
	} else {

		return string(mikrotikOutput), nil
	}

}


func getDeviceIP(d DeviceInfo) string {
	if dev, ok := d.(Device); ok {
		return dev.IP
	}
	return "unknown"
}

func (d Device) GetVendorSNMP(community string, timeout int) (string, error) {
	oid := []string{"1.3.6.1.2.1.1.2.0"}
	result, err := snmp.SnmpWalk(d.IP, oid, community, timeout)
	if err != nil {
		return "", fmt.Errorf("Error get Vendor by SNMP %w", err)
	}

	return result, nil
}

func (d Device) GetHostnameSNMP(community string, timeout int) (string, error) {
	oid := []string{"1.3.6.1.2.1.1.5.0"}
	result, err := snmp.SnmpWalk(d.IP, oid, community, timeout)
	if err != nil {
		return "", fmt.Errorf("Error get hostname by SNMP %w", err)
	}
	return result, nil
}

func (d Device) GetUPTimeSNMP(community string, timeout int) (string, error) {
	oid := []string{"1.3.6.1.2.1.1.3.0"}
	result, err := snmp.SnmpWalk(d.IP, oid, community, timeout)
	if err != nil {
		return "", fmt.Errorf("Error get uptime by SNMP %w", err)
	}
	return result, nil
}


func newDevice(cfg config.DeviceConfig, credential config.CredentialInfo) (Device, error) {


	return Device{
		IP:       cfg.IP,
		Port:     cfg.Port,
		Username: credential.Username,
		Password: credential.Password,
		Vendor:   cfg.Vendor,
	}, nil
}

