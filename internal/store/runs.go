package store

import (
	"context"
	"fmt"
	"time"
)

// RecordProviderRun appends one row to provider_runs — the simple run log
// every scheduled provider cycle writes (Phase 1; the lease-based
// discovery_jobs queue is Phase 2). target_type/target_id stay NULL for
// the Phase 1 scheduled runs, which are provider-level.
func (s *Store) RecordProviderRun(ctx context.Context, provider, status string, itemsFound int, errorMessage *string, startedAt, finishedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO provider_runs (provider, status, items_found, error_message, started_at, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		provider, status, itemsFound, errorMessage, startedAt, finishedAt)
	if err != nil {
		return fmt.Errorf("record provider run: %w", err)
	}
	return nil
}
