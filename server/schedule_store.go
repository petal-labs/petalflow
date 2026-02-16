package server

import (
	"context"
	"errors"
	"time"
)

var (
	ErrWorkflowScheduleExists   = errors.New("workflow schedule already exists")
	ErrWorkflowScheduleNotFound = errors.New("workflow schedule not found")
)

const (
	ScheduleRunStatusRunning        = "running"
	ScheduleRunStatusCompleted      = "completed"
	ScheduleRunStatusFailed         = "failed"
	ScheduleRunStatusSkippedOverlap = "skipped_overlap"
)

// WorkflowSchedule represents a persisted cron schedule for a workflow.
type WorkflowSchedule struct {
	ID         string         `json:"id"`
	WorkflowID string         `json:"workflow_id"`
	Cron       string         `json:"cron"`
	Enabled    bool           `json:"enabled"`
	Input      map[string]any `json:"input,omitempty"`
	Options    RunReqOptions  `json:"options,omitempty"`

	NextRunAt  time.Time  `json:"next_run_at"`
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	LastRunID  string     `json:"last_run_id,omitempty"`
	LastStatus string     `json:"last_status,omitempty"`
	LastError  string     `json:"last_error,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WorkflowScheduleStore provides CRUD + due scheduling operations.
type WorkflowScheduleStore interface {
	ListSchedules(ctx context.Context, workflowID string) ([]WorkflowSchedule, error)
	GetSchedule(ctx context.Context, workflowID, scheduleID string) (WorkflowSchedule, bool, error)
	CreateSchedule(ctx context.Context, schedule WorkflowSchedule) error
	UpdateSchedule(ctx context.Context, schedule WorkflowSchedule) error
	DeleteSchedule(ctx context.Context, workflowID, scheduleID string) error
	DeleteSchedulesByWorkflow(ctx context.Context, workflowID string) error
	ListDueSchedules(ctx context.Context, now time.Time, limit int) ([]WorkflowSchedule, error)
}
