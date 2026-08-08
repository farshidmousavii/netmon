// Package dlog provides the daemon's structured logger: a log/slog JSON
// handler, constructor-injected everywhere it is used.
//
// It is deliberately unrelated to internal/logger (the legacy CLI's color
// logger with a package-level global). Daemon code (bidar serve, and later
// internal/providers/* and internal/api) uses this package; the legacy CLI
// commands keep internal/logger. The two never import each other.
package dlog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// LevelEnv is the environment variable controlling the daemon log level.
// Accepted values: debug | info | warn | error (case-insensitive).
// Default: info. Pinned in docs/roadmap.md Phase 0.
const LevelEnv = "BIDAR_LOG_LEVEL"

// Options configures a daemon logger. Zero values are fine: Level zero is
// slog.LevelInfo, and a nil Out falls back to os.Stdout.
type Options struct {
	// Level is the minimum level that gets emitted.
	Level slog.Level
	// Out is where JSON lines are written; nil means os.Stdout.
	Out io.Writer
}

// New returns a slog.Logger writing JSON lines to opts.Out (or os.Stdout)
// at opts.Level (or info). JSON is the default because the daemon targets
// systemd, whose journal captures structured output natively and forwards
// it without line-splitting.
func New(opts Options) *slog.Logger {
	level := opts.Level
	if level == 0 {
		level = slog.LevelInfo
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	return slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level}))
}

// LevelFromEnv resolves the BIDAR_LOG_LEVEL value (default info) without
// constructing a logger — handy for callers that want to log the resolved
// level as a structured field.
func LevelFromEnv() (slog.Level, error) {
	return parseLevel(os.Getenv(LevelEnv))
}

// NewFromEnv builds the daemon logger from the environment: level from
// BIDAR_LOG_LEVEL (default info), output to stdout. An unknown level
// string fails loudly rather than silently logging nothing — a typo in a
// systemd Environment= line should be visible at startup, not months later.
func NewFromEnv() (*slog.Logger, error) {
	level, err := LevelFromEnv()
	if err != nil {
		return nil, err
	}
	return New(Options{Level: level}), nil
}

func parseLevel(raw string) (slog.Level, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return slog.LevelInfo, nil
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(raw)); err != nil {
		return 0, fmt.Errorf("%s: invalid level %q (use debug|info|warn|error): %w", LevelEnv, raw, err)
	}
	return level, nil
}
