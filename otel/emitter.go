package otel

import (
	"github.com/petal-labs/petalflow/runtime"
)

// EnrichEmitter wraps an EventEmitter with OpenTelemetry trace context.
// When events are emitted, it looks up the active span from the TracingHandler
// and populates the TraceID and SpanID fields on the event.
//
// For node-level events (where NodeID is set), the node span is checked first.
// If no node span is found, it falls back to the run-level span.
// When no span is active, the event passes through unchanged.
func EnrichEmitter(emit runtime.EventEmitter, tracing *TracingHandler) runtime.EventEmitter {
	return func(e runtime.Event) {
		// For node-level events, try node span first.
		if e.NodeID != "" {
			sc := tracing.ActiveSpanContext(e.RunID, e.NodeID)
			if sc.IsValid() {
				e.TraceID = sc.TraceID().String()
				e.SpanID = sc.SpanID().String()
			}
		}
		// Fallback to run-level span.
		if e.TraceID == "" && e.RunID != "" {
			sc := tracing.ActiveRunSpanContext(e.RunID)
			if sc.IsValid() {
				e.TraceID = sc.TraceID().String()
				e.SpanID = sc.SpanID().String()
			}
		}
		emit(e)
	}
}
