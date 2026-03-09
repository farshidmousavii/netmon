package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/farshidmousavii/netmon/internal/backup"
	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/device"
	"github.com/farshidmousavii/netmon/internal/logger"
	"github.com/farshidmousavii/netmon/internal/report"
)


func main() {

    if len(os.Args) > 1 && os.Args[1] == "diff" {
        runDiff()
        return
    }

    //run monitorig
    runMonitor()
}

//subcommand
func runDiff() {
	fmt.Println("run diff")
    if len(os.Args) < 4 {
        fmt.Fprintf(os.Stderr, "Usage: %s diff <file1> <file2>\n", os.Args[0])
        os.Exit(1)
    }

    file1 := os.Args[2]
    file2 := os.Args[3]

    identical, diffs, err := backup.CompareFiles(file1, file2)
    if err != nil {
        log.Fatal(err)
    }

    if identical {
        fmt.Println("Files are identical")
        return
    }

    fmt.Printf("Found %d differences:\n\n", len(diffs))
    for _, diff := range diffs {
        fmt.Printf("Line %d:\n", diff.Line)
        if diff.OldContent != "" {
            fmt.Printf("  - %s\n", diff.OldContent)
        }
        if diff.NewContent != "" {
            fmt.Printf("  + %s\n", diff.NewContent)
        }
        fmt.Println()
    }
}

func runMonitor() {
    logToFile := flag.Bool("l", false, "enable file logging")
    configPath := flag.String("config", "config.yaml", "path to config file")
    skipBackup := flag.Bool("skip-backup", false, "skip backup, only health check")
    skipSNMP := flag.Bool("skip-snmp", false, "skip SNMP queries")
    jsonOutput := flag.Bool("json", false, "output as JSON")

    flag.Parse()

    if err := logger.Init(*logToFile); err != nil {
        log.Fatal(err)
    }

    if err := config.LoadEnvVars(); err != nil {
        logger.Warning("failed to load .env: %v", err)
    }

    cfg, err := config.LoadConfig(*configPath)
    if err != nil {
        log.Fatal(err)
    }

    if *skipSNMP {
        cfg.SNMP = nil
    }

    reports := make(chan report.DeviceReport, len(cfg.Devices))
    var wg sync.WaitGroup

    logger.Info("Starting network monitor")

    for _, deviceCfg := range cfg.Devices {
        wg.Add(1)
        go device.CheckDeviceFull(deviceCfg, cfg, &wg, reports, *skipBackup)
    }

    wg.Wait()
    close(reports)

    var allReports []report.DeviceReport
    for r := range reports {
        allReports = append(allReports, r)
    }

    if *jsonOutput {
        if err := report.ReportToJson(allReports); err != nil {
            log.Fatal(err)
        }
    } else {
        report.PrintReport(allReports)
    }

    logger.Info("Network monitor completed")
}
