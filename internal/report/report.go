package report

import (
	"fmt"
	"strings"
	"time"
)

func PrintReport(reports <-chan DeviceReport) {
    var allReports []DeviceReport
    
    for report := range reports {
        allReports = append(allReports, report)
    }

    // Header
    printHeader(len(allReports))

    for i, report := range allReports {
        printDeviceReport(i+1, report)
    }

    // Summary
    printSummary(allReports)
}

func printHeader(totalDevices int) {
    fmt.Println()
    fmt.Println(strings.Repeat("═", 70))
    fmt.Println("           NETWORK DEVICE MONITORING REPORT")
    fmt.Println(strings.Repeat("═", 70))
    fmt.Printf("Started:       %s\n", time.Now().Format("2006-01-02 15:04:05"))
    fmt.Printf("Total Devices: %d\n", totalDevices)
    fmt.Println()
}

func printDeviceReport(num int, report DeviceReport) {
    fmt.Println(strings.Repeat("─", 70))
    fmt.Printf("Device #%d: %s (%s)\n", num, report.Name, report.IP)
    fmt.Println(strings.Repeat("─", 70))
    fmt.Printf("Type:     %s\n", report.Type)

    // Status
    if report.Error != nil {
        fmt.Printf("Status:   ✗ Failed\n")
        fmt.Printf("Error:    %v\n", report.Error)
        fmt.Println()
        return  
    }

    // Online status
    if report.Online {
        fmt.Printf("Status:   ✓ Online\n")
        fmt.Printf("Ping:     %s\n", report.PingTime)
    } else {
        fmt.Printf("Status:   ✗ Offline\n")
    }

    // SNMP Info
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
    

    // Backup
    fmt.Println("\nBackup:")
    if report.BackupPath != "" {
        fmt.Printf("  ✓ Saved to: %s\n", report.BackupPath)
    } else {
        fmt.Printf("  ✗ Not saved\n")
    }

    fmt.Println()
}

func printSummary(reports []DeviceReport) {
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