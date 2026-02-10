package tool

import (
	"context"
	"errors"
	"sync"
	"time"
)

const defaultHealthPollInterval = 5 * time.Second

// HealthEvent captures a scheduler-driven health evaluation result.
type HealthEvent struct {
	ToolName       string
	PreviousStatus Status
	Status         Status
	Report         HealthReport
	Error          error
}

// HealthEventHandler handles scheduler health events.
type HealthEventHandler func(event HealthEvent)

// HealthSchedulerConfig controls background health scheduling behavior.
type HealthSchedulerConfig struct {
	Service      *DaemonToolService
	PollInterval time.Duration
	Now          func() time.Time
	OnEvent      HealthEventHandler
}

// HealthScheduler periodically evaluates tool health based on per-tool intervals.
type HealthScheduler struct {
	service      *DaemonToolService
	pollInterval time.Duration
	now          func() time.Time
	onEvent      HealthEventHandler

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewHealthScheduler creates a health scheduler.
func NewHealthScheduler(cfg HealthSchedulerConfig) (*HealthScheduler, error) {
	if cfg.Service == nil {
		return nil, errors.New("tool: health scheduler service is nil")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultHealthPollInterval
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.OnEvent == nil {
		cfg.OnEvent = func(HealthEvent) {}
	}

	return &HealthScheduler{
		service:      cfg.Service,
		pollInterval: cfg.PollInterval,
		now:          cfg.Now,
		onEvent:      cfg.OnEvent,
	}, nil
}

// Start begins scheduler execution.
func (s *HealthScheduler) Start(ctx context.Context) error {
	if s == nil {
		return errors.New("tool: health scheduler is nil")
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

	return nil
}

// Stop terminates scheduler execution.
func (s *HealthScheduler) Stop(ctx context.Context) error {
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

// RunOnce performs one scheduling pass.
func (s *HealthScheduler) RunOnce(ctx context.Context) error {
	if s == nil || s.service == nil {
		return errors.New("tool: health scheduler service is nil")
	}

	regs, err := s.service.List(ctx, ToolListFilter{IncludeBuiltins: false})
	if err != nil {
		return err
	}

	now := s.now()
	for _, reg := range regs {
		if !reg.Enabled || reg.Status == StatusDisabled {
			continue
		}
		if !isHealthCheckDue(reg, now) {
			continue
		}

		previousStatus := reg.Status
		updated, report, err := s.service.Health(ctx, reg.Name)
		emitHealthObservation(ToolHealthObservation{
			ToolName:      reg.Name,
			State:         report.State,
			Status:        updated.Status,
			FailureCount:  report.FailureCount,
			DurationMS:    report.LatencyMS,
			Interval:      healthIntervalForRegistration(reg),
			ErrorCode:     toolErrorCode(err),
			PreviousState: previousStatus,
		})
		event := HealthEvent{
			ToolName:       reg.Name,
			PreviousStatus: previousStatus,
			Status:         updated.Status,
			Report:         report,
			Error:          err,
		}
		s.onEvent(event)
	}
	return nil
}

func isHealthCheckDue(reg Registration, now time.Time) bool {
	if reg.LastHealthCheck.IsZero() {
		return true
	}
	interval := healthIntervalForRegistration(reg)
	return !now.Before(reg.LastHealthCheck.Add(interval))
}

func healthIntervalForRegistration(reg Registration) time.Duration {
	if reg.Manifest.Health == nil || reg.Manifest.Health.IntervalSeconds <= 0 {
		return 30 * time.Second
	}
	return time.Duration(reg.Manifest.Health.IntervalSeconds) * time.Second
}
