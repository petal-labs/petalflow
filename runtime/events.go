// Package runtime provides the execution engine for PetalFlow workflow graphs.
package runtime

import (
	"time"

	"github.com/petal-labs/petalflow/core"
)

// EventKind identifies the type of event emitted by the runtime.
type EventKind string

const (
	// EventRunStarted is emitted when a graph run begins.
	EventRunStarted EventKind = "run.started"

	// EventNodeStarted is emitted when a node begins execution.
	EventNodeStarted EventKind = "node.started"

	// EventNodeOutput is emitted when a node produces output.
	// This is optional and used for streaming intermediate results.
	EventNodeOutput EventKind = "node.output"

	// EventNodeFailed is emitted when a node encounters an error.
	EventNodeFailed EventKind = "node.failed"

	// EventNodeFinished is emitted when a node completes successfully.
	EventNodeFinished EventKind = "node.finished"

	// EventRouteDecision is emitted when a router node makes a routing decision.
	EventRouteDecision EventKind = "route.decision"

	// EventRunFinished is emitted when a graph run completes.
	EventRunFinished EventKind = "run.finished"

	// EventStepPaused is emitted when execution pauses at a step point.
	EventStepPaused EventKind = "step.paused"

	// EventStepResumed is emitted when execution resumes after a step.
	EventStepResumed EventKind = "step.resumed"

	// EventStepSkipped is emitted when a node is skipped via StepActionSkipNode.
	EventStepSkipped EventKind = "step.skipped"

	// EventStepAborted is emitted when execution is aborted via StepActionAbort.
	EventStepAborted EventKind = "step.aborted"

	// EventToolCall is emitted when a tool invocation begins.
	EventToolCall EventKind = "tool.call"

	// EventToolResult is emitted when a tool invocation completes.
	EventToolResult EventKind = "tool.result"

	// EventNodeOutputDelta is emitted for incremental streaming output from a node.
	EventNodeOutputDelta EventKind = "node.output.delta"

	// EventNodeOutputFinal is emitted for the final consolidated output from a node.
	EventNodeOutputFinal EventKind = "node.output.final"

	// EventNodeOutputPreview is emitted for a preview of node output before completion.
	EventNodeOutputPreview EventKind = "node.output.preview"

	// EventRunSnapshot is emitted to capture a point-in-time snapshot of run state.
	EventRunSnapshot EventKind = "run.snapshot"
)

// String returns the string representation of the EventKind.
func (k EventKind) String() string {
	return string(k)
}

// Event is a structured, streamable record of what happened during execution.
// Events should be kept small; large data should be stored via RunStore
// or referenced via artifact URIs.
type Event struct {
	// Kind identifies the event type.
	Kind EventKind

	// RunID is the unique identifier for this run.
	RunID string

	// NodeID is the node that produced this event (empty for run-level events).
	NodeID string

	// NodeKind is the kind of node (empty for run-level events).
	NodeKind core.NodeKind

	// Time is when the event occurred.
	Time time.Time

	// Attempt is the attempt number (1-indexed) for retry scenarios.
	Attempt int

	// Elapsed is the duration since the run or node started.
	Elapsed time.Duration

	// Payload contains event-specific data.
	// Keep this small; prefer references to stored envelopes/records.
	Payload map[string]any

	// Seq is a monotonic sequence number per run (1-indexed).
	Seq uint64

	// TraceID is the OpenTelemetry trace ID (hex-encoded, empty when OTel inactive).
	TraceID string

	// SpanID is the OpenTelemetry span ID (hex-encoded, empty when OTel inactive).
	SpanID string
}

// NewEvent creates a new event with the current timestamp.
func NewEvent(kind EventKind, runID string) Event {
	return Event{
		Kind:    kind,
		RunID:   runID,
		Time:    time.Now(),
		Attempt: 1,
		Payload: make(map[string]any),
	}
}

// WithNode sets the node information on the event.
func (e Event) WithNode(nodeID string, nodeKind core.NodeKind) Event {
	e.NodeID = nodeID
	e.NodeKind = nodeKind
	return e
}

// WithAttempt sets the attempt number on the event.
func (e Event) WithAttempt(attempt int) Event {
	e.Attempt = attempt
	return e
}

// WithElapsed sets the elapsed duration on the event.
func (e Event) WithElapsed(elapsed time.Duration) Event {
	e.Elapsed = elapsed
	return e
}

// WithPayload adds a key-value pair to the event payload.
func (e Event) WithPayload(key string, value any) Event {
	if e.Payload == nil {
		e.Payload = make(map[string]any)
	}
	e.Payload[key] = value
	return e
}

// EventEmitter is a function type for emitting events.
// The runtime provides an emitter to nodes that need to emit intermediate events.
type EventEmitter func(Event)

// EventEmitterDecorator wraps an emitter to add cross-cutting behavior.
// Typical uses include enriching emitted events (for example with trace metadata).
type EventEmitterDecorator func(EventEmitter) EventEmitter

// EventPublisher can publish events to external subscribers.
// This interface is satisfied by bus.EventBus, allowing the runtime
// to distribute events without importing the bus package directly.
type EventPublisher interface {
	Publish(event Event)
}

// EventHandler is a function type for handling events.
// Implementations can log, store, or forward events as needed.
type EventHandler func(Event)

// MultiEventHandler combines multiple handlers into one.
func MultiEventHandler(handlers ...EventHandler) EventHandler {
	return func(e Event) {
		for _, h := range handlers {
			if h != nil {
				h(e)
			}
		}
	}
}

// ChannelEventHandler returns a handler that sends events to a channel.
// The channel should have sufficient buffer to avoid blocking.
// Events are dropped if the channel is full or closed.
func ChannelEventHandler(ch chan<- Event) EventHandler {
	return func(e Event) {
		select {
		case ch <- e:
		default:
			// Drop event if channel is full
		}
	}
}
