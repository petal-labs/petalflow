// Package runtime provides the execution engine for PetalFlow workflow graphs.
package runtime

import (
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/memory"
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

	// EventRunResumed is emitted when a persisted run continues from a checkpoint.
	EventRunResumed EventKind = "run.resumed"

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

	// EventLLMCall is emitted when an LLM request is about to be sent to a provider.
	// Payload includes: provider, model, system_prompt, messages, tools, temperature, max_tokens.
	EventLLMCall EventKind = "llm.call"

	// EventLLMResponse is emitted when an LLM response is received from a provider.
	// Payload includes: provider, model, completion, tool_calls, usage (input_tokens,
	// output_tokens, total_tokens, cache_read_tokens, cache_creation_tokens),
	// stop_reason, request_id, ttft_ms, latency_ms.
	EventLLMResponse EventKind = "llm.response"

	// EventEdgeTransfer is emitted when data flows between nodes via an edge.
	// Payload includes: source_node, source_port, target_node, target_port,
	// data_size_bytes, data_preview.
	EventEdgeTransfer EventKind = "edge.transfer"

	// EventMemoryRecall is emitted when a node reads conversation memory and/or
	// knowledge from a memory provider. Payload includes: provider, namespace,
	// session_id, thread_id, status (ok|unavailable), history_count,
	// retrieved_count, latency_ms, error_class. Content is never included unless
	// the node explicitly opts in.
	EventMemoryRecall EventKind = "memory.recall"

	// EventMemoryStore is emitted when a node writes to a memory provider.
	// Payload includes: provider, namespace, session_id, thread_id, status,
	// message_count, stored_count, latency_ms, error_class.
	EventMemoryStore EventKind = "memory.store"

	// EventContextAssembled is emitted when recalled history and retrieved
	// artifacts are fitted to a token/artifact budget before a model call.
	// Payload is memory.AssemblyStats: token estimates, included/dropped
	// counts, compacted flag, and prompt_cache_key (a hash of the stable
	// request prefix, never the prefix itself).
	EventContextAssembled EventKind = "context.assembled"

	// EventContextCompacted is emitted in addition to context.assembled when
	// history had to be truncated or summarized to fit the budget. Payload
	// includes: history_dropped, summarized, max_tokens.
	EventContextCompacted EventKind = "context.compacted"
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

// WithScope records the memory scope's namespace, session_id, and thread_id
// on the event payload. The run ID is already the event's RunID and the tenant
// is deliberately not written to events; content is never included.
func (e Event) WithScope(scope memory.Scope) Event {
	if scope.Namespace != "" {
		e = e.WithPayload("namespace", scope.Namespace)
	}
	if scope.SessionID != "" {
		e = e.WithPayload("session_id", scope.SessionID)
	}
	if scope.ThreadID != "" {
		e = e.WithPayload("thread_id", scope.ThreadID)
	}
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
