package tool

import (
	"context"
	"sync"
	"time"
)

// Observation correlation: run/tenant context attached to invocation and
// retry observations so telemetry (spans and metrics) can correlate a
// tool call with its workflow run. Populated from the audit context the
// HTTP layers and runtime bridges attach to invocations; empty for
// daemon-level calls with no run context.
type ObservationCorrelation struct {
	TenantID string
	RunID    string
	NodeID   string
}

// CorrelationFromContext extracts observation correlation from the
// invocation context (set via ContextWithAuditContext).
func CorrelationFromContext(ctx context.Context) ObservationCorrelation {
	meta, ok := AuditContextFromContext(ctx)
	if !ok {
		return ObservationCorrelation{}
	}
	return ObservationCorrelation{
		TenantID: meta.TenantID,
		RunID:    meta.RunID,
		NodeID:   meta.NodeID,
	}
}

// ToolInvokeObservation captures one adapter invocation outcome.
type ToolInvokeObservation struct {
	ToolName   string
	Action     string
	Transport  TransportType
	Attempts   int
	DurationMS int64
	Success    bool
	ErrorCode  string
	// Correlation attaches run/tenant context when the invocation is
	// part of a workflow run.
	Correlation ObservationCorrelation
}

// ToolRetryObservation captures one retry attempt for an invocation.
type ToolRetryObservation struct {
	ToolName  string
	Action    string
	Transport TransportType
	Attempt   int
	ErrorCode string
	// Correlation attaches run/tenant context when the retry is part of
	// a workflow run.
	Correlation ObservationCorrelation
}

// ToolHealthObservation captures one health-check outcome. Health checks
// run from the background scheduler with no run context, so they carry
// no correlation.
type ToolHealthObservation struct {
	ToolName      string
	State         HealthState
	Status        Status
	FailureCount  int
	DurationMS    int64
	Interval      time.Duration
	ErrorCode     string
	PreviousState Status
}

// Observer receives tool-level observability events.
type Observer interface {
	ObserveInvoke(observation ToolInvokeObservation)
	ObserveRetry(observation ToolRetryObservation)
	ObserveHealth(observation ToolHealthObservation)
}

type noopObserver struct{}

func (noopObserver) ObserveInvoke(ToolInvokeObservation) {}
func (noopObserver) ObserveRetry(ToolRetryObservation)   {}
func (noopObserver) ObserveHealth(ToolHealthObservation) {}

var (
	observerMu     sync.RWMutex
	activeObserver Observer = noopObserver{}
)

// SetObserver sets the process-wide tool observability observer.
func SetObserver(observer Observer) {
	observerMu.Lock()
	defer observerMu.Unlock()
	if observer == nil {
		activeObserver = noopObserver{}
		return
	}
	activeObserver = observer
}

func emitInvokeObservation(observation ToolInvokeObservation) {
	observerMu.RLock()
	observer := activeObserver
	observerMu.RUnlock()
	observer.ObserveInvoke(observation)
}

func emitRetryObservation(observation ToolRetryObservation) {
	observerMu.RLock()
	observer := activeObserver
	observerMu.RUnlock()
	observer.ObserveRetry(observation)
}

func emitHealthObservation(observation ToolHealthObservation) {
	observerMu.RLock()
	observer := activeObserver
	observerMu.RUnlock()
	observer.ObserveHealth(observation)
}
