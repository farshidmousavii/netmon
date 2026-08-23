package cli

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/farshidmousavii/bidar/internal/crypto"
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

var devicesSetRouterOSAuthCmd = &cobra.Command{
	Use:   "set-routeros-auth <name-or-mgmt_ip> <username> <password>",
	Short: "Set a MikroTik device's RouterOS API credentials",
	Long: `Store RouterOS API credentials for a mikrotik_routeros device,
encrypted at rest with BIDAR_MASTER_KEY. The Phase 2 polling provider
uses them to read the ARP and wireless tables.

Optional --port overrides the RouterOS API port (default 8728); without
the flag any previously stored port is left unchanged.

Takes effect on the next scheduled poll cycle — no daemon restart
needed.`,
	Args: cobra.ExactArgs(3),
	Run:  runDevicesSetRouterOSAuth,
}

func init() {
	rootCmd.AddCommand(devicesCmd)
	devicesCmd.AddCommand(devicesListCmd)
	devicesCmd.AddCommand(devicesSetRoleCmd)
	devicesCmd.AddCommand(devicesSetRouterOSAuthCmd)

	devicesListCmd.Flags().String("role", "", "filter by role (core|access|unassigned)")
	devicesSetRouterOSAuthCmd.Flags().Int("port", 0, "RouterOS API port (default: leave unchanged, device default 8728)")
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

func runDevicesSetRouterOSAuth(cmd *cobra.Command, args []string) {
	if err := logger.Init(false); err != nil {
		log.Fatal(err)
	}

	nameOrIP := strings.TrimSpace(args[0])
	username := strings.TrimSpace(args[1])
	password := args[2]

	var port *int32
	if raw := cmd.Flags().Lookup("port").Value.String(); raw != "" && raw != "0" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > 65535 {
			log.Fatalf("invalid --port %q (use 1-65535)", raw)
		}
		p := int32(n)
		port = &p
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	st, closePool := devicesStore(ctx)
	defer closePool()

	enc, err := crypto.NewFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	pwEnc, err := enc.Encrypt([]byte(password))
	if err != nil {
		log.Fatal(err)
	}

	id, err := st.SetDeviceRouterOSAuth(ctx, nameOrIP, username, pwEnc, port)
	if err != nil {
		log.Fatal(err)
	}
	msg := fmt.Sprintf("device %d (%s): RouterOS credentials stored for user %q", id, nameOrIP, username)
	if port != nil {
		msg += fmt.Sprintf(", port %d", *port)
	}
	fmt.Println(msg)
}
