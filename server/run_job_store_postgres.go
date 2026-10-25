package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/petal-labs/petalflow/internal/sqldialect"
)

// EnqueueRunJob inserts a new queued run job.
func (s *PostgresStore) EnqueueRunJob(ctx context.Context, job RunJob) error {
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = job.CreatedAt
	}
	if job.Status == "" {
		job.Status = RunJobStatusQueued
	}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = defaultRunJobMaxAttempts
	}
	if job.NextAttemptAt.IsZero() {
		job.NextAttemptAt = job.CreatedAt
	}
	if job.Trigger == "" {
		job.Trigger = RunJobTriggerAPI
	}

	inputJSON, err := marshalRunJobInput(job.Input)
	if err != nil {
		return fmt.Errorf("workflow postgres store: marshal run job input: %w", err)
	}
	optionsJSON, err := marshalRunJobOptions(job.Options)
	if err != nil {
		return fmt.Errorf("workflow postgres store: marshal run job options: %w", err)
	}

	_, err = s.db.ExecContext(ctx, sqldialect.Rebind(`
INSERT INTO run_jobs
	(id, workflow_id, tenant_id, trigger_type, schedule_id, input_json, options_json, status, attempt, max_attempts, last_error, next_attempt_at, lease_owner, lease_expires_at, run_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		job.ID,
		job.WorkflowID,
		job.TenantID,
		job.Trigger,
		nullIfEmpty(job.ScheduleID),
		inputJSON,
		optionsJSON,
		job.Status,
		job.Attempt,
		job.MaxAttempts,
		nullIfEmpty(job.LastError),
		formatJobTime(job.NextAttemptAt),
		nullIfEmpty(job.LeaseOwner),
		formatNullableJobTime(job.LeaseExpiresAt),
		nullIfEmpty(job.RunID),
		formatJobTime(job.CreatedAt),
		formatJobTime(job.UpdatedAt),
	)
	if err != nil {
		if isPostgresUniqueViolation(err) {
			return ErrRunJobExists
		}
		return fmt.Errorf("workflow postgres store: enqueue run job: %w", err)
	}
	return nil
}

// GetRunJob returns one run job by ID.
func (s *PostgresStore) GetRunJob(ctx context.Context, id string) (RunJob, error) {
	row := s.db.QueryRowContext(ctx, sqldialect.Rebind(
		`SELECT `+runJobSelectColumns+` FROM run_jobs WHERE id = ?`), id)
	job, err := scanRunJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RunJob{}, ErrRunJobNotFound
		}
		return RunJob{}, fmt.Errorf("workflow postgres store: get run job: %w", err)
	}
	return job, nil
}

// ClaimRunJobs atomically claims due jobs for the owner. The conditional
// UPDATE arbitrates concurrent claims; the loser matches no rows.
func (s *PostgresStore) ClaimRunJobs(ctx context.Context, owner string, now time.Time, leaseDuration time.Duration, limit int) ([]RunJob, error) {
	if limit <= 0 {
		limit = 1
	}
	nowText := formatJobTime(now)
	leaseUntil := now.Add(leaseDuration)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("workflow postgres store: begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, sqldialect.Rebind(`
SELECT `+runJobSelectColumns+` FROM run_jobs
WHERE `+runJobDueCondition+`
ORDER BY next_attempt_at ASC
LIMIT ?`),
		nowText, nowText, limit)
	if err != nil {
		return nil, fmt.Errorf("workflow postgres store: claim run jobs: %w", err)
	}

	var candidates []RunJob
	for rows.Next() {
		job, err := scanRunJob(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		candidates = append(candidates, job)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow postgres store: claim run jobs rows: %w", err)
	}

	claimed := make([]RunJob, 0, len(candidates))
	for _, candidate := range candidates {
		res, err := tx.ExecContext(ctx, sqldialect.Rebind(`
UPDATE run_jobs
SET status = 'running', lease_owner = ?, lease_expires_at = ?, attempt = attempt + 1, updated_at = ?
WHERE id = ? AND `+runJobDueCondition),
			owner,
			formatJobTime(leaseUntil),
			nowText,
			candidate.ID,
			nowText, nowText,
		)
		if err != nil {
			return nil, fmt.Errorf("workflow postgres store: claim run job %s: %w", candidate.ID, err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("workflow postgres store: claim run job %s affected: %w", candidate.ID, err)
		}
		if affected == 0 {
			continue
		}
		candidate.Status = RunJobStatusRunning
		candidate.LeaseOwner = owner
		expires := leaseUntil
		candidate.LeaseExpiresAt = &expires
		candidate.Attempt++
		candidate.UpdatedAt = now
		claimed = append(claimed, candidate)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("workflow postgres store: commit claim: %w", err)
	}
	return claimed, nil
}

// RenewRunJobLease extends a held lease.
func (s *PostgresStore) RenewRunJobLease(ctx context.Context, id, owner string, leaseUntil time.Time, now time.Time) error {
	res, err := s.db.ExecContext(ctx, sqldialect.Rebind(`
UPDATE run_jobs
SET lease_expires_at = ?, updated_at = ?
WHERE id = ? AND lease_owner = ? AND status = 'running'`),
		formatJobTime(leaseUntil), formatJobTime(now), id, owner)
	if err != nil {
		return fmt.Errorf("workflow postgres store: renew run job lease: %w", err)
	}
	return requireRunJobAffected(res, "renew run job lease")
}

// CompleteRunJob settles a job as completed.
func (s *PostgresStore) CompleteRunJob(ctx context.Context, id, runID string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, sqldialect.Rebind(`
UPDATE run_jobs
SET status = 'completed', run_id = ?, lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
WHERE id = ? AND status IN ('queued', 'running')`),
		nullIfEmpty(runID), formatJobTime(now), id)
	if err != nil {
		return fmt.Errorf("workflow postgres store: complete run job: %w", err)
	}
	return requireRunJobAffected(res, "complete run job")
}

// PauseRunJob marks a running job as paused.
func (s *PostgresStore) PauseRunJob(ctx context.Context, id, runID, lastError string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, sqldialect.Rebind(`
UPDATE run_jobs
SET status = 'paused', run_id = ?, last_error = ?, lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
WHERE id = ? AND status = 'running'`),
		nullIfEmpty(runID), nullIfEmpty(lastError), formatJobTime(now), id)
	if err != nil {
		return fmt.Errorf("workflow postgres store: pause run job: %w", err)
	}
	return requireRunJobAffected(res, "pause run job")
}

// FailRunJob records a failure, retrying with backoff or dead-lettering
// per the job's attempt budget. It returns the resulting status.
func (s *PostgresStore) FailRunJob(ctx context.Context, id, lastError string, nextAttemptAt time.Time, now time.Time) (string, error) {
	res, err := s.db.ExecContext(ctx, sqldialect.Rebind(`
UPDATE run_jobs
SET
	status = CASE WHEN attempt >= max_attempts THEN 'dead' ELSE 'queued' END,
	next_attempt_at = CASE WHEN attempt >= max_attempts THEN next_attempt_at ELSE ? END,
	last_error = ?,
	lease_owner = NULL,
	lease_expires_at = NULL,
	updated_at = ?
WHERE id = ? AND status = 'running'`),
		formatJobTime(nextAttemptAt), nullIfEmpty(lastError), formatJobTime(now), id)
	if err != nil {
		return "", fmt.Errorf("workflow postgres store: fail run job: %w", err)
	}
	if err := requireRunJobAffected(res, "fail run job"); err != nil {
		return "", err
	}

	job, err := s.GetRunJob(ctx, id)
	if err != nil {
		return "", err
	}
	return job.Status, nil
}

// DeadLetterRunJob terminally fails a running job without consuming
// the retry budget.
func (s *PostgresStore) DeadLetterRunJob(ctx context.Context, id, lastError string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, sqldialect.Rebind(`
UPDATE run_jobs
SET status = 'dead', last_error = ?, lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
WHERE id = ? AND status IN ('queued', 'running')`),
		nullIfEmpty(lastError), formatJobTime(now), id)
	if err != nil {
		return fmt.Errorf("workflow postgres store: dead-letter run job: %w", err)
	}
	return requireRunJobAffected(res, "dead-letter run job")
}

// CancelRunJob cancels a queued or running job.
func (s *PostgresStore) CancelRunJob(ctx context.Context, id string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, sqldialect.Rebind(`
UPDATE run_jobs
SET status = 'canceled', lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
WHERE id = ? AND status IN ('queued', 'running')`),
		formatJobTime(now), id)
	if err != nil {
		return fmt.Errorf("workflow postgres store: cancel run job: %w", err)
	}
	return requireRunJobAffected(res, "cancel run job")
}

// ResetRunJob requeues a terminal job for retry.
func (s *PostgresStore) ResetRunJob(ctx context.Context, id string, now time.Time, maxAttempts int) error {
	if maxAttempts <= 0 {
		maxAttempts = defaultRunJobMaxAttempts
	}
	res, err := s.db.ExecContext(ctx, sqldialect.Rebind(`
UPDATE run_jobs
SET status = 'queued', attempt = 0, max_attempts = ?, next_attempt_at = ?, last_error = '',
	lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
WHERE id = ? AND status IN ('failed', 'canceled', 'dead', 'paused')`),
		maxAttempts, formatJobTime(now), formatJobTime(now), id)
	if err != nil {
		return fmt.Errorf("workflow postgres store: reset run job: %w", err)
	}
	return requireRunJobAffected(res, "reset run job")
}

// PruneRunJobs deletes terminal jobs older than the cutoff.
func (s *PostgresStore) PruneRunJobs(ctx context.Context, olderThan time.Time, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, sqldialect.Rebind(`
DELETE FROM run_jobs
WHERE status IN ('completed', 'failed', 'canceled', 'dead') AND updated_at < ?`),
		formatJobTime(olderThan))
	if err != nil {
		return 0, fmt.Errorf("workflow postgres store: prune run jobs: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow postgres store: prune run jobs affected: %w", err)
	}
	return deleted, nil
}

func marshalRunJobOptions(options RunReqOptions) ([]byte, error) {
	data, err := json.Marshal(options)
	if err != nil {
		return nil, fmt.Errorf("workflow postgres store: marshal run job options: %w", err)
	}
	if len(data) == 0 {
		return []byte(`{}`), nil
	}
	return data, nil
}

var _ RunJobStore = (*PostgresStore)(nil)
