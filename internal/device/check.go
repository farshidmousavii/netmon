package device

import (
	"context"
	"fmt"

	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/logger"
	"github.com/farshidmousavii/netmon/internal/report"
	"github.com/farshidmousavii/netmon/internal/snmp"
)

func CheckDevice(ctx context.Context, deviceCfg config.DeviceConfig, cfg *config.Config, reports chan<- report.DeviceReport, skipBackup bool) {

	report := report.DeviceReport{
		Name: deviceCfg.Name,
		IP:   deviceCfg.IP,
		Type: deviceCfg.Vendor,
	}

	select {
	case <-ctx.Done():
		report.Error = fmt.Errorf("operation cancelled")
		reports <- report
		return
	default:
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

	select {
	case <-ctx.Done():
		report.Error = fmt.Errorf("operation cancelled")
		reports <- report
		return
	default:
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
		select {
		case <-ctx.Done():
			reports <- report
			return
		default:
		}
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
