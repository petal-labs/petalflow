package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultWorkflowSchedulePollInterval = 5 * time.Second
	defaultWorkflowScheduleBatchLimit   = 100
	defaultScheduleLeaseDuration        = 60 * time.Second
	defaultScheduleRetryBackoff         = 30 * time.Second
	defaultScheduleMaxConsecutiveFails  = 5
	maxScheduleRetryBackoff             = time.Hour
)

// WorkflowSchedulerConfig configures the background workflow schedule runner.
type WorkflowSchedulerConfig struct {
	Runner       *Server
	Store        WorkflowScheduleStore
	PollInterval time.Duration
	BatchLimit   int
	// SchedulerID identifies this replica in schedule leases; empty
	// generates one.
	SchedulerID string
	// LeaseDuration is the exclusive claim window a replica holds over a
	// due schedule while executing it. Expired leases let any replica
	// claim the schedule again after a crash.
	LeaseDuration time.Duration
	// RetryBackoff is the base exponential backoff applied when a
	// scheduled run fails, capped at one hour.
	RetryBackoff time.Duration
	// MaxConsecutiveFailures disables the schedule (dead-letter) after
	// this many consecutive failed runs.
	MaxConsecutiveFailures int
	Now                    func() time.Time
	Logger                 *slog.Logger
}

// WorkflowScheduler periodically executes due workflow schedules.
//
// Due schedules are claimed with a durable lease, so two replicas polling
// the same store can never execute the same due schedule concurrently.
// Missed runs (while replicas were down) coalesce into a single backfill
// run at the next claim: the schedule executes once and NextRunAt advances
// to the next cron occurrence. Failed runs retry with exponential backoff
// and dead-letter (disable) the schedule after MaxConsecutiveFailures.
type WorkflowScheduler struct {
	runner       *Server
	store        WorkflowScheduleStore
	pollInterval time.Duration
	batchLimit   int
	schedulerID  string
	lease        time.Duration
	backoff      time.Duration
	maxFailures  int
	now          func() time.Time
	logger       *slog.Logger

	wg     sync.WaitGroup
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewWorkflowScheduler creates a workflow scheduler instance.
func NewWorkflowScheduler(cfg WorkflowSchedulerConfig) (*WorkflowScheduler, error) {
	if cfg.Runner == nil {
		return nil, errors.New("workflow scheduler runner is nil")
	}
	if cfg.Store == nil {
		return nil, errors.New("workflow scheduler store is nil")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultWorkflowSchedulePollInterval
	}
	if cfg.BatchLimit <= 0 {
		cfg.BatchLimit = defaultWorkflowScheduleBatchLimit
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = defaultScheduleLeaseDuration
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = defaultScheduleRetryBackoff
	}
	if cfg.MaxConsecutiveFailures <= 0 {
		cfg.MaxConsecutiveFailures = defaultScheduleMaxConsecutiveFails
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	schedulerID := ""
	if cfg.SchedulerID != "" {
		schedulerID = cfg.SchedulerID
	} else {
		schedulerID = "scheduler-" + uuid.New().String()
	}

	return &WorkflowScheduler{
		runner:       cfg.Runner,
		store:        cfg.Store,
		pollInterval: cfg.PollInterval,
		batchLimit:   cfg.BatchLimit,
		schedulerID:  schedulerID,
		lease:        cfg.LeaseDuration,
		backoff:      cfg.RetryBackoff,
		maxFailures:  cfg.MaxConsecutiveFailures,
		now:          cfg.Now,
		logger:       cfg.Logger,
	}, nil
}

// Start starts background polling. The loop stops when either the passed
// context is cancelled or Stop is called; Stop additionally waits for
// in-flight scheduled runs to settle.
func (s *WorkflowScheduler) Start(ctx context.Context) error {
	if s == nil {
		return errors.New("workflow scheduler is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return nil
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.cancel = cancel
	s.done = done
	s.mu.Unlock()

	go func() {
		defer close(done)
		_ = s.RunOnce(loopCtx)
		ticker := time.NewTicker(s.pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				_ = s.RunOnce(loopCtx)
			}
		}
	}()

	return nil
}

// Stop stops background polling and waits for in-flight scheduled runs.
func (s *WorkflowScheduler) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()

	if cancel == nil {
		return nil
	}
	cancel()

	if done == nil {
		return nil
	}
	waitDone := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(waitDone)
	}()

	// The loop exits promptly after cancel; settle only when in-flight
	// scheduled runs have finished too.
	<-done
	select {
	case <-waitDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// RunOnce executes a single scheduler pass: claim due schedules with a
// lease, advance their NextRunAt, and dispatch runs in the background.
func (s *WorkflowScheduler) RunOnce(ctx context.Context) error {
	if s == nil || s.store == nil || s.runner == nil {
		return errors.New("workflow scheduler is not configured")
	}

	now := s.now().UTC()
	claimed, err := s.store.ClaimDueSchedules(ctx, s.schedulerID, now, s.lease, s.batchLimit)
	if err != nil {
		return err
	}

	for _, schedule := range claimed {
		if !s.advanceClaimedSchedule(ctx, schedule, now) {
			continue
		}
		s.wg.Add(1)
		go s.runSchedule(schedule, now)
	}
	return nil
}

// advanceClaimedSchedule applies backfill semantics: one execution covers
// all occurrences missed since the last run, and NextRunAt advances to
// the next cron occurrence after now.
func (s *WorkflowScheduler) advanceClaimedSchedule(ctx context.Context, schedule WorkflowSchedule, now time.Time) bool {
	nextRunAt, err := nextCronRunUTC(schedule.Cron, now)
	if err != nil {
		s.disableSchedule(ctx, schedule, now, fmt.Errorf("invalid cron expression: %w", err))
		return false
	}

	schedule.NextRunAt = nextRunAt
	schedule.LastStatus = ScheduleRunStatusRunning
	schedule.LastError = ""
	schedule.UpdatedAt = now
	if err := s.store.UpdateSchedule(ctx, schedule); err != nil {
		s.logger.Error("update claimed schedule before run", "scheduler", s.schedulerID, "schedule_id", schedule.ID, "workflow_id", schedule.WorkflowID, "error", err)
		return false
	}
	return true
}

func (s *WorkflowScheduler) runSchedule(schedule WorkflowSchedule, scheduledAt time.Time) {
	defer s.wg.Done()

	runReq := RunRequest{
		Input:   cloneMapAny(schedule.Input),
		Options: schedule.Options,
	}
	runReq.Options.Stream = false

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp, runErr := s.runner.runScheduledWorkflow(runCtx, schedule.WorkflowID, runReq, scheduledRunMetadata{
		ScheduleID:  schedule.ID,
		WorkflowID:  schedule.WorkflowID,
		ScheduledAt: scheduledAt,
	})

	s.settleSchedule(schedule, runErr, resp, cancel)
}

// settleSchedule persists the run outcome: success resets the failure
// counter; failure increments it and applies retry backoff; exceeding
// the failure budget dead-letters (disables) the schedule.
func (s *WorkflowScheduler) settleSchedule(schedule WorkflowSchedule, runErr error, resp RunResponse, cancel context.CancelFunc) {
	finish := s.now().UTC()
	latest, found, err := s.store.GetSchedule(context.Background(), schedule.WorkflowID, schedule.ID)
	if err != nil {
		s.logger.Error("load schedule after run", "scheduler", s.schedulerID, "schedule_id", schedule.ID, "workflow_id", schedule.WorkflowID, "error", err)
		return
	}
	if !found {
		return
	}

	latest.UpdatedAt = finish
	latest.LastRunAt = &finish
	latest.LeaseOwner = ""
	latest.LeaseExpiresAt = nil
	if runErr != nil {
		latest.LastStatus = ScheduleRunStatusFailed
		latest.LastError = runErr.Error()
		latest.ConsecutiveFailures++
		if latest.ConsecutiveFailures >= s.maxFailures {
			latest.Enabled = false
			latest.LastStatus = ScheduleRunStatusDeadLetter
			latest.LastError = fmt.Sprintf("%s (disabled after %d consecutive failures)", runErr.Error(), latest.ConsecutiveFailures)
		} else {
			// Retry with backoff instead of waiting for the next cron
			// occurrence, capped so a flaky workflow cannot spin hot.
			backoff := s.backoff * time.Duration(1<<min(latest.ConsecutiveFailures-1, 6))
			if backoff > maxScheduleRetryBackoff {
				backoff = maxScheduleRetryBackoff
			}
			latest.NextRunAt = finish.Add(backoff)
		}
	} else {
		latest.LastStatus = ScheduleRunStatusCompleted
		latest.LastError = ""
		latest.LastRunID = resp.RunID
		latest.ConsecutiveFailures = 0
	}

	if err := s.store.UpdateSchedule(context.Background(), latest); err != nil {
		s.logger.Error("persist schedule run result", "scheduler", s.schedulerID, "schedule_id", schedule.ID, "workflow_id", schedule.WorkflowID, "error", err)
	}
}

// disableSchedule dead-letters a schedule that can never run again in
// its current form (e.g. an invalid cron expression).
func (s *WorkflowScheduler) disableSchedule(ctx context.Context, schedule WorkflowSchedule, now time.Time, runErr error) {
	schedule.Enabled = false
	schedule.LastStatus = ScheduleRunStatusDeadLetter
	schedule.LastError = runErr.Error()
	schedule.UpdatedAt = now
	if err := s.store.UpdateSchedule(ctx, schedule); err != nil {
		s.logger.Error("persist schedule dead-letter", "scheduler", s.schedulerID, "schedule_id", schedule.ID, "workflow_id", schedule.WorkflowID, "error", err)
	}
}

func cloneMapAny(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
