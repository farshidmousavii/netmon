package device

import (
	"fmt"
	"sync"

	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/logger"
	"github.com/farshidmousavii/netmon/internal/report"
	"github.com/farshidmousavii/netmon/internal/snmp"
)

func CheckDevice(deviceCfg config.DeviceConfig, cfg *config.Config, wg *sync.WaitGroup, reports chan<- report.DeviceReport, skipBackup bool) {
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
	device, err := NewDevice(deviceCfg, cred)
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
		}

		uptime, err := device.GetUPTimeSNMP(cfg.SNMP.Community, cfg.SNMP.Timeout)
		if err != nil {
			logger.Warning("device %s: Failed to get uptime via SNMP: %v", device.IP, err)
		} else {
			report.SNMPInfo.Uptime = uptime
		}
	}

	reports <- report

}
