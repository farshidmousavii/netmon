package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var initFormat string

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize config files",
	Run:   runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().StringVar(&initFormat, "format", "yaml", "config format (yaml/csv)")
}

func runInit(cmd *cobra.Command, args []string) {
	switch strings.ToLower(initFormat) {
	case "yaml", "yml":
		createYAMLConfig()
	case "csv":
		createCSVConfig()
	default:
		fmt.Printf("Unsupported format: %s (use yaml or csv)\n", initFormat)
		os.Exit(1)
	}
}

func createYAMLConfig() {
	if _, err := os.Stat("config.yaml"); err == nil {
		fmt.Println("⚠ config.yaml already exists")
		return
	}

	configContent := `version: 1

credentials:
  cisco:
    username: admin
    password: changeme
  
  mikrotik:
    username: admin
    password: changeme

devices:
  - name: core-switch
    ip: 192.168.1.1
    port: 22
    vendor: cisco
    credential: cisco

snmp:
  community: public
  timeout: 10

backup:
  directory: backups
  archive_path: ""
`
	if err := os.WriteFile("config.yaml", []byte(configContent), 0644); err != nil {
		fmt.Printf("✗ Failed to create config.yaml: %v\n", err)
	} else {
		fmt.Println("✓ config.yaml created")
		fmt.Println("\nNext steps:")
		fmt.Println("  1. Edit config.yaml with your credentials")
		fmt.Println("  2. Run: netmon-cli monitor")
	}
}

func createCSVConfig() {
	if _, err := os.Stat("devices.csv"); err == nil {
		fmt.Println("⚠ devices.csv already exists")
		return
	}

	csvContent := `#snmp_community=public
#snmp_timeout=10
#backup_dir=backups
#backup_archive=""
name,ip,port,vendor,username,password
core-switch,192.168.1.1,22,cisco,admin,changeme
dist-switch,192.168.2.1,22,cisco,admin,changeme
edge-router,192.168.3.1,22,mikrotik,admin,changeme
`
	if err := os.WriteFile("devices.csv", []byte(csvContent), 0644); err != nil {
		fmt.Printf("✗ Failed to create devices.csv: %v\n", err)
	} else {
		fmt.Println("✓ devices.csv created")
		fmt.Println("\nNext steps:")
		fmt.Println("  1. Edit devices.csv with your devices")
		fmt.Println("  2. Run: netmon-cli monitor --config devices.csv")
	}
}
