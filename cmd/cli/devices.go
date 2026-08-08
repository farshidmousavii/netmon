package cli

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/farshidmousavii/bidar/internal/db"
	"github.com/farshidmousavii/bidar/internal/logger"
	"github.com/farshidmousavii/bidar/internal/store"
	"github.com/spf13/cobra"
)

var devicesCmd = &cobra.Command{
	Use:   "devices",
	Short: "Manage network devices",
	Long: `Manage the canonical device list (network_devices).

Environment-configured (not the --config flag):
  BIDAR_DATABASE_URL   Postgres connection string (required)`,
}

var devicesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List devices, optionally filtered by role",
	Run:   runDevicesList,
}

var devicesSetRoleCmd = &cobra.Command{
	Use:   "set-role <name-or-mgmt_ip> <core|access|unassigned>",
	Short: "Set a device's role",
	Long: `Set a device's role: core (polled by the Phase 1 ARP collector),
access (Phase 2), or unassigned (not polled).

Takes effect on the next scheduled poll cycle — no daemon restart needed
(the ARP provider re-reads role fresh every cycle).`,
	Args: cobra.ExactArgs(2),
	Run:  runDevicesSetRole,
}

func init() {
	rootCmd.AddCommand(devicesCmd)
	devicesCmd.AddCommand(devicesListCmd)
	devicesCmd.AddCommand(devicesSetRoleCmd)

	devicesListCmd.Flags().String("role", "", "filter by role (core|access|unassigned)")
}

func devicesStore(ctx context.Context) (*store.Store, func()) {
	databaseURL, err := db.DatabaseURLFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	return store.New(pool), pool.Close
}

func runDevicesList(cmd *cobra.Command, args []string) {
	if err := logger.Init(false); err != nil {
		log.Fatal(err)
	}

	var role *string
	if raw := strings.TrimSpace(cmd.Flags().Lookup("role").Value.String()); raw != "" {
		if !store.ValidDeviceRoles[raw] {
			log.Fatalf("invalid role %q (use core, access or unassigned)", raw)
		}
		role = &raw
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	st, closePool := devicesStore(ctx)
	defer closePool()

	devices, err := st.ListDevices(ctx, role)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%-28s %-16s %-18s %-10s %-8s\n", "NAME", "MGMT IP", "PROTOCOL FAMILY", "ROLE", "ENABLED")
	for _, d := range devices {
		enabled := "yes"
		if !d.Enabled {
			enabled = "no"
		}
		fmt.Printf("%-28s %-16s %-18s %-10s %-8s\n",
			d.Name, d.MgmtIP.String(), d.ProtocolFamily, d.Role, enabled)
	}
	fmt.Printf("\n%d device(s)\n", len(devices))
}

func runDevicesSetRole(cmd *cobra.Command, args []string) {
	if err := logger.Init(false); err != nil {
		log.Fatal(err)
	}

	nameOrIP := strings.TrimSpace(args[0])
	role := strings.TrimSpace(args[1])
	if !store.ValidDeviceRoles[role] {
		log.Fatalf("invalid role %q (use core, access or unassigned)", role)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	st, closePool := devicesStore(ctx)
	defer closePool()

	id, err := st.SetDeviceRole(ctx, nameOrIP, role)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("device %d (%s): role -> %s\n", id, nameOrIP, role)
}
