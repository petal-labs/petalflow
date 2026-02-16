package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultWorkflowSchedulePollInterval = 5 * time.Second
	defaultWorkflowScheduleBatchLimit   = 100
)

// WorkflowSchedulerConfig configures the background workflow schedule runner.
type WorkflowSchedulerConfig struct {
	Runner       *Server
	Store        WorkflowScheduleStore
	PollInterval time.Duration
	BatchLimit   int
	Now          func() time.Time
	Logger       *slog.Logger
}

// WorkflowScheduler periodically executes due workflow schedules.
type WorkflowScheduler struct {
	runner       *Server
	store        WorkflowScheduleStore
	pollInterval time.Duration
	batchLimit   int
	now          func() time.Time
	logger       *slog.Logger

	mu     sync.Mutex
	active map[string]struct{}
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
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return &WorkflowScheduler{
		runner:       cfg.Runner,
		store:        cfg.Store,
		pollInterval: cfg.PollInterval,
		batchLimit:   cfg.BatchLimit,
		now:          cfg.Now,
		logger:       cfg.Logger,
		active:       map[string]struct{}{},
	}, nil
}

// Start starts background polling.
func (s *WorkflowScheduler) Start(ctx context.Context) error {
	if s == nil {
		return errors.New("workflow scheduler is nil")
	}

	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return nil
	}
	loopCtx, cancel := context.WithCancel(context.Background())
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

	_ = ctx
	return nil
}

// Stop stops background polling.
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
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RunOnce executes a single scheduler pass.
func (s *WorkflowScheduler) RunOnce(ctx context.Context) error {
	if s == nil || s.store == nil || s.runner == nil {
		return errors.New("workflow scheduler is not configured")
	}

	now := s.now().UTC()
	dueSchedules, err := s.store.ListDueSchedules(ctx, now, s.batchLimit)
	if err != nil {
		return err
	}

	for _, schedule := range dueSchedules {
		s.processDueSchedule(ctx, schedule, now)
	}
	return nil
}

func (s *WorkflowScheduler) processDueSchedule(ctx context.Context, schedule WorkflowSchedule, now time.Time) {
	if !schedule.Enabled {
		return
	}

	if s.isScheduleActive(schedule.ID) {
		s.markSkippedOverlap(ctx, schedule, now)
		return
	}

	nextRunAt, err := nextCronRunUTC(schedule.Cron, now)
	if err != nil {
		s.markScheduleFailure(ctx, schedule, now, fmt.Errorf("invalid cron expression: %w", err))
		return
	}

	schedule.NextRunAt = nextRunAt
	schedule.LastStatus = ScheduleRunStatusRunning
	schedule.LastError = ""
	schedule.UpdatedAt = now
	if err := s.store.UpdateSchedule(ctx, schedule); err != nil {
		s.logger.Error("update schedule before run", "schedule_id", schedule.ID, "workflow_id", schedule.WorkflowID, "error", err)
		return
	}

	s.markScheduleActive(schedule.ID)
	go s.runSchedule(schedule, now)
}

func (s *WorkflowScheduler) runSchedule(schedule WorkflowSchedule, scheduledAt time.Time) {
	defer s.unmarkScheduleActive(schedule.ID)

	runReq := RunRequest{
		Input:   cloneMapAny(schedule.Input),
		Options: schedule.Options,
	}
	runReq.Options.Stream = false

	resp, runErr := s.runner.runScheduledWorkflow(context.Background(), schedule.WorkflowID, runReq, scheduledRunMetadata{
		ScheduleID:  schedule.ID,
		WorkflowID:  schedule.WorkflowID,
		ScheduledAt: scheduledAt,
	})

	finish := s.now().UTC()
	latest, found, err := s.store.GetSchedule(context.Background(), schedule.WorkflowID, schedule.ID)
	if err != nil {
		s.logger.Error("load schedule after run", "schedule_id", schedule.ID, "workflow_id", schedule.WorkflowID, "error", err)
		return
	}
	if !found {
		return
	}

	latest.UpdatedAt = finish
	latest.LastRunAt = &finish
	if runErr != nil {
		latest.LastStatus = ScheduleRunStatusFailed
		latest.LastError = runErr.Error()
	} else {
		latest.LastStatus = ScheduleRunStatusCompleted
		latest.LastError = ""
		latest.LastRunID = resp.RunID
	}

	if err := s.store.UpdateSchedule(context.Background(), latest); err != nil {
		s.logger.Error("persist schedule run result", "schedule_id", schedule.ID, "workflow_id", schedule.WorkflowID, "error", err)
	}
}

func (s *WorkflowScheduler) markSkippedOverlap(ctx context.Context, schedule WorkflowSchedule, now time.Time) {
	nextRunAt, err := nextCronRunUTC(schedule.Cron, now)
	if err != nil {
		s.markScheduleFailure(ctx, schedule, now, fmt.Errorf("invalid cron expression: %w", err))
		return
	}

	schedule.NextRunAt = nextRunAt
	schedule.LastStatus = ScheduleRunStatusSkippedOverlap
	schedule.LastError = "skipped because prior scheduled run is still active"
	schedule.UpdatedAt = now
	if err := s.store.UpdateSchedule(ctx, schedule); err != nil {
		s.logger.Error("persist overlap skip", "schedule_id", schedule.ID, "workflow_id", schedule.WorkflowID, "error", err)
	}
}

func (s *WorkflowScheduler) markScheduleFailure(ctx context.Context, schedule WorkflowSchedule, now time.Time, runErr error) {
	nextRunAt, nextErr := nextCronRunUTC(schedule.Cron, now)
	if nextErr == nil {
		schedule.NextRunAt = nextRunAt
	}
	schedule.LastStatus = ScheduleRunStatusFailed
	schedule.LastError = runErr.Error()
	schedule.UpdatedAt = now
	if err := s.store.UpdateSchedule(ctx, schedule); err != nil {
		s.logger.Error("persist schedule failure", "schedule_id", schedule.ID, "workflow_id", schedule.WorkflowID, "error", err)
	}
}

func (s *WorkflowScheduler) isScheduleActive(scheduleID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.active[scheduleID]
	return ok
}

func (s *WorkflowScheduler) markScheduleActive(scheduleID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[scheduleID] = struct{}{}
}

func (s *WorkflowScheduler) unmarkScheduleActive(scheduleID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, scheduleID)
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
