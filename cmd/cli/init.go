package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize config files",
	Run: func(cmd *cobra.Command, args []string) {
		runInit()
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit() {
	// Check if files exist
	if _, err := os.Stat("config.yaml"); err == nil {
		fmt.Println("⚠ config.yaml already exists")
	} else {
		configContent := `version: 1

credentials:
  cisco:
    username: admin
    password: ${CISCO_PASSWORD}
  
  mikrotik:
    username: admin
    password: ${MIKROTIK_PASSWORD}

devices:
  - name: my-device
    ip: 192.168.1.1
    port: 22
    vendor: cisco
    credential: cisco

snmp:
  community: ${SNMP_COMMUNITY}
  timeout: 10

backup:
  directory: backups
  archive_path: ""
`
		if err := os.WriteFile("config.yaml", []byte(configContent), 0644); err != nil {
			fmt.Printf("Failed to create config.yaml: %v\n", err)
		} else {
			fmt.Println("config.yaml created")
		}
	}

	if _, err := os.Stat(".env"); err == nil {
		fmt.Println(".env already exists")
	} else {
		envContent := `CISCO_PASSWORD=changeme
MIKROTIK_PASSWORD=changeme
SNMP_COMMUNITY=public
`
		if err := os.WriteFile(".env", []byte(envContent), 0600); err != nil {
			fmt.Printf("Failed to create .env: %v\n", err)
		} else {
			fmt.Println(".env created")
		}
	}

	fmt.Println("\nNext steps:")
	fmt.Println("  1. Edit config.yaml with your devices")
	fmt.Println("  2. Edit .env with your credentials")
	fmt.Println("  3. Run: netmon-cli")
}
