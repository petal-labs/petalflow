package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	defaultRunWorkerPollInterval = time.Second
	defaultRunWorkerLease        = 60 * time.Second
	defaultRunWorkerConcurrency  = 4
	defaultRunWorkerMaxAttempts  = defaultRunJobMaxAttempts
	defaultRunWorkerBackoff      = 5 * time.Second
	defaultRunWorkerRetention    = 24 * time.Hour
	defaultRunWorkerPrune        = 10 * time.Minute
	defaultRunWorkerCancelPoll   = 2 * time.Second
	maxRunWorkerBackoff          = time.Hour
)

// RunWorkerConfig configures the asynchronous run job worker.
type RunWorkerConfig struct {
	Runner *Server
	Store  RunJobStore
	// WorkerID identifies this worker in job leases; empty generates one.
	WorkerID string
	// PollInterval is how often the worker looks for claimable jobs.
	PollInterval time.Duration
	// LeaseDuration is the exclusive claim window a worker holds on a
	// running job. Expired leases make crashed workers' jobs claimable
	// again by any replica.
	LeaseDuration time.Duration
	// Concurrency bounds jobs executed simultaneously (resource limit).
	Concurrency int
	// MaxAttempts is the default retry budget for enqueued jobs.
	MaxAttempts int
	// RetryBackoff is the base exponential backoff between attempts,
	// capped at one hour.
	RetryBackoff time.Duration
	// RetentionAge is how long terminal jobs are kept before pruning;
	// zero or negative disables pruning.
	RetentionAge time.Duration
	// PruneInterval is how often the retention pruner runs.
	PruneInterval time.Duration
	// Now overrides the clock for tests.
	Now func() time.Time
	// Logger receives worker lifecycle and failure logs.
	Logger *slog.Logger
}

// RunWorker claims queued run jobs and executes them with lease
// ownership, cancel polling, retry/backoff, and dead-letter handling.
// Expired-lease jobs (from crashed workers) are reclaimed automatically,
// so worker crashes produce recoverable, observable outcomes.
type RunWorker struct {
	runner     *Server
	store      RunJobStore
	worker     string
	poll       time.Duration
	lease      time.Duration
	workers    int
	backoff    time.Duration
	retain     time.Duration
	prune      time.Duration
	cancelPoll time.Duration
	now        func() time.Time
	logger     *slog.Logger

	inFlight atomic.Int64

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	wg     sync.WaitGroup
}

// NewRunWorker creates an asynchronous run worker.
func NewRunWorker(cfg RunWorkerConfig) (*RunWorker, error) {
	if cfg.Runner == nil {
		return nil, errors.New("run worker runner is nil")
	}
	if cfg.Store == nil {
		return nil, errors.New("run worker store is nil")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultRunWorkerPollInterval
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = defaultRunWorkerLease
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = defaultRunWorkerConcurrency
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultRunWorkerMaxAttempts
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = defaultRunWorkerBackoff
	}
	if cfg.RetentionAge <= 0 {
		cfg.RetentionAge = defaultRunWorkerRetention
	}
	if cfg.PruneInterval <= 0 {
		cfg.PruneInterval = defaultRunWorkerPrune
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	workerID := strings.TrimSpace(cfg.WorkerID)
	if workerID == "" {
		workerID = "worker-" + uuid.New().String()
	}

	return &RunWorker{
		runner:     cfg.Runner,
		store:      cfg.Store,
		worker:     workerID,
		poll:       cfg.PollInterval,
		lease:      cfg.LeaseDuration,
		workers:    cfg.Concurrency,
		backoff:    cfg.RetryBackoff,
		retain:     cfg.RetentionAge,
		prune:      cfg.PruneInterval,
		cancelPoll: defaultRunWorkerCancelPoll,
		now:        cfg.Now,
		logger:     cfg.Logger,
	}, nil
}

// Start starts background claiming and execution.
func (w *RunWorker) Start(ctx context.Context) error {
	if w == nil {
		return errors.New("run worker is nil")
	}

	w.mu.Lock()
	if w.cancel != nil {
		w.mu.Unlock()
		return nil
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	w.cancel = cancel
	w.done = done
	w.mu.Unlock()

	go func() {
		defer close(done)
		w.loop(loopCtx)
	}()
	return nil
}

// Stop cancels the claim loop and waits for in-flight job executions.
func (w *RunWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}

	w.mu.Lock()
	cancel := w.cancel
	done := w.done
	w.cancel = nil
	w.done = nil
	w.mu.Unlock()

	if cancel == nil {
		return nil
	}
	cancel()

	waitDone := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(waitDone)
	}()

	// The loop exits promptly after cancel; settle only when in-flight
	// executions have finished too.
	<-done
	select {
	case <-waitDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// RunOnce performs a single claim-and-dispatch pass. It returns after
// dispatching; execution continues in the background.
func (w *RunWorker) RunOnce(ctx context.Context) error {
	if w == nil || w.store == nil || w.runner == nil {
		return errors.New("run worker is not configured")
	}

	now := w.now().UTC()
	available := w.workers - int(w.inFlight.Load())
	if available <= 0 {
		return nil
	}

	claimed, err := w.store.ClaimRunJobs(ctx, w.worker, now, w.lease, available)
	if err != nil {
		return fmt.Errorf("run worker claim: %w", err)
	}

	for _, job := range claimed {
		w.wg.Add(1)
		w.inFlight.Add(1)
		go w.executeJob(ctx, job)
	}
	return nil
}

func (w *RunWorker) loop(ctx context.Context) {
	pruneTicker := time.NewTicker(w.prune)
	defer pruneTicker.Stop()

	_ = w.RunOnce(ctx)
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				w.logger.Error("run worker claim pass failed", "worker", w.worker, "error", err)
			}
		case <-pruneTicker.C:
			w.pruneTerminalJobs(ctx)
		}
	}
}

func (w *RunWorker) pruneTerminalJobs(ctx context.Context) {
	cutoff := w.now().UTC().Add(-w.retain)
	deleted, err := w.store.PruneRunJobs(ctx, cutoff, w.now().UTC())
	if err != nil && !errors.Is(err, context.Canceled) {
		w.logger.Error("run job retention prune failed", "worker", w.worker, "error", err)
		return
	}
	if deleted > 0 {
		w.logger.Info("pruned terminal run jobs", "worker", w.worker, "deleted", deleted)
	}
}

// executeJob runs one claimed job end to end: cancel polling, lease
// renewal, execution, and settlement.
func (w *RunWorker) executeJob(ctx context.Context, job RunJob) {
	defer w.wg.Done()
	defer w.inFlight.Add(-1)

	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stopRenew := w.renewLeaseLoop(jobCtx, job.ID, cancel)
	stopPoll := w.cancelPollLoop(jobCtx, job.ID, cancel)
	defer close(stopRenew)
	defer close(stopPoll)

	// A canceled job may have lost the race before execution started.
	if w.jobCanceled(job.ID) {
		w.settleCanceled(job.ID)
		return
	}

	plan, err := w.runner.planWorkflowRun(jobCtx, job.WorkflowID, RunRequest{
		Input:   cloneMapAny(job.Input),
		Options: job.Options,
	})
	if err != nil {
		w.handlePlanError(job, err)
		return
	}

	// The job ID is the durable run ID so polling, streaming, cancel,
	// and retry endpoints work before, during, and after execution.
	plan.env.Trace.RunID = job.ID

	resp, runErr := w.runner.executeWorkflowRunSync(jobCtx, job.WorkflowID, plan, nil)
	now := w.now().UTC()

	if runErr != nil {
		w.handleRunError(jobCtx, job, runErr, resp, now)
		return
	}

	if err := w.store.CompleteRunJob(ctx, job.ID, resp.RunID, now); err != nil {
		w.logger.Error("complete run job", "worker", w.worker, "job", job.ID, "error", err)
	}
}

// handlePlanError settles a failed plan: deterministic client errors
// (missing workflow, invalid input, hydration failure) dead-letter the
// job because retrying cannot fix them, while server-side errors
// (store hiccups, registry failures) retry with backoff.
func (w *RunWorker) handlePlanError(job RunJob, err error) {
	var apiErr *runAPIError
	if errors.As(err, &apiErr) && apiErr.Status >= 500 {
		nextAttempt := w.now().UTC().Add(w.backoffDelay(job.Attempt))
		status, failErr := w.store.FailRunJob(context.Background(), job.ID, planErrorReason(err), nextAttempt, w.now().UTC())
		if failErr != nil {
			w.logger.Error("fail run job after plan error", "worker", w.worker, "job", job.ID, "error", failErr)
			return
		}
		w.logger.Warn("run job plan failed; scheduled for retry",
			"worker", w.worker, "job", job.ID, "attempt", job.Attempt, "error", planErrorReason(err), "retry_at", nextAttempt)
		if status == RunJobStatusDead {
			w.logger.Error("run job dead-lettered after repeated failures", "worker", w.worker, "job", job.ID)
		}
		return
	}
	w.terminalFailure(job, planErrorReason(err))
}

// handleRunError classifies a failed execution into pause, cancel,
// retryable failure, or terminal failure.
func (w *RunWorker) handleRunError(ctx context.Context, job RunJob, runErr error, resp RunResponse, now time.Time) {
	var apiErr *runAPIError
	if errors.As(runErr, &apiErr) && apiErr.Status == statusAcceptedPaused {
		if err := w.store.PauseRunJob(ctx, job.ID, resp.RunID, apiErr.Message, now); err != nil {
			w.logger.Error("pause run job", "worker", w.worker, "job", job.ID, "error", err)
		}
		return
	}

	// An explicit cancel request must not be retried. Status lookups use
	// a fresh context because the run context may already be cancelled.
	if ctx.Err() != nil && w.jobCanceled(job.ID) {
		w.settleCanceled(job.ID)
		return
	}

	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		if ctx.Err() != nil {
			// Worker shutdown or lease loss: leave the job leased; the
			// lease expires and any replica can resume from checkpoint.
			w.logger.Warn("run job interrupted by worker shutdown; lease will expire for reassignment",
				"worker", w.worker, "job", job.ID)
			return
		}
	}

	nextAttempt := now.Add(w.backoffDelay(job.Attempt))
	status, err := w.store.FailRunJob(ctx, job.ID, runErr.Error(), nextAttempt, now)
	if err != nil {
		w.logger.Error("fail run job", "worker", w.worker, "job", job.ID, "error", err)
		return
	}
	logAttrs := []any{"worker", w.worker, "job", job.ID, "attempt", job.Attempt, "error", runErr.Error()}
	if status == RunJobStatusDead {
		w.logger.Error("run job dead-lettered after repeated failures", logAttrs...)
		return
	}
	w.logger.Warn("run job failed; scheduled for retry", append(logAttrs, "retry_at", nextAttempt)...)
}

func (w *RunWorker) settleCanceled(jobID string) {
	if err := w.store.CancelRunJob(context.Background(), jobID, w.now().UTC()); err != nil && !errors.Is(err, ErrRunJobConflict) {
		w.logger.Error("cancel run job", "worker", w.worker, "job", jobID, "error", err)
	}
}

func (w *RunWorker) terminalFailure(job RunJob, reason string) {
	if err := w.store.DeadLetterRunJob(context.Background(), job.ID, reason, w.now().UTC()); err != nil && !errors.Is(err, ErrRunJobConflict) {
		w.logger.Error("dead-letter run job", "worker", w.worker, "job", job.ID, "error", err)
		return
	}
	w.logger.Error("run job dead-lettered for deterministic failure", "worker", w.worker, "job", job.ID, "reason", reason)
}

func (w *RunWorker) backoffDelay(attempt int) time.Duration {
	delay := w.backoff * time.Duration(1<<max(0, attempt-1))
	if delay > maxRunWorkerBackoff {
		delay = maxRunWorkerBackoff
	}
	return delay
}

// renewLeaseLoop periodically extends the job lease while it executes.
// A lost lease (conflict) cancels execution because another replica owns
// the job now.
func (w *RunWorker) renewLeaseLoop(ctx context.Context, jobID string, cancel context.CancelFunc) chan struct{} {
	stop := make(chan struct{})
	renewEvery := w.lease / 3
	if renewEvery <= 0 {
		renewEvery = time.Second
	}
	go func() {
		ticker := time.NewTicker(renewEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				now := w.now().UTC()
				err := w.store.RenewRunJobLease(ctx, jobID, w.worker, now.Add(w.lease), now)
				if err != nil {
					w.logger.Warn("run job lease renewal failed; cancelling execution for reassignment",
						"worker", w.worker, "job", jobID, "error", err)
					cancel()
					return
				}
			}
		}
	}()
	return stop
}

// cancelPollLoop watches for cancel requests raised on any replica and
// cancels the local execution context when observed.
func (w *RunWorker) cancelPollLoop(ctx context.Context, jobID string, cancel context.CancelFunc) chan struct{} {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(w.cancelPoll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				if w.jobCanceled(jobID) {
					cancel()
					return
				}
			}
		}
	}()
	return stop
}

// jobCanceled reports whether a cancel was requested for the job, either
// through the run job record or the durable run record. Lookups use a
// fresh context so cancel detection keeps working when the run context
// is already cancelled (shutdown, deadline, or cancel itself).
func (w *RunWorker) jobCanceled(jobID string) bool {
	ctx := context.Background()
	job, err := w.store.GetRunJob(ctx, jobID)
	if err == nil && job.Status == RunJobStatusCanceled {
		return true
	}
	if w.runner.runStore != nil {
		record, err := w.runner.runStore.Get(ctx, jobID)
		if err == nil && record != nil && record.CancelRequested {
			return true
		}
	}
	return false
}

// planErrorReason strips HTTP detail from plan errors for dead-letter records.
func planErrorReason(err error) string {
	var apiErr *runAPIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("%s: %s", apiErr.Code, apiErr.Message)
	}
	return err.Error()
}

// statusAcceptedPaused mirrors http.StatusAccepted for run-pause handling.
const statusAcceptedPaused = 202
