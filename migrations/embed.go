// Package migrations embeds the golang-migrate SQL files so the compiled
// bidar binary runs `bidar migrate` from any working directory. The daemon
// (systemd) and the CLI never depend on a checked-out repo or a
// migrations/ directory on disk.
package migrations

import "embed"

// FS holds every *.sql migration file in this directory (up and down).
//
//go:embed *.sql
var FS embed.FS
