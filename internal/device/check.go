package device

import (
	"fmt"
	"sync"

	"github.com/farshidmousavii/netmon/internal/backup"
	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/logger"
	"github.com/farshidmousavii/netmon/internal/report"
	"github.com/farshidmousavii/netmon/internal/snmp"
)

func CheckDeviceFull(deviceCfg config.DeviceConfig, cfg *config.Config, wg *sync.WaitGroup, reports chan<- report.DeviceReport) {
	defer wg.Done()

	report := report.DeviceReport{
		Name: deviceCfg.Name,
		IP:   deviceCfg.IP,
		Type: deviceCfg.Vendor,
	}

	cred, err := cfg.GetCredential(deviceCfg.Credential)
	if err != nil {
		logger.Error("Device %s: %v", deviceCfg.Name, err)
	}

	// new device
	device, err := newDevice(deviceCfg, cred)
	if err != nil {
		report.Error = err
		reports <- report
		return
	}

	// Ping
	pingTime, err := device.Ping()
	if err != nil {
		report.Error = fmt.Errorf("ping failed: %w", err)
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
			logger.Warning("Failed to get vendor via SNMP for %s: %v", device.IP, err)
		} else {
			report.SNMPInfo.Vendor = snmp.ParseVendorSNMP(oid)
		}

		host, err := device.GetHostnameSNMP(cfg.SNMP.Community, cfg.SNMP.Timeout)
		if err != nil {
			logger.Warning("Failed to get hostname via SNMP for %s: %v", device.IP, err)
		} else {
			report.SNMPInfo.Hostname = host
			hostName = host
		}

		uptime, err := device.GetUPTimeSNMP(cfg.SNMP.Community, cfg.SNMP.Timeout)
		if err != nil {
			logger.Warning("Failed to get uptime via SNMP for %s: %v", device.IP, err)
		} else {
			report.SNMPInfo.Uptime = uptime
		}
	}


	//  Backup

	output , err := device.ShowCommand()
	if err !=nil {
		report.Error = fmt.Errorf("can't get backup: %w" , err)
		reports <- report
		return
	}
	
	target := hostName
	if target == "" {
		target = device.IP
	}

	filePath ,err := backup.WriteToFile(target,device.Type() ,output , cfg.Backup.Directory , cfg.Backup.ArchivePath)
	if err !=nil {
		report.Error = fmt.Errorf("can not wirte to file: %w" , err)
		reports <- report
		return
	}
	report.BackupPath = filePath
	reports <- report

}