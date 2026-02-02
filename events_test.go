package petalflow

import (
	"testing"
	"time"
)

func TestEventKind_String(t *testing.T) {
	tests := []struct {
		kind     EventKind
		expected string
	}{
		{EventRunStarted, "run_started"},
		{EventNodeStarted, "node_started"},
		{EventNodeOutput, "node_output"},
		{EventNodeFailed, "node_failed"},
		{EventNodeFinished, "node_finished"},
		{EventRunFinished, "run_finished"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.expected {
				t.Errorf("EventKind.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNewEvent(t *testing.T) {
	before := time.Now()
	event := NewEvent(EventRunStarted, "run-123")
	after := time.Now()

	if event.Kind != EventRunStarted {
		t.Errorf("Event.Kind = %v, want %v", event.Kind, EventRunStarted)
	}
	if event.RunID != "run-123" {
		t.Errorf("Event.RunID = %v, want 'run-123'", event.RunID)
	}
	if event.Time.Before(before) || event.Time.After(after) {
		t.Error("Event.Time should be between before and after")
	}
	if event.Attempt != 1 {
		t.Errorf("Event.Attempt = %v, want 1", event.Attempt)
	}
	if event.Payload == nil {
		t.Error("Event.Payload should be initialized")
	}
}

func TestEvent_WithNode(t *testing.T) {
	event := NewEvent(EventNodeStarted, "run-123").
		WithNode("node-1", NodeKindLLM)

	if event.NodeID != "node-1" {
		t.Errorf("Event.NodeID = %v, want 'node-1'", event.NodeID)
	}
	if event.NodeKind != NodeKindLLM {
		t.Errorf("Event.NodeKind = %v, want %v", event.NodeKind, NodeKindLLM)
	}
}

func TestEvent_WithAttempt(t *testing.T) {
	event := NewEvent(EventNodeFailed, "run-123").
		WithAttempt(3)

	if event.Attempt != 3 {
		t.Errorf("Event.Attempt = %v, want 3", event.Attempt)
	}
}

func TestEvent_WithElapsed(t *testing.T) {
	elapsed := 5 * time.Second
	event := NewEvent(EventNodeFinished, "run-123").
		WithElapsed(elapsed)

	if event.Elapsed != elapsed {
		t.Errorf("Event.Elapsed = %v, want %v", event.Elapsed, elapsed)
	}
}

func TestEvent_WithPayload(t *testing.T) {
	event := NewEvent(EventRunStarted, "run-123").
		WithPayload("key1", "value1").
		WithPayload("key2", 42)

	if event.Payload["key1"] != "value1" {
		t.Errorf("Event.Payload['key1'] = %v, want 'value1'", event.Payload["key1"])
	}
	if event.Payload["key2"] != 42 {
		t.Errorf("Event.Payload['key2'] = %v, want 42", event.Payload["key2"])
	}
}

func TestEvent_WithPayload_NilPayload(t *testing.T) {
	event := Event{Kind: EventRunStarted}
	event = event.WithPayload("key", "value")

	if event.Payload == nil {
		t.Error("WithPayload should initialize Payload if nil")
	}
	if event.Payload["key"] != "value" {
		t.Error("WithPayload should set value")
	}
}

func TestEvent_Chaining(t *testing.T) {
	event := NewEvent(EventNodeFinished, "run-123").
		WithNode("node-1", NodeKindTool).
		WithAttempt(2).
		WithElapsed(100*time.Millisecond).
		WithPayload("result", "success")

	if event.Kind != EventNodeFinished {
		t.Error("Kind not preserved through chaining")
	}
	if event.RunID != "run-123" {
		t.Error("RunID not preserved through chaining")
	}
	if event.NodeID != "node-1" {
		t.Error("NodeID not set")
	}
	if event.NodeKind != NodeKindTool {
		t.Error("NodeKind not set")
	}
	if event.Attempt != 2 {
		t.Error("Attempt not set")
	}
	if event.Elapsed != 100*time.Millisecond {
		t.Error("Elapsed not set")
	}
	if event.Payload["result"] != "success" {
		t.Error("Payload not set")
	}
}

func TestMultiEventHandler(t *testing.T) {
	var calls1, calls2 int

	handler1 := func(e Event) { calls1++ }
	handler2 := func(e Event) { calls2++ }

	multi := MultiEventHandler(handler1, handler2)

	event := NewEvent(EventRunStarted, "test")
	multi(event)

	if calls1 != 1 {
		t.Errorf("handler1 called %d times, want 1", calls1)
	}
	if calls2 != 1 {
		t.Errorf("handler2 called %d times, want 1", calls2)
	}
}

func TestMultiEventHandler_NilHandler(t *testing.T) {
	var calls int
	handler := func(e Event) { calls++ }

	// Should not panic with nil handlers
	multi := MultiEventHandler(nil, handler, nil)

	event := NewEvent(EventRunStarted, "test")
	multi(event)

	if calls != 1 {
		t.Errorf("handler called %d times, want 1", calls)
	}
}

func TestChannelEventHandler(t *testing.T) {
	ch := make(chan Event, 2)
	handler := ChannelEventHandler(ch)

	event1 := NewEvent(EventRunStarted, "test")
	event2 := NewEvent(EventRunFinished, "test")

	handler(event1)
	handler(event2)

	received1 := <-ch
	received2 := <-ch

	if received1.Kind != EventRunStarted {
		t.Error("First event kind incorrect")
	}
	if received2.Kind != EventRunFinished {
		t.Error("Second event kind incorrect")
	}
}

func TestChannelEventHandler_FullChannel(t *testing.T) {
	ch := make(chan Event, 1)
	handler := ChannelEventHandler(ch)

	// Fill the channel
	handler(NewEvent(EventRunStarted, "test"))

	// This should not block (event dropped)
	done := make(chan bool)
	go func() {
		handler(NewEvent(EventRunFinished, "test"))
		done <- true
	}()

	select {
	case <-done:
		// Good, handler returned without blocking
	case <-time.After(100 * time.Millisecond):
		t.Error("ChannelEventHandler blocked on full channel")
	}
}
