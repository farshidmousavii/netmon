package report

import (
	"fmt"
	"strings"
	"time"
)

// PrintMonitorReport -- monitor (ping + SNMP)
func PrintMonitorReport(allReports []DeviceReport) {
	printHeader("NETWORK DEVICE HEALTH CHECK", len(allReports))

	for i, report := range allReports {
		printMonitorDevice(i+1, report)
	}

	printMonitorSummary(allReports)
}

// PrintBackupReport
func PrintBackupReport(allReports []DeviceReport) {
	printHeader("DEVICE CONFIGURATION BACKUP", len(allReports))

	for i, report := range allReports {
		printBackupDevice(i+1, report)
	}

	printBackupSummary(allReports)
}

// ===== Helper Functions =====

func printHeader(title string, totalDevices int) {
	fmt.Println()
	fmt.Println(strings.Repeat("═", 70))
	fmt.Printf("           %s\n", title)
	fmt.Println(strings.Repeat("═", 70))
	fmt.Printf("Started:       %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf("Total Devices: %d\n", totalDevices)
	fmt.Println()
}

// printMonitorDevice - ping + SNMP
func printMonitorDevice(num int, report DeviceReport) {
	fmt.Println(strings.Repeat("─", 70))
	fmt.Printf("Device #%d: %s (%s)\n", num, report.Name, report.IP)
	fmt.Println(strings.Repeat("─", 70))
	fmt.Printf("Type:     %s\n", report.Type)

	if report.Error != nil {
		fmt.Printf("Status:   ✗ Failed\n")
		fmt.Printf("Error:    %v\n", report.Error)
		fmt.Println()
		return
	}

	if report.Online {
		fmt.Printf("Status:   ✓ Online\n")
		if report.PingTime != "" {
			fmt.Printf("Ping:     %s\n", report.PingTime)
		}
	} else {
		fmt.Printf("Status:   ✗ Offline\n")
	}

	// SNMP Info
	if report.SNMPInfo.Hostname != "" || report.SNMPInfo.Vendor != "" || report.SNMPInfo.Uptime != "" {
		fmt.Println("\nSNMP Info:")
		if report.SNMPInfo.Hostname != "" {
			fmt.Printf("  Hostname: %s\n", report.SNMPInfo.Hostname)
		}
		if report.SNMPInfo.Vendor != "" {
			fmt.Printf("  Vendor:   %s\n", report.SNMPInfo.Vendor)
		}
		if report.SNMPInfo.Uptime != "" {
			fmt.Printf("  Uptime:   %s\n", report.SNMPInfo.Uptime)
		}
	}

	fmt.Println()
}

// printBackupDevice -  backup info
func printBackupDevice(num int, report DeviceReport) {
	fmt.Println(strings.Repeat("─", 70))
	fmt.Printf("Device #%d: %s (%s)\n", num, report.Name, report.IP)
	fmt.Println(strings.Repeat("─", 70))
	fmt.Printf("Type:     %s\n", report.Type)

	if report.Error != nil {
		fmt.Printf("Status:   ✗ Failed\n")
		fmt.Printf("Error:    %v\n", report.Error)
		fmt.Println()
		return
	}

	// Backup Status
	if report.BackupPath != "" {
		fmt.Printf("Status:   ✓ Success\n")
		fmt.Printf("Saved to: %s\n", report.BackupPath)
	} else {
		fmt.Printf("Status:   ✗ Failed\n")
	}

	fmt.Println()
}

func printMonitorSummary(reports []DeviceReport) {
	online := 0
	failed := 0

	for _, report := range reports {
		if report.Error != nil {
			failed++
		} else if report.Online {
			online++
		}
	}

	fmt.Println(strings.Repeat("═", 70))
	fmt.Println("Summary:")
	fmt.Printf("  Total:   %d devices\n", len(reports))
	fmt.Printf("  Online:  %d devices\n", online)
	fmt.Printf("  Failed:  %d devices\n", failed)
	fmt.Println(strings.Repeat("═", 70))
}

func printBackupSummary(reports []DeviceReport) {
	success := 0
	failed := 0

	for _, report := range reports {
		if report.Error != nil {
			failed++
		} else if report.BackupPath != "" {
			success++
		}
	}

	fmt.Println(strings.Repeat("═", 70))
	fmt.Println("Summary:")
	fmt.Printf("  Total:     %d devices\n", len(reports))
	fmt.Printf("  Success:   %d backups\n", success)
	fmt.Printf("  Failed:    %d devices\n", failed)
	fmt.Println(strings.Repeat("═", 70))
}
