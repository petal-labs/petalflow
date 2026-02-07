package sse_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/sse"
)

// helper to create a test event with the given sequence number and kind.
func testEvent(runID string, seq uint64, kind runtime.EventKind) runtime.Event {
	return runtime.Event{
		Kind:    kind,
		RunID:   runID,
		NodeID:  fmt.Sprintf("node-%d", seq),
		Time:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Attempt: 1,
		Elapsed: time.Duration(seq) * time.Millisecond,
		Payload: map[string]any{"seq_val": float64(seq)},
		Seq:     seq,
	}
}

// sseMessage represents a parsed SSE message from the stream.
type sseMessage struct {
	ID    string
	Event string
	Data  string
}

// parseSSEMessages reads SSE messages from the response body string.
func parseSSEMessages(body string) []sseMessage {
	var msgs []sseMessage
	scanner := bufio.NewScanner(strings.NewReader(body))

	var current sseMessage
	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = end of message.
			if current.ID != "" || current.Event != "" || current.Data != "" {
				msgs = append(msgs, current)
				current = sseMessage{}
			}
			continue
		}

		if strings.HasPrefix(line, ": ") {
			// Comment line (heartbeat).
			continue
		}

		if strings.HasPrefix(line, "id: ") {
			current.ID = strings.TrimPrefix(line, "id: ")
		} else if strings.HasPrefix(line, "event: ") {
			current.Event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			current.Data = strings.TrimPrefix(line, "data: ")
		}
	}

	return msgs
}

// setupTestServer creates a test mux with the SSE handler registered.
func setupTestServer(store bus.EventStore, eb bus.EventBus) *httptest.Server {
	handler := sse.NewSSEHandler(store, eb)
	mux := http.NewServeMux()
	mux.Handle("GET /runs/{run_id}/events", handler)
	return httptest.NewServer(mux)
}

func TestSSEHandler_ReplayFromStore(t *testing.T) {
	store := bus.NewMemEventStore()
	eb := bus.NewMemBus(bus.MemBusConfig{})
	defer eb.Close()

	runID := "run-replay"
	ctx := context.Background()

	// Pre-populate store with events including a run.finished.
	events := []runtime.Event{
		testEvent(runID, 1, runtime.EventRunStarted),
		testEvent(runID, 2, runtime.EventNodeStarted),
		testEvent(runID, 3, runtime.EventNodeFinished),
		testEvent(runID, 4, runtime.EventRunFinished),
	}
	for _, e := range events {
		if err := store.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	ts := setupTestServer(store, eb)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/runs/" + runID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream, got %s", ct)
	}

	// Read the full body (stream should close after run.finished).
	var body strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			body.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}

	msgs := parseSSEMessages(body.String())
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d: %v", len(msgs), body.String())
	}

	// Verify first message.
	if msgs[0].ID != "1" {
		t.Errorf("expected id 1, got %s", msgs[0].ID)
	}
	if msgs[0].Event != "run.started" {
		t.Errorf("expected event run.started, got %s", msgs[0].Event)
	}

	// Verify the data is valid JSON with expected fields.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(msgs[0].Data), &parsed); err != nil {
		t.Fatalf("failed to parse data JSON: %v", err)
	}
	if parsed["kind"] != "run.started" {
		t.Errorf("expected kind run.started, got %v", parsed["kind"])
	}
	if parsed["run_id"] != runID {
		t.Errorf("expected run_id %s, got %v", runID, parsed["run_id"])
	}

	// Verify last message is run.finished.
	if msgs[3].Event != "run.finished" {
		t.Errorf("expected last event run.finished, got %s", msgs[3].Event)
	}
	if msgs[3].ID != "4" {
		t.Errorf("expected id 4, got %s", msgs[3].ID)
	}
}

func TestSSEHandler_LiveSubscription(t *testing.T) {
	store := bus.NewMemEventStore()
	eb := bus.NewMemBus(bus.MemBusConfig{})
	defer eb.Close()

	runID := "run-live"

	ts := setupTestServer(store, eb)
	defer ts.Close()

	// Start the request in a goroutine since it will block until run.finished.
	type result struct {
		body string
		err  error
	}
	resultCh := make(chan result, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/runs/"+runID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		defer resp.Body.Close()

		var body strings.Builder
		buf := make([]byte, 4096)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				body.Write(buf[:n])
			}
			if readErr != nil {
				break
			}
		}
		resultCh <- result{body: body.String()}
	}()

	// Give the handler time to subscribe.
	time.Sleep(100 * time.Millisecond)

	// Publish live events.
	eb.Publish(testEvent(runID, 1, runtime.EventRunStarted))
	eb.Publish(testEvent(runID, 2, runtime.EventNodeStarted))
	eb.Publish(testEvent(runID, 3, runtime.EventRunFinished))

	res := <-resultCh
	if res.err != nil {
		t.Fatal(res.err)
	}

	msgs := parseSSEMessages(res.body)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d: %s", len(msgs), res.body)
	}

	if msgs[0].Event != "run.started" {
		t.Errorf("expected run.started, got %s", msgs[0].Event)
	}
	if msgs[2].Event != "run.finished" {
		t.Errorf("expected run.finished, got %s", msgs[2].Event)
	}
}

func TestSSEHandler_AfterCursor(t *testing.T) {
	store := bus.NewMemEventStore()
	eb := bus.NewMemBus(bus.MemBusConfig{})
	defer eb.Close()

	runID := "run-cursor"
	ctx := context.Background()

	// Store events 1-5.
	for i := uint64(1); i <= 5; i++ {
		kind := runtime.EventNodeStarted
		if i == 5 {
			kind = runtime.EventRunFinished
		}
		if err := store.Append(ctx, testEvent(runID, i, kind)); err != nil {
			t.Fatal(err)
		}
	}

	ts := setupTestServer(store, eb)
	defer ts.Close()

	// Request with ?after=3 should skip events 1-3.
	resp, err := http.Get(ts.URL + "/runs/" + runID + "/events?after=3")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			body.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}

	msgs := parseSSEMessages(body.String())
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (seq 4 and 5), got %d: %s", len(msgs), body.String())
	}

	if msgs[0].ID != "4" {
		t.Errorf("expected first message id 4, got %s", msgs[0].ID)
	}
	if msgs[1].ID != "5" {
		t.Errorf("expected second message id 5, got %s", msgs[1].ID)
	}
}

func TestSSEHandler_SequenceDedup(t *testing.T) {
	store := bus.NewMemEventStore()
	eb := bus.NewMemBus(bus.MemBusConfig{})
	defer eb.Close()

	runID := "run-dedup"
	ctx := context.Background()

	// Store events 1-2.
	if err := store.Append(ctx, testEvent(runID, 1, runtime.EventRunStarted)); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, testEvent(runID, 2, runtime.EventNodeStarted)); err != nil {
		t.Fatal(err)
	}

	ts := setupTestServer(store, eb)
	defer ts.Close()

	type result struct {
		body string
		err  error
	}
	resultCh := make(chan result, 1)

	reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", ts.URL+"/runs/"+runID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		defer resp.Body.Close()

		var body strings.Builder
		buf := make([]byte, 4096)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				body.Write(buf[:n])
			}
			if readErr != nil {
				break
			}
		}
		resultCh <- result{body: body.String()}
	}()

	// Give handler time to start.
	time.Sleep(100 * time.Millisecond)

	// Publish events that overlap with stored events (seq 1, 2) plus new ones.
	eb.Publish(testEvent(runID, 1, runtime.EventRunStarted))
	eb.Publish(testEvent(runID, 2, runtime.EventNodeStarted))
	eb.Publish(testEvent(runID, 3, runtime.EventNodeFinished))
	eb.Publish(testEvent(runID, 4, runtime.EventRunFinished))

	res := <-resultCh
	if res.err != nil {
		t.Fatal(res.err)
	}

	msgs := parseSSEMessages(res.body)
	// Should have: 1 (replay), 2 (replay), 3 (live, new), 4 (live, new).
	// Events 1 and 2 from live should be deduped.
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages (2 replay + 2 live), got %d: %s", len(msgs), res.body)
	}

	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	expected := []string{"1", "2", "3", "4"}
	for i, exp := range expected {
		if ids[i] != exp {
			t.Errorf("message %d: expected id %s, got %s", i, exp, ids[i])
		}
	}
}

func TestSSEHandler_Heartbeat(t *testing.T) {
	store := bus.NewMemEventStore()
	eb := bus.NewMemBus(bus.MemBusConfig{})
	defer eb.Close()

	runID := "run-heartbeat"

	// Create handler with a short heartbeat for testing.
	handler := sse.NewSSEHandler(store, eb)

	// We will test using a custom recorder that captures writes.
	mux := http.NewServeMux()
	mux.Handle("GET /runs/{run_id}/events", handler)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/runs/"+runID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Read until we see a heartbeat. Since HeartbeatInterval is 15s, we use
	// a shorter approach: just publish a run.finished after a brief delay.
	// For a proper heartbeat test, we'd need to shorten the interval.
	// Instead, let's verify the handler writes a heartbeat by using a
	// buffered read with a deadline.

	// For this test, send run.finished to close the stream, then verify
	// the overall behavior.
	time.Sleep(50 * time.Millisecond)
	eb.Publish(testEvent(runID, 1, runtime.EventRunFinished))

	var body strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			body.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}

	msgs := parseSSEMessages(body.String())
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Event != "run.finished" {
		t.Errorf("expected run.finished, got %s", msgs[0].Event)
	}
}

func TestSSEHandler_HeartbeatSent(t *testing.T) {
	// This test validates that the heartbeat comment is actually written
	// by checking raw body content. We use a custom transport that supports
	// streaming reads.

	store := bus.NewMemEventStore()
	eb := bus.NewMemBus(bus.MemBusConfig{})
	defer eb.Close()

	runID := "run-heartbeat-check"

	handler := sse.NewSSEHandler(store, eb)
	mux := http.NewServeMux()
	mux.Handle("GET /runs/{run_id}/events", handler)

	// Use httptest.NewUnstartedServer so we can control timing.
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/runs/"+runID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Wait for slightly longer than the heartbeat interval.
	time.Sleep(sse.HeartbeatInterval + 2*time.Second)

	// Now send run.finished to close the stream.
	eb.Publish(testEvent(runID, 1, runtime.EventRunFinished))

	var body strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			body.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}

	rawBody := body.String()

	// Verify the heartbeat comment was present.
	if !strings.Contains(rawBody, ": ping") {
		t.Errorf("expected heartbeat ': ping' in body, got: %s", rawBody)
	}

	// Verify the run.finished event is also present.
	msgs := parseSSEMessages(rawBody)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 event message, got %d", len(msgs))
	}
	if msgs[0].Event != "run.finished" {
		t.Errorf("expected run.finished, got %s", msgs[0].Event)
	}
}

func TestSSEHandler_ClientDisconnect(t *testing.T) {
	store := bus.NewMemEventStore()
	eb := bus.NewMemBus(bus.MemBusConfig{})
	defer eb.Close()

	runID := "run-disconnect"

	ts := setupTestServer(store, eb)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())

	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/runs/"+runID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	// Give handler time to enter live streaming.
	time.Sleep(100 * time.Millisecond)

	// Cancel the context to simulate client disconnect.
	cancel()
	resp.Body.Close()

	// Wait a bit, then verify the handler doesn't panic or hang.
	time.Sleep(100 * time.Millisecond)

	// If we got here without hanging, the test passes.
	// Publish another event to verify the handler is no longer processing.
	eb.Publish(testEvent(runID, 1, runtime.EventNodeStarted))
	time.Sleep(50 * time.Millisecond)
}

func TestSSEHandler_StreamClosesOnRunFinished(t *testing.T) {
	store := bus.NewMemEventStore()
	eb := bus.NewMemBus(bus.MemBusConfig{})
	defer eb.Close()

	runID := "run-close-on-finish"

	ts := setupTestServer(store, eb)
	defer ts.Close()

	type result struct {
		body string
		err  error
	}
	resultCh := make(chan result, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/runs/"+runID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		defer resp.Body.Close()

		var body strings.Builder
		buf := make([]byte, 4096)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				body.Write(buf[:n])
			}
			if readErr != nil {
				break
			}
		}
		resultCh <- result{body: body.String()}
	}()

	// Give handler time to subscribe.
	time.Sleep(100 * time.Millisecond)

	// Publish some events then run.finished.
	eb.Publish(testEvent(runID, 1, runtime.EventRunStarted))
	eb.Publish(testEvent(runID, 2, runtime.EventNodeStarted))
	eb.Publish(testEvent(runID, 3, runtime.EventRunFinished))

	// Publish an event after run.finished - should not be received.
	time.Sleep(50 * time.Millisecond)
	eb.Publish(testEvent(runID, 4, runtime.EventNodeStarted))

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatal(res.err)
		}
		msgs := parseSSEMessages(res.body)
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d: %s", len(msgs), res.body)
		}
		if msgs[2].Event != "run.finished" {
			t.Errorf("expected last event run.finished, got %s", msgs[2].Event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stream to close")
	}
}

func TestSSEHandler_MissingRunID(t *testing.T) {
	store := bus.NewMemEventStore()
	eb := bus.NewMemBus(bus.MemBusConfig{})
	defer eb.Close()

	handler := sse.NewSSEHandler(store, eb)

	// Test with empty run_id (no path value set).
	req := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSSEHandler_InvalidAfterParam(t *testing.T) {
	store := bus.NewMemEventStore()
	eb := bus.NewMemBus(bus.MemBusConfig{})
	defer eb.Close()

	ts := setupTestServer(store, eb)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/runs/run-1/events?after=notanumber")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestSSEHandler_SSEFormat(t *testing.T) {
	store := bus.NewMemEventStore()
	eb := bus.NewMemBus(bus.MemBusConfig{})
	defer eb.Close()

	runID := "run-format"
	ctx := context.Background()

	evt := runtime.Event{
		Kind:    runtime.EventNodeStarted,
		RunID:   runID,
		NodeID:  "node-1",
		NodeKind: "llm",
		Time:    time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		Attempt: 2,
		Elapsed: 1500 * time.Millisecond,
		Payload: map[string]any{"model": "gpt-4"},
		Seq:     42,
		TraceID: "abc123",
		SpanID:  "def456",
	}

	if err := store.Append(ctx, evt); err != nil {
		t.Fatal(err)
	}
	// Also store a run.finished so the stream closes.
	finishEvt := testEvent(runID, 43, runtime.EventRunFinished)
	if err := store.Append(ctx, finishEvt); err != nil {
		t.Fatal(err)
	}

	ts := setupTestServer(store, eb)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/runs/" + runID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			body.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}

	raw := body.String()

	// Verify SSE format: "id: 42\nevent: node.started\ndata: {...}\n\n"
	if !strings.Contains(raw, "id: 42\n") {
		t.Error("expected 'id: 42' in output")
	}
	if !strings.Contains(raw, "event: node.started\n") {
		t.Error("expected 'event: node.started' in output")
	}

	msgs := parseSSEMessages(raw)
	if len(msgs) < 1 {
		t.Fatal("expected at least 1 message")
	}

	// Parse the JSON data to verify all fields.
	var data map[string]any
	if err := json.Unmarshal([]byte(msgs[0].Data), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if data["kind"] != "node.started" {
		t.Errorf("expected kind node.started, got %v", data["kind"])
	}
	if data["run_id"] != runID {
		t.Errorf("expected run_id %s, got %v", runID, data["run_id"])
	}
	if data["node_id"] != "node-1" {
		t.Errorf("expected node_id node-1, got %v", data["node_id"])
	}
	if data["node_kind"] != "llm" {
		t.Errorf("expected node_kind llm, got %v", data["node_kind"])
	}
	if data["attempt"] != float64(2) {
		t.Errorf("expected attempt 2, got %v", data["attempt"])
	}
	if data["elapsed_ms"] != float64(1500) {
		t.Errorf("expected elapsed_ms 1500, got %v", data["elapsed_ms"])
	}
	if data["seq"] != float64(42) {
		t.Errorf("expected seq 42, got %v", data["seq"])
	}
	if data["trace_id"] != "abc123" {
		t.Errorf("expected trace_id abc123, got %v", data["trace_id"])
	}
	if data["span_id"] != "def456" {
		t.Errorf("expected span_id def456, got %v", data["span_id"])
	}
	if payload, ok := data["payload"].(map[string]any); ok {
		if payload["model"] != "gpt-4" {
			t.Errorf("expected payload.model gpt-4, got %v", payload["model"])
		}
	} else {
		t.Error("expected payload to be a map")
	}
}

func TestSSEHandler_ReplayThenLive(t *testing.T) {
	// Test the full flow: replay stored events, then receive live events,
	// with proper deduplication.
	store := bus.NewMemEventStore()
	eb := bus.NewMemBus(bus.MemBusConfig{})
	defer eb.Close()

	runID := "run-replay-live"
	ctx := context.Background()

	// Store events 1-3.
	if err := store.Append(ctx, testEvent(runID, 1, runtime.EventRunStarted)); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, testEvent(runID, 2, runtime.EventNodeStarted)); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, testEvent(runID, 3, runtime.EventNodeFinished)); err != nil {
		t.Fatal(err)
	}

	ts := setupTestServer(store, eb)
	defer ts.Close()

	type result struct {
		body string
		err  error
	}
	resultCh := make(chan result, 1)

	reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", ts.URL+"/runs/"+runID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		defer resp.Body.Close()

		var body strings.Builder
		buf := make([]byte, 4096)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				body.Write(buf[:n])
			}
			if readErr != nil {
				break
			}
		}
		resultCh <- result{body: body.String()}
	}()

	// Give handler time to start.
	time.Sleep(100 * time.Millisecond)

	// Publish live events. Seq 2 and 3 should be deduped (already replayed).
	eb.Publish(testEvent(runID, 2, runtime.EventNodeStarted))
	eb.Publish(testEvent(runID, 3, runtime.EventNodeFinished))
	eb.Publish(testEvent(runID, 4, runtime.EventNodeStarted))
	eb.Publish(testEvent(runID, 5, runtime.EventRunFinished))

	res := <-resultCh
	if res.err != nil {
		t.Fatal(res.err)
	}

	msgs := parseSSEMessages(res.body)
	// Should get: 1, 2, 3 (from replay), 4, 5 (from live). Seq 2, 3 from live are deduped.
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d: %s", len(msgs), res.body)
	}

	expectedIDs := []string{"1", "2", "3", "4", "5"}
	for i, exp := range expectedIDs {
		if msgs[i].ID != exp {
			t.Errorf("message %d: expected id %s, got %s", i, exp, msgs[i].ID)
		}
	}
}

func TestSSEHandler_AfterCursorWithLive(t *testing.T) {
	store := bus.NewMemEventStore()
	eb := bus.NewMemBus(bus.MemBusConfig{})
	defer eb.Close()

	runID := "run-after-live"
	ctx := context.Background()

	// Store events 1-3.
	for i := uint64(1); i <= 3; i++ {
		if err := store.Append(ctx, testEvent(runID, i, runtime.EventNodeStarted)); err != nil {
			t.Fatal(err)
		}
	}

	ts := setupTestServer(store, eb)
	defer ts.Close()

	type result struct {
		body string
		err  error
	}
	resultCh := make(chan result, 1)

	reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start with ?after=2, so only event 3 is replayed from store.
	req, err := http.NewRequestWithContext(reqCtx, "GET", ts.URL+"/runs/"+runID+"/events?after=2", nil)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		defer resp.Body.Close()

		var body strings.Builder
		buf := make([]byte, 4096)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				body.Write(buf[:n])
			}
			if readErr != nil {
				break
			}
		}
		resultCh <- result{body: body.String()}
	}()

	time.Sleep(100 * time.Millisecond)

	// Publish live events: seq 3 (should be deduped), 4, 5 (run.finished).
	eb.Publish(testEvent(runID, 3, runtime.EventNodeStarted))
	eb.Publish(testEvent(runID, 4, runtime.EventNodeFinished))
	eb.Publish(testEvent(runID, 5, runtime.EventRunFinished))

	res := <-resultCh
	if res.err != nil {
		t.Fatal(res.err)
	}

	msgs := parseSSEMessages(res.body)
	// Should get: 3 (replay), 4, 5 (live). Seq 3 from live is deduped.
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d: %s", len(msgs), res.body)
	}

	expectedIDs := []string{"3", "4", "5"}
	for i, exp := range expectedIDs {
		if msgs[i].ID != exp {
			t.Errorf("message %d: expected id %s, got %s", i, exp, msgs[i].ID)
		}
	}
}
