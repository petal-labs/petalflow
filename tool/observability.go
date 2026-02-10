package tool

import (
	"sync"
	"time"
)

// ToolInvokeObservation captures one adapter invocation outcome.
type ToolInvokeObservation struct {
	ToolName   string
	Action     string
	Transport  TransportType
	Attempts   int
	DurationMS int64
	Success    bool
	ErrorCode  string
}

// ToolRetryObservation captures one retry attempt for an invocation.
type ToolRetryObservation struct {
	ToolName  string
	Action    string
	Transport TransportType
	Attempt   int
	ErrorCode string
}

// ToolHealthObservation captures one health-check outcome.
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
