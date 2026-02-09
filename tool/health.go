package tool

import (
	"context"
	"time"
)

// HealthState indicates the current health of a registered tool.
type HealthState string

const (
	HealthUnknown   HealthState = "unknown"
	HealthHealthy   HealthState = "healthy"
	HealthUnhealthy HealthState = "unhealthy"
)

// HealthReport is a normalized health snapshot for a single tool.
type HealthReport struct {
	ToolName      string      `json:"tool_name"`
	State         HealthState `json:"state"`
	CheckedAt     time.Time   `json:"checked_at"`
	LatencyMS     int64       `json:"latency_ms,omitempty"`
	FailureCount  int         `json:"failure_count,omitempty"`
	ErrorMessage  string      `json:"error_message,omitempty"`
	DiagnosticRef string      `json:"diagnostic_ref,omitempty"`
}

// Prober checks the health status of a registration.
type Prober interface {
	Probe(ctx context.Context, reg Registration) (HealthReport, error)
}

// Monitor manages periodic health checks.
type Monitor interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}
