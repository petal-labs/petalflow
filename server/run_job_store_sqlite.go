package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// jobTimeLayout is a fixed-width RFC 3339 UTC format so timestamp
// comparisons in SQL ORDER BY / WHERE clauses are lexicographically
// correct (RFC 3339Nano trims trailing zeros, which breaks ordering).
const jobTimeLayout = "2006-01-02T15:04:05.000000Z07:00"

func formatJobTime(t time.Time) string {
	return t.UTC().Format(jobTimeLayout)
}

func parseJobTime(value string) (time.Time, error) {
	if t, err := time.Parse(jobTimeLayout, value); err == nil {
		return t, nil
	}
	// Tolerate values written in other RFC 3339 layouts (e.g. by
	// external tooling).
	return time.Parse(time.RFC3339Nano, value)
}

func formatNullableJobTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return formatJobTime(*t)
}

func nullableJobTimeString(value sql.NullString) (*time.Time, error) {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil, nil
	}
	parsed, err := parseJobTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

const runJobSelectColumns = `id, workflow_id, tenant_id, trigger_type, schedule_id, input_json, options_json, status, attempt, max_attempts, last_error, next_attempt_at, lease_owner, lease_expires_at, run_id, created_at, updated_at`

const runJobsSQLiteSchema = `
CREATE TABLE IF NOT EXISTS run_jobs (
	id TEXT PRIMARY KEY,
	workflow_id TEXT NOT NULL,
	tenant_id TEXT NOT NULL DEFAULT '',
	trigger_type TEXT NOT NULL DEFAULT 'api',
	schedule_id TEXT,
	input_json BLOB NOT NULL,
	options_json BLOB NOT NULL,
	status TEXT NOT NULL,
	attempt INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 3,
	last_error TEXT,
	next_attempt_at TEXT NOT NULL,
	lease_owner TEXT,
	lease_expires_at TEXT,
	run_id TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_run_jobs_claim
ON run_jobs(status, next_attempt_at);

CREATE INDEX IF NOT EXISTS idx_run_jobs_updated
ON run_jobs(updated_at);`

// claim condition matching due queued jobs and expired leases. The two
// placeholders both bind `now` (formatted uniformly); they are spelled as
// separate ?s because SQLite does not support named args here.
const runJobDueCondition = `(status = 'queued' AND next_attempt_at <= ?)
OR (status = 'running' AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?)`

func marshalRunJobInput(input map[string]any) ([]byte, error) {
	if input == nil {
		return []byte(`{}`), nil
	}
	return json.Marshal(input)
}

func unmarshalRunJobInput(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("unmarshal run job input: %w", err)
	}
	if input == nil {
		return map[string]any{}, nil
	}
	return input, nil
}

// EnqueueRunJob inserts a new queued run job.
func (s *SQLiteStore) EnqueueRunJob(ctx context.Context, job RunJob) error {
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
		return fmt.Errorf("workflow sqlite store marshal run job input: %w", err)
	}
	optionsJSON, err := json.Marshal(job.Options)
	if err != nil {
		return fmt.Errorf("workflow sqlite store marshal run job options: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO run_jobs
	(id, workflow_id, tenant_id, trigger_type, schedule_id, input_json, options_json, status, attempt, max_attempts, last_error, next_attempt_at, lease_owner, lease_expires_at, run_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		if strings.Contains(err.Error(), "UNIQUE constraint failed: run_jobs.id") {
			return ErrRunJobExists
		}
		return fmt.Errorf("workflow sqlite store enqueue run job: %w", err)
	}
	return nil
}

// GetRunJob returns one run job by ID.
func (s *SQLiteStore) GetRunJob(ctx context.Context, id string) (RunJob, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+runJobSelectColumns+` FROM run_jobs WHERE id = ?`, id)
	job, err := scanRunJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RunJob{}, ErrRunJobNotFound
		}
		return RunJob{}, fmt.Errorf("workflow sqlite store get run job: %w", err)
	}
	return job, nil
}

type runJobScanner interface {
	Scan(dest ...any) error
}

func scanRunJob(scanner runJobScanner) (RunJob, error) {
	var (
		job            RunJob
		tenantID       string
		triggerType    string
		scheduleID     sql.NullString
		inputRaw       []byte
		optionsRaw     []byte
		status         string
		attempt        int
		maxAttempts    int
		lastError      sql.NullString
		nextAttemptAt  string
		leaseOwner     sql.NullString
		leaseExpiresAt sql.NullString
		runID          sql.NullString
		createdAt      string
		updatedAt      string
	)
	if err := scanner.Scan(
		&job.ID,
		&job.WorkflowID,
		&tenantID,
		&triggerType,
		&scheduleID,
		&inputRaw,
		&optionsRaw,
		&status,
		&attempt,
		&maxAttempts,
		&lastError,
		&nextAttemptAt,
		&leaseOwner,
		&leaseExpiresAt,
		&runID,
		&createdAt,
		&updatedAt,
	); err != nil {
		return RunJob{}, err
	}

	input, err := unmarshalRunJobInput(inputRaw)
	if err != nil {
		return RunJob{}, fmt.Errorf("workflow store scan run job: %w", err)
	}
	var options RunReqOptions
	if err := json.Unmarshal(optionsRaw, &options); err != nil {
		return RunJob{}, fmt.Errorf("workflow store scan run job options: %w", err)
	}

	nextAttempt, err := parseJobTime(nextAttemptAt)
	if err != nil {
		return RunJob{}, fmt.Errorf("workflow store parse run job next_attempt_at: %w", err)
	}
	leaseExpires, err := nullableJobTimeString(leaseExpiresAt)
	if err != nil {
		return RunJob{}, fmt.Errorf("workflow store parse run job lease_expires_at: %w", err)
	}
	created, err := parseJobTime(createdAt)
	if err != nil {
		return RunJob{}, fmt.Errorf("workflow store parse run job created_at: %w", err)
	}
	updated, err := parseJobTime(updatedAt)
	if err != nil {
		return RunJob{}, fmt.Errorf("workflow store parse run job updated_at: %w", err)
	}

	job.TenantID = tenantID
	job.Trigger = triggerType
	job.ScheduleID = scheduleID.String
	job.Input = input
	job.Options = options
	job.Status = status
	job.Attempt = attempt
	job.MaxAttempts = maxAttempts
	job.LastError = lastError.String
	job.NextAttemptAt = nextAttempt
	job.LeaseOwner = leaseOwner.String
	job.LeaseExpiresAt = leaseExpires
	job.RunID = runID.String
	job.CreatedAt = created
	job.UpdatedAt = updated
	return job, nil
}

// ClaimRunJobs atomically claims due jobs for the owner.
func (s *SQLiteStore) ClaimRunJobs(ctx context.Context, owner string, now time.Time, leaseDuration time.Duration, limit int) ([]RunJob, error) {
	if limit <= 0 {
		limit = 1
	}
	nowText := formatJobTime(now)
	leaseUntil := now.Add(leaseDuration)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("workflow sqlite store begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
SELECT `+runJobSelectColumns+` FROM run_jobs
WHERE `+runJobDueCondition+`
ORDER BY next_attempt_at ASC
LIMIT ?`,
		nowText, nowText, limit)
	if err != nil {
		return nil, fmt.Errorf("workflow sqlite store claim run jobs: %w", err)
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
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("workflow sqlite store claim run jobs rows: %w", err)
	}
	_ = rows.Close()

	claimed := make([]RunJob, 0, len(candidates))
	for _, candidate := range candidates {
		res, err := tx.ExecContext(ctx, `
UPDATE run_jobs
SET status = 'running', lease_owner = ?, lease_expires_at = ?, attempt = attempt + 1, updated_at = ?
WHERE id = ? AND `+runJobDueCondition,
			owner,
			formatJobTime(leaseUntil),
			nowText,
			candidate.ID,
			nowText, nowText,
		)
		if err != nil {
			return nil, fmt.Errorf("workflow sqlite store claim run job %s: %w", candidate.ID, err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("workflow sqlite store claim run job %s affected: %w", candidate.ID, err)
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
		return nil, fmt.Errorf("workflow sqlite store commit claim: %w", err)
	}
	return claimed, nil
}

// RenewRunJobLease extends a held lease.
func (s *SQLiteStore) RenewRunJobLease(ctx context.Context, id, owner string, leaseUntil time.Time, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE run_jobs
SET lease_expires_at = ?, updated_at = ?
WHERE id = ? AND lease_owner = ? AND status = 'running'`,
		formatJobTime(leaseUntil), formatJobTime(now), id, owner)
	if err != nil {
		return fmt.Errorf("workflow sqlite store renew run job lease: %w", err)
	}
	return requireRunJobAffected(res, "renew run job lease")
}

// CompleteRunJob settles a job as completed.
func (s *SQLiteStore) CompleteRunJob(ctx context.Context, id, runID string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE run_jobs
SET status = 'completed', run_id = ?, lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
WHERE id = ? AND status IN ('queued', 'running')`,
		nullIfEmpty(runID), formatJobTime(now), id)
	if err != nil {
		return fmt.Errorf("workflow sqlite store complete run job: %w", err)
	}
	return requireRunJobAffected(res, "complete run job")
}

// PauseRunJob marks a running job as paused.
func (s *SQLiteStore) PauseRunJob(ctx context.Context, id, runID, lastError string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE run_jobs
SET status = 'paused', run_id = ?, last_error = ?, lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
WHERE id = ? AND status = 'running'`,
		nullIfEmpty(runID), nullIfEmpty(lastError), formatJobTime(now), id)
	if err != nil {
		return fmt.Errorf("workflow sqlite store pause run job: %w", err)
	}
	return requireRunJobAffected(res, "pause run job")
}

// FailRunJob records a failure, retrying with backoff or dead-lettering
// per the job's attempt budget. It returns the resulting status.
func (s *SQLiteStore) FailRunJob(ctx context.Context, id, lastError string, nextAttemptAt time.Time, now time.Time) (string, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE run_jobs
SET
	status = CASE WHEN attempt >= max_attempts THEN 'dead' ELSE 'queued' END,
	next_attempt_at = CASE WHEN attempt >= max_attempts THEN next_attempt_at ELSE ? END,
	last_error = ?,
	lease_owner = NULL,
	lease_expires_at = NULL,
	updated_at = ?
WHERE id = ? AND status = 'running'`,
		formatJobTime(nextAttemptAt), nullIfEmpty(lastError), formatJobTime(now), id)
	if err != nil {
		return "", fmt.Errorf("workflow sqlite store fail run job: %w", err)
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
func (s *SQLiteStore) DeadLetterRunJob(ctx context.Context, id, lastError string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE run_jobs
SET status = 'dead', last_error = ?, lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
WHERE id = ? AND status IN ('queued', 'running')`,
		nullIfEmpty(lastError), formatJobTime(now), id)
	if err != nil {
		return fmt.Errorf("workflow sqlite store dead-letter run job: %w", err)
	}
	return requireRunJobAffected(res, "dead-letter run job")
}

// CancelRunJob cancels a queued or running job.
func (s *SQLiteStore) CancelRunJob(ctx context.Context, id string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE run_jobs
SET status = 'canceled', lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
WHERE id = ? AND status IN ('queued', 'running')`,
		formatJobTime(now), id)
	if err != nil {
		return fmt.Errorf("workflow sqlite store cancel run job: %w", err)
	}
	return requireRunJobAffected(res, "cancel run job")
}

// ResetRunJob requeues a terminal job for retry.
func (s *SQLiteStore) ResetRunJob(ctx context.Context, id string, now time.Time, maxAttempts int) error {
	if maxAttempts <= 0 {
		maxAttempts = defaultRunJobMaxAttempts
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE run_jobs
SET status = 'queued', attempt = 0, max_attempts = ?, next_attempt_at = ?, last_error = '',
	lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
WHERE id = ? AND status IN ('failed', 'canceled', 'dead', 'paused')`,
		maxAttempts, formatJobTime(now), formatJobTime(now), id)
	if err != nil {
		return fmt.Errorf("workflow sqlite store reset run job: %w", err)
	}
	return requireRunJobAffected(res, "reset run job")
}

// PruneRunJobs deletes terminal jobs older than the cutoff.
func (s *SQLiteStore) PruneRunJobs(ctx context.Context, olderThan time.Time, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
DELETE FROM run_jobs
WHERE status IN ('completed', 'failed', 'canceled', 'dead') AND updated_at < ?`,
		formatJobTime(olderThan))
	if err != nil {
		return 0, fmt.Errorf("workflow sqlite store prune run jobs: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow sqlite store prune run jobs affected: %w", err)
	}
	return deleted, nil
}

func requireRunJobAffected(res sql.Result, action string) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workflow store %s affected rows: %w", action, err)
	}
	if affected == 0 {
		return ErrRunJobConflict
	}
	return nil
}

// --- Distributed schedule lease (SQLite) ---

// ClaimDueSchedules atomically claims due, unleased schedules for the
// owner, setting a lease that expires after leaseDuration. Schedules
// already leased to any owner (including this one) within the lease
// window are not returned, which prevents two scheduler replicas from
// executing the same due schedule concurrently.
func (s *SQLiteStore) ClaimDueSchedules(ctx context.Context, owner string, now time.Time, leaseDuration time.Duration, limit int) ([]WorkflowSchedule, error) {
	if limit <= 0 {
		limit = 1
	}
	nowText := now.UTC().Format(time.RFC3339Nano)
	leaseUntil := now.Add(leaseDuration).UTC().Format(time.RFC3339Nano)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("workflow sqlite store begin schedule claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
SELECT id FROM workflow_schedules
WHERE enabled = 1
	AND next_run_at <= ?
	AND (lease_expires_at IS NULL OR lease_expires_at <= ?)
ORDER BY next_run_at ASC
LIMIT ?`,
		nowText, nowText, limit)
	if err != nil {
		return nil, fmt.Errorf("workflow sqlite store claim due schedules: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("workflow sqlite store scan due schedule id: %w", err)
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow sqlite store claim due schedules rows: %w", err)
	}

	claimed := make([]WorkflowSchedule, 0, len(ids))
	for _, id := range ids {
		res, err := tx.ExecContext(ctx, `
UPDATE workflow_schedules
SET lease_owner = ?, lease_expires_at = ?, updated_at = ?
WHERE id = ? AND enabled = 1
	AND (lease_expires_at IS NULL OR lease_expires_at <= ?)`,
			owner, leaseUntil, nowText, id, nowText)
		if err != nil {
			return nil, fmt.Errorf("workflow sqlite store claim schedule %s: %w", id, err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("workflow sqlite store claim schedule %s affected: %w", id, err)
		}
		if affected == 0 {
			continue
		}
		schedule, err := scanScheduleInTx(tx, id)
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, schedule)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("workflow sqlite store commit schedule claim: %w", err)
	}
	return claimed, nil
}

func scanScheduleInTx(tx *sql.Tx, id string) (WorkflowSchedule, error) {
	row := tx.QueryRow(`SELECT `+scheduleSelectColumns+` FROM workflow_schedules WHERE id = ?`, id)
	schedule, err := scanWorkflowSchedule(row)
	if err != nil {
		return WorkflowSchedule{}, fmt.Errorf("workflow sqlite store scan claimed schedule: %w", err)
	}
	return schedule, nil
}
