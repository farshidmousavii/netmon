package store

// discovery_jobs persistence — the Postgres-backed poll queue. All state
// lives in the table, so the worker pool is resumable across restarts:
// a claimed job whose lease expires (crashed worker) becomes claimable
// again, and nothing lives only in memory.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/farshidmousavii/bidar/internal/domain"
)

// EnqueueJob schedules one unit of work. A target with an already-active
// job (queued or leased-running) is skipped rather than double-queued —
// the scheduler can call this every tick without piling up duplicates.
// Returns the job id and whether a row was actually inserted.
func (s *Store) EnqueueJob(ctx context.Context, provider, targetType string, targetID *int64, scheduledAt time.Time) (int64, bool, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO discovery_jobs (provider, target_type, target_id, scheduled_at)
		SELECT $1, $2, $3, $4
		WHERE NOT EXISTS (
			SELECT 1 FROM discovery_jobs
			WHERE provider = $1 AND target_type = $2 AND target_id IS NOT DISTINCT FROM $3
			  AND status IN ('queued', 'running')
		)
		RETURNING id`,
		provider, targetType, targetID, scheduledAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil // active job already exists
	}
	if err != nil {
		return 0, false, fmt.Errorf("enqueue %s job for %s/%v: %w", provider, targetType, targetID, err)
	}
	return id, true, nil
}

// ClaimDueJob leases the oldest due job to owner: queued-and-due jobs,
// plus running jobs whose lease expired (a crashed worker's leftovers).
// The single UPDATE ... WHERE id = (SELECT ... FOR UPDATE SKIP LOCKED)
// statement is the lease — concurrent workers never take the same job.
// Returns ErrNotFound when nothing is claimable.
func (s *Store) ClaimDueJob(ctx context.Context, owner string, lease time.Duration, now time.Time) (*domain.DiscoveryJob, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE discovery_jobs SET
		    status = 'running',
		    attempt = attempt + 1,
		    lease_owner = $1,
		    lease_expires_at = $2,
		    started_at = now()
		WHERE id = (
			SELECT id FROM discovery_jobs
			WHERE (status = 'queued' AND scheduled_at <= $3)
			   OR (status = 'running' AND lease_expires_at < $3)
			ORDER BY scheduled_at
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, provider, target_type, target_id, status, attempt,
		          lease_owner, lease_expires_at, scheduled_at, started_at,
		          finished_at, error_message`,
		owner, now.Add(lease), now)

	var j domain.DiscoveryJob
	err := row.Scan(&j.ID, &j.Provider, &j.TargetType, &j.TargetID, &j.Status,
		&j.Attempt, &j.LeaseOwner, &j.LeaseExpiresAt, &j.ScheduledAt,
		&j.StartedAt, &j.FinishedAt, &j.ErrorMessage)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound(err)
	}
	if err != nil {
		return nil, fmt.Errorf("claim discovery job: %w", err)
	}
	return &j, nil
}

// CompleteJob marks a leased job succeeded.
func (s *Store) CompleteJob(ctx context.Context, id int64, owner string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE discovery_jobs SET
		    status = 'succeeded', finished_at = $3, error_message = NULL,
		    lease_owner = NULL, lease_expires_at = NULL
		WHERE id = $1 AND status = 'running' AND lease_owner = $2`,
		id, owner, now)
	if err != nil {
		return fmt.Errorf("complete discovery job %d: %w", id, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("complete discovery job %d: %w", id, ErrNotFound)
	}
	return nil
}

// FinishJob closes a leased job with its terminal outcome: succeeded
// when errorMessage is nil, failed otherwise. Retries are not the job's
// business — the enqueue-side circuit breaker decides when the next job
// for the same target becomes due, reading the device's
// consecutive_failures. The attempt counter (incremented at claim time)
// still records every start, including lease-expiry reclaims.
func (s *Store) FinishJob(ctx context.Context, id int64, owner string, errorMessage *string, now time.Time) error {
	status := "succeeded"
	if errorMessage != nil {
		status = "failed"
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE discovery_jobs SET
		    status = $3, finished_at = $4, error_message = $5,
		    lease_owner = NULL, lease_expires_at = NULL
		WHERE id = $1 AND status = 'running' AND lease_owner = $2`,
		id, owner, status, now, errorMessage)
	if err != nil {
		return fmt.Errorf("finish discovery job %d: %w", id, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("finish discovery job %d: %w", id, ErrNotFound)
	}
	return nil
}
