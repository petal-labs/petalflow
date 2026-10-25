package server

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrRunJobNotFound is returned when a run job does not exist.
	ErrRunJobNotFound = errors.New("run job not found")
	// ErrRunJobExists is returned when enqueueing an ID that already exists.
	ErrRunJobExists = errors.New("run job already exists")
	// ErrRunJobConflict is returned when a compare-and-set transition
	// loses a race, e.g. another worker claimed or settled the job first.
	ErrRunJobConflict = errors.New("run job state changed concurrently")
)

// defaultRunJobMaxAttempts bounds retries before dead-lettering when a
// job or its submitter does not specify a budget.
const defaultRunJobMaxAttempts = 3

// Run job statuses.
const (
	RunJobStatusQueued    = "queued"
	RunJobStatusRunning   = "running"
	RunJobStatusPaused    = "paused"
	RunJobStatusCompleted = "completed"
	RunJobStatusFailed    = "failed"
	RunJobStatusCanceled  = "canceled"
	RunJobStatusDead      = "dead"
)

// RunJobTrigger values identify what enqueued a run job.
const (
	RunJobTriggerAPI      = "api"
	RunJobTriggerSchedule = "schedule"
)

// RunJob is a durable, leasable unit of asynchronous workflow execution.
// A client submits a run job without holding an HTTP request open; workers
// claim jobs with a lease, execute the workflow, and settle the job with
// terminal status. Expired leases make crashed workers' jobs claimable
// again, and repeated failures dead-letter the job.
type RunJob struct {
	ID string `json:"id"`
	// ID doubles as the durable run ID so status, events, cancel, and
	// retry endpoints work uniformly before, during, and after execution.
	WorkflowID string         `json:"workflow_id"`
	TenantID   string         `json:"tenant_id,omitempty"`
	Trigger    string         `json:"trigger,omitempty"`
	ScheduleID string         `json:"schedule_id,omitempty"`
	Input      map[string]any `json:"input,omitempty"`
	Options    RunReqOptions  `json:"options,omitempty"`

	Status      string `json:"status"`
	Attempt     int    `json:"attempt"`
	MaxAttempts int    `json:"max_attempts,omitempty"`
	LastError   string `json:"last_error,omitempty"`

	// NextAttemptAt gates when a queued job may be claimed; retry
	// backoff pushes it into the future.
	NextAttemptAt time.Time `json:"next_attempt_at"`

	// Lease identifies the worker currently executing the job.
	LeaseOwner     string     `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`

	RunID string `json:"run_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// isRunJobTerminal reports whether a job status needs no further work.
// Paused (human-input) jobs are not terminal: they can still be canceled
// or retried, and they are excluded from retention pruning because the
// human response may arrive arbitrarily later.
func isRunJobTerminal(status string) bool {
	switch status {
	case RunJobStatusCompleted, RunJobStatusFailed, RunJobStatusCanceled, RunJobStatusDead:
		return true
	default:
		return false
	}
}

// RunJobStore persists asynchronous run jobs with claim/lease semantics.
//
// Claims are atomic across processes: two workers calling ClaimRunJobs for
// the same due job must never both observe a successful claim. Implementations
// must recover jobs whose lease expired (crashed worker) by making them
// claimable again. All transitions are compare-and-set on (id, status, lease).
type RunJobStore interface {
	// EnqueueRunJob inserts a new queued job. ErrRunJobExists is
	// returned if the ID already exists.
	EnqueueRunJob(ctx context.Context, job RunJob) error
	// GetRunJob returns one job by ID.
	GetRunJob(ctx context.Context, id string) (RunJob, error)
	// ClaimRunJobs atomically claims up to limit due jobs for the owner:
	// queued jobs with NextAttemptAt <= now, plus running jobs whose
	// lease expired before now (crashed-worker recovery). Claimed jobs
	// get status=running, LeaseOwner=owner, and a fresh lease.
	ClaimRunJobs(ctx context.Context, owner string, now time.Time, leaseDuration time.Duration, limit int) ([]RunJob, error)
	// RenewRunJobLease extends the lease of a running job held by owner.
	RenewRunJobLease(ctx context.Context, id, owner string, leaseUntil time.Time, now time.Time) error
	// CompleteRunJob settles a job as completed.
	CompleteRunJob(ctx context.Context, id, runID string, now time.Time) error
	// PauseRunJob marks a job paused (e.g. waiting on human input).
	PauseRunJob(ctx context.Context, id, runID, lastError string, now time.Time) error
	// FailRunJob records a failure: jobs below MaxAttempts go back to
	// queued with NextAttemptAt for backoff; jobs at the attempt cap go
	// to the dead-letter status. It returns the resulting status.
	FailRunJob(ctx context.Context, id, lastError string, nextAttemptAt time.Time, now time.Time) (string, error)
	// DeadLetterRunJob terminally fails a running job without consuming
	// the retry budget (used for deterministic, non-retryable errors).
	DeadLetterRunJob(ctx context.Context, id, lastError string, now time.Time) error
	// CancelRunJob cancels a queued or running job.
	CancelRunJob(ctx context.Context, id string, now time.Time) error
	// ResetRunJob requeues a terminal (failed/canceled/dead) job for
	// retry with attempt reset.
	ResetRunJob(ctx context.Context, id string, now time.Time, maxAttempts int) error
	// PruneRunJobs deletes terminal jobs not updated since olderThan and
	// returns the number of deleted rows.
	PruneRunJobs(ctx context.Context, olderThan time.Time, now time.Time) (int64, error)
}
