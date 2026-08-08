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

var hostsCmd = &cobra.Command{
	Use:   "hosts [hostname-substring]",
	Short: "List hosts from the database",
	Long: `List the reconciled hosts table (the Phase 1 inventory) with
hostname, IP, MAC, VLAN, AD status and last-seen times.

Environment-configured (not the --config flag):
  BIDAR_DATABASE_URL   Postgres connection string (required)

An optional argument filters by hostname substring (case-insensitive).
This is the Phase 1 inspection surface; the full REST API is Phase 4.`,
	Args: cobra.MaximumNArgs(1),
	Run:  runHosts,
}

func init() {
	rootCmd.AddCommand(hostsCmd)
}

func runHosts(cmd *cobra.Command, args []string) {
	if err := logger.Init(false); err != nil {
		log.Fatal(err)
	}

	filter := ""
	if len(args) == 1 {
		filter = strings.ToLower(strings.TrimSpace(args[0]))
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	databaseURL, err := db.DatabaseURLFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	hosts, err := store.New(pool).ListHosts(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// Table header.
	fmt.Printf("%-24s %-16s %-18s %-6s %-10s %-10s %-12s %-12s\n",
		"HOSTNAME", "IP", "MAC", "VLAN", "VLAN-SRC", "AD", "MATCH", "LAST SEEN")

	count := 0
	for _, h := range hosts {
		name := ""
		if h.Hostname != nil {
			name = *h.Hostname
		}
		if filter != "" && !strings.Contains(strings.ToLower(name), filter) {
			continue
		}
		ip := ""
		if h.CurrentIP != nil {
			ip = h.CurrentIP.String()
		}
		mac := ""
		if h.CurrentMAC != nil {
			mac = h.CurrentMAC.String()
		}
		vlan := ""
		if h.CurrentVLAN != nil {
			vlan = fmt.Sprintf("%d", *h.CurrentVLAN)
		}
		vlanSrc := ""
		if h.VLANSrc != nil {
			vlanSrc = *h.VLANSrc
		}
		lastSeen := ""
		for _, t := range []*time.Time{h.LastADSyncAt, h.LastPresenceAt} {
			if t != nil && lastSeen == "" {
				lastSeen = t.UTC().Format("2006-01-02 15:04")
			}
		}

		fmt.Printf("%-24s %-16s %-18s %-6s %-10s %-10s %-12s %-12s\n",
			name, ip, mac, vlan, vlanSrc, h.ADStatus, h.MatchStatus, lastSeen)
		count++
	}

	fmt.Printf("\n%d host(s)\n", count)
}
