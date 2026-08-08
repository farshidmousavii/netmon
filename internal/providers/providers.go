// Package providers defines the shared contract every Phase 1+ evidence
// source implements (docs/coding-standards.md): one package per provider
// (ad, arp, dhcp, icmpsweep, ...), providers never import each other, and
// one provider failing must never crash the daemon or block the others —
// Run returns an error and the scheduler decides what to do with it.
package providers

import (
	"context"
	"time"
)

// Provider is one isolated evidence source. Constructed with explicit
// dependencies (config, DB pool, logger) — no package-level state.
type Provider interface {
	// Name identifies the provider, e.g. "ad" (used in provider_runs).
	Name() string
	// Run performs one poll/collection cycle and reports what it found.
	Run(ctx context.Context) (Result, error)
	// Health reports the outcome of the most recent Run.
	Health() Health
}

// Result summarizes one Run.
type Result struct {
	// ItemsFound is the number of observations produced (e.g. computer
	// objects pulled from AD).
	ItemsFound int
}

// Health describes the last Run outcome, for visibility and circuit
// breaking (docs/architecture.md §Phase 2).
type Health struct {
	Healthy   bool
	LastRunAt time.Time
	LastError string
}
