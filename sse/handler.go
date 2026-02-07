// Package sse provides a Server-Sent Events handler for streaming workflow
// execution events to HTTP clients. It supports replaying stored events and
// subscribing to live events via the event bus.
package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/runtime"
)

// HeartbeatInterval is the interval between SSE heartbeat comments.
const HeartbeatInterval = 15 * time.Second

// sseEvent is the JSON-serializable representation of a runtime event
// sent over the SSE stream.
type sseEvent struct {
	Kind      string         `json:"kind"`
	RunID     string         `json:"run_id"`
	NodeID    string         `json:"node_id,omitempty"`
	NodeKind  string         `json:"node_kind,omitempty"`
	Time      time.Time      `json:"time"`
	Attempt   int            `json:"attempt"`
	ElapsedMs int64          `json:"elapsed_ms"`
	Payload   map[string]any `json:"payload"`
	Seq       uint64         `json:"seq"`
	TraceID   string         `json:"trace_id,omitempty"`
	SpanID    string         `json:"span_id,omitempty"`
}

func toSSEEvent(e runtime.Event) sseEvent {
	return sseEvent{
		Kind:      string(e.Kind),
		RunID:     e.RunID,
		NodeID:    e.NodeID,
		NodeKind:  string(e.NodeKind),
		Time:      e.Time,
		Attempt:   e.Attempt,
		ElapsedMs: e.Elapsed.Milliseconds(),
		Payload:   e.Payload,
		Seq:       e.Seq,
		TraceID:   e.TraceID,
		SpanID:    e.SpanID,
	}
}

// SSEHandler serves an SSE stream of workflow execution events for a given run.
// It first replays stored events from the EventStore, then subscribes to live
// events via the EventBus. Duplicate events (by sequence number) are skipped.
//
// The handler expects a "run_id" path value (Go 1.22+ ServeMux) and an optional
// "after" query parameter to specify the last-seen sequence number.
//
// SSE format:
//
//	id: {seq}
//	event: {kind}
//	data: {json}
//
// A heartbeat comment ": ping\n\n" is sent every 15 seconds.
// The stream closes when a "run.finished" event is sent or the client disconnects.
type SSEHandler struct {
	store bus.EventStore
	bus   bus.EventBus
}

// NewSSEHandler creates a new SSEHandler with the given EventStore and EventBus.
func NewSSEHandler(store bus.EventStore, eb bus.EventBus) *SSEHandler {
	return &SSEHandler{
		store: store,
		bus:   eb,
	}
}

// ServeHTTP implements http.Handler. It streams events for the run identified
// by the "run_id" path value.
func (h *SSEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	if runID == "" {
		http.Error(w, "missing run_id", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Parse optional ?after= cursor.
	var afterSeq uint64
	if afterStr := r.URL.Query().Get("after"); afterStr != "" {
		parsed, err := strconv.ParseUint(afterStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid after parameter", http.StatusBadRequest)
			return
		}
		afterSeq = parsed
	}

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()

	// Subscribe to live events before replaying stored events, to avoid
	// missing events that arrive between replay and subscription.
	sub := h.bus.Subscribe(runID)
	defer sub.Close()

	// Phase 1: Replay stored events.
	var lastSeq uint64
	if afterSeq > 0 {
		lastSeq = afterSeq
	}

	finished, err := h.replayStored(ctx, w, flusher, runID, afterSeq, &lastSeq)
	if err != nil || finished {
		return
	}

	// Phase 2: Stream live events with heartbeat.
	h.streamLive(ctx, w, flusher, sub, &lastSeq)
}

// replayStored replays events from the store, writing them to the SSE stream.
// It returns true if a run.finished event was sent (stream should close) or
// if the context was cancelled.
func (h *SSEHandler) replayStored(
	ctx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	runID string,
	afterSeq uint64,
	lastSeq *uint64,
) (finished bool, err error) {
	events, err := h.store.List(ctx, runID, afterSeq, 0)
	if err != nil {
		return false, err
	}

	for _, evt := range events {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}

		if err := writeSSEEvent(w, evt); err != nil {
			return false, err
		}
		flusher.Flush()

		if evt.Seq > *lastSeq {
			*lastSeq = evt.Seq
		}

		if evt.Kind == runtime.EventRunFinished {
			return true, nil
		}
	}

	return false, nil
}

// streamLive streams events from the live subscription, deduplicating against
// already-sent sequence numbers.
func (h *SSEHandler) streamLive(
	ctx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	sub bus.Subscription,
	lastSeq *uint64,
) {
	heartbeat := time.NewTicker(HeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case evt, ok := <-sub.Events():
			if !ok {
				// Subscription closed.
				return
			}

			// Dedup: skip events already sent during replay.
			if evt.Seq <= *lastSeq {
				continue
			}

			if err := writeSSEEvent(w, evt); err != nil {
				return
			}
			flusher.Flush()

			*lastSeq = evt.Seq

			if evt.Kind == runtime.EventRunFinished {
				return
			}

		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSEEvent writes a single event in SSE format.
func writeSSEEvent(w http.ResponseWriter, evt runtime.Event) error {
	data, err := json.Marshal(toSSEEvent(evt))
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", evt.Seq, evt.Kind, data)
	return err
}
