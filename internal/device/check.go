package device

import (
	"fmt"
	"regexp"
	"sync"

	"github.com/farshidmousavii/netmon/internal/backup"
	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/logger"
	"github.com/farshidmousavii/netmon/internal/report"
	"github.com/farshidmousavii/netmon/internal/snmp"
)

func CheckDeviceFull(deviceCfg config.DeviceConfig, cfg *config.Config, wg *sync.WaitGroup, reports chan<- report.DeviceReport, skipBackup bool) {
	defer wg.Done()

	report := report.DeviceReport{
		Name: deviceCfg.Name,
		IP:   deviceCfg.IP,
		Type: deviceCfg.Vendor,
	}

	cred, err := cfg.GetCredential(deviceCfg.Credential)
	if err != nil {
		logger.Error("device %s: failed to get credential: %v", deviceCfg.Name, err)
		report.Error = err
		reports <- report
		return
	}

	// new device
	device, err := newDevice(deviceCfg, cred)
	if err != nil {
		logger.Error("device %s: failed to create: %v", deviceCfg.Name, err)
		report.Error = err
		reports <- report
		return
	}

	// Ping
	pingTime, err := device.Ping()
	if err != nil {
		logger.Error("device %s: ping failed: %v", device.IP, err)
		report.Error = fmt.Errorf("device %s: ping failed: %w", device.IP, err)
		reports <- report
		return
	}
	report.Online = true
	report.PingTime = pingTime

	// SNMP
	var hostName string
	if cfg.SNMP != nil {
		oid, err := device.GetVendorSNMP(cfg.SNMP.Community, cfg.SNMP.Timeout)
		if err != nil {
			logger.Warning("device %s: Failed to get vendor via SNMP : %v", device.IP, err)
		} else {
			report.SNMPInfo.Vendor = snmp.ParseVendorSNMP(oid)
		}

		host, err := device.GetHostnameSNMP(cfg.SNMP.Community, cfg.SNMP.Timeout)
		if err != nil {
			logger.Warning("device %s: Failed to get hostname via SNMP: %v", device.IP, err)
		} else {
			report.SNMPInfo.Hostname = host
			hostName = host
		}

		uptime, err := device.GetUPTimeSNMP(cfg.SNMP.Community, cfg.SNMP.Timeout)
		if err != nil {
			logger.Warning("device %s: Failed to get uptime via SNMP: %v", device.IP, err)
		} else {
			report.SNMPInfo.Uptime = uptime
		}
	}

	//  Backup
	if !skipBackup {

		output, err := device.ShowCommand()
		if err != nil {
			logger.Error("device %s: failed to get config: %v", device.IP, err)
			report.Error = fmt.Errorf("device %s: failed to get config: %w", device.IP, err)
			reports <- report
			return
		}

		target := hostName
		if target == "" {
			target = extractHostname(device.Type(), output)
		}

		filePath, err := backup.WriteToFile(target, device.Type(), output, cfg.Backup.Directory, cfg.Backup.ArchivePath)
		if err != nil {
			logger.Error("device %s: failed to write backup: %v", device.IP, err)
			report.Error = fmt.Errorf("device %s: failed to write backup: %w", device.IP, err)
			reports <- report
			return
		}
		report.BackupPath = filePath
	}
	reports <- report

}

func extractHostname(deviceType, backupConfig string) string {

	var match []string

	switch deviceType {
	case "cisco":
		re := regexp.MustCompile(`\bhostname\s+(\S+)`)
		match = re.FindStringSubmatch(backupConfig)
	case "mikrotik":
		re := regexp.MustCompile(`(?m)^set\s+name=([^\s]+)`)
		match = re.FindStringSubmatch(backupConfig)
	}

	if len(match) > 1 {
		return match[1]
	}

	return ""
}
