package llmprovider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

type stubLLMClient struct {
	resp core.LLMResponse
	err  error
}

func (s *stubLLMClient) Complete(_ context.Context, _ core.LLMRequest) (core.LLMResponse, error) {
	return s.resp, s.err
}

func collectEvents(t *testing.T, client core.LLMClient, eventCtx LLMEventContext, ctx context.Context) []runtime.Event {
	t.Helper()
	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) {
		events = append(events, e)
	})
	instrumented := NewInstrumentedClient(client, emitter, eventCtx)
	req := core.LLMRequest{
		Model:       "test-model",
		System:      "You are a test assistant.",
		InputText:   "Summarize: subject",
		Messages:    []core.LLMMessage{{Role: "user", Content: "Summarize: subject"}},
		Temperature: ptrFloat64(0.2),
		JSONSchema:  map[string]any{"type": "object"},
	}
	_, _ = instrumented.Complete(ctx, req)
	return events
}

func ptrFloat64(v float64) *float64 { return &v }

func payloadString(e runtime.Event, key string) string {
	s, _ := e.Payload[key].(string)
	return s
}

func TestInstrumentedClientDefaultEmitsMetadataOnly(t *testing.T) {
	events := collectEvents(t, &stubLLMClient{
		resp: core.LLMResponse{
			Text:     "the answer",
			Model:    "test-model",
			Provider: "stub",
			Usage:    core.LLMTokenUsage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8},
		},
	}, LLMEventContext{RunID: "run-1", NodeID: "n1"}, context.Background())

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (llm.call, llm.response)", len(events))
	}
	call, response := events[0], events[1]
	if call.Kind != runtime.EventLLMCall || response.Kind != runtime.EventLLMResponse {
		t.Fatalf("kinds = %s, %s", call.Kind, response.Kind)
	}

	// Call event: metadata survives, content becomes descriptors.
	for _, field := range []string{"system_prompt", "messages", "input_text", "json_schema"} {
		if _, present := call.Payload[field]; present {
			t.Errorf("llm.call carries %q by default: %+v", field, call.Payload)
		}
	}
	if call.Payload["model"] != "test-model" {
		t.Errorf("model = %v, want kept", call.Payload["model"])
	}
	if call.Payload["temperature"] != 0.2 {
		t.Errorf("temperature = %v, want kept", call.Payload["temperature"])
	}
	if call.Payload["system_prompt_bytes"] != len("You are a test assistant.") {
		t.Errorf("system_prompt_bytes = %v", call.Payload["system_prompt_bytes"])
	}
	if call.Payload["messages_count"] != 1 {
		t.Errorf("messages_count = %v, want 1", call.Payload["messages_count"])
	}
	if call.Payload["input_text_bytes"] != len("Summarize: subject") {
		t.Errorf("input_text_bytes = %v", call.Payload["input_text_bytes"])
	}
	if call.Payload["json_schema_bytes"] == nil {
		t.Errorf("json_schema_bytes missing")
	}

	// Response event: no completion, descriptor present, usage kept.
	if _, present := response.Payload["completion"]; present {
		t.Errorf("llm.response carries completion by default: %+v", response.Payload)
	}
	if response.Payload["completion_bytes"] != len("the answer") {
		t.Errorf("completion_bytes = %v", response.Payload["completion_bytes"])
	}
	if response.Payload["input_tokens"] != 5 || response.Payload["output_tokens"] != 3 {
		t.Errorf("usage tokens = %v/%v, want kept", response.Payload["input_tokens"], response.Payload["output_tokens"])
	}
}

func TestInstrumentedClientContextCaptureOptIn(t *testing.T) {
	ctx := runtime.ContextWithContentCapture(context.Background(), runtime.ContentCapture{
		Enabled:  true,
		MaxBytes: 128,
	})
	events := collectEvents(t, &stubLLMClient{
		resp: core.LLMResponse{
			Text:     "sk-abcdef123456789 in the completion",
			Model:    "test-model",
			Provider: "stub",
			Usage:    core.LLMTokenUsage{TotalTokens: 8},
		},
	}, LLMEventContext{RunID: "run-1", NodeID: "n1"}, ctx)

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	call, response := events[0], events[1]

	if got := payloadString(call, "system_prompt"); got != "You are a test assistant." {
		t.Errorf("system_prompt = %q, want captured", got)
	}
	if got := payloadString(call, "input_text"); got != "Summarize: subject" {
		t.Errorf("input_text = %q, want captured", got)
	}
	if got := payloadString(call, "messages"); !strings.Contains(got, "Summarize: subject") {
		t.Errorf("messages = %q, want captured JSON", got)
	}

	completion := payloadString(response, "completion")
	if completion == "" {
		t.Fatal("completion missing under capture opt-in")
	}
	if strings.Contains(completion, "sk-abcdef123456789") {
		t.Errorf("captured completion leaks credential: %q", completion)
	}
}

func TestInstrumentedClientConfiguredCaptureOptIn(t *testing.T) {
	// Capture policy set on LLMEventContext for wiring outside a
	// runtime run.
	capture := runtime.ContentCapture{Enabled: true}
	events := collectEvents(t, &stubLLMClient{
		resp: core.LLMResponse{Text: "answer", Model: "test-model", Provider: "stub"},
	}, LLMEventContext{RunID: "run-1", NodeID: "n1", Capture: &capture}, context.Background())

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if payloadString(events[1], "completion") != "answer" {
		t.Errorf("completion = %q, want captured", payloadString(events[1], "completion"))
	}
}

func TestInstrumentedClientErrorEventsScrubbedAndCoded(t *testing.T) {
	events := collectEvents(t, &stubLLMClient{
		err: context.DeadlineExceeded,
	}, LLMEventContext{RunID: "run-1", NodeID: "n1"}, context.Background())

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	response := events[1]
	if response.Payload["status"] != "error" {
		t.Fatalf("status = %v, want error", response.Payload["status"])
	}
	if response.Payload["error_code"] != "deadline_exceeded" {
		t.Errorf("error_code = %v, want deadline_exceeded", response.Payload["error_code"])
	}
	msg, _ := response.Payload["error"].(string)
	if msg == "" {
		t.Error("error message missing")
	}
}

func TestContextInstrumentedClientEmitsThroughRuntimeContext(t *testing.T) {
	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) {
		events = append(events, e)
	})

	// The node context carries the emitter; the request metadata
	// carries run/node identity (set by LLM nodes).
	ctx := runtime.ContextWithEmitter(context.Background(), emitter)
	req := core.LLMRequest{
		Model:     "test-model",
		InputText: "hello",
		Meta:      map[string]any{"run_id": "run-9", "node_id": "llm-node"},
	}

	client := NewContextInstrumentedClient(&stubLLMClient{
		resp: core.LLMResponse{Text: "answer", Model: "test-model", Provider: "stub"},
	})
	if _, err := client.Complete(ctx, req); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (llm.call, llm.response)", len(events))
	}
	call, response := events[0], events[1]
	if call.Kind != runtime.EventLLMCall || response.Kind != runtime.EventLLMResponse {
		t.Fatalf("kinds = %s, %s", call.Kind, response.Kind)
	}
	if call.RunID != "run-9" || call.NodeID != "llm-node" {
		t.Errorf("call identity = %s/%s, want run-9/llm-node", call.RunID, call.NodeID)
	}
	if response.RunID != "run-9" || response.NodeID != "llm-node" {
		t.Errorf("response identity = %s/%s, want run-9/llm-node", response.RunID, response.NodeID)
	}
	if response.Payload["provider"] != "stub" {
		t.Errorf("provider = %v, want stub", response.Payload["provider"])
	}
	// Default privacy holds: descriptors, no content.
	if _, present := call.Payload["input_text"]; present {
		t.Errorf("input_text present by default: %+v", call.Payload)
	}
	if call.Payload["input_text_bytes"] != len("hello") {
		t.Errorf("input_text_bytes = %v", call.Payload["input_text_bytes"])
	}
}

func TestContextInstrumentedClientSilentOutsideRuntime(t *testing.T) {
	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) {
		events = append(events, e)
	})
	_ = emitter

	// No emitter in the context: no events, the client behaves normally.
	client := NewContextInstrumentedClient(&stubLLMClient{
		resp: core.LLMResponse{Text: "answer", Model: "test-model"},
	})
	resp, err := client.Complete(context.Background(), core.LLMRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "answer" {
		t.Errorf("text = %q, want answer", resp.Text)
	}
	if len(events) != 0 {
		t.Errorf("events emitted outside a runtime run: %d", len(events))
	}
}

func TestContextInstrumentedClientPreservesStreaming(t *testing.T) {
	stub := &stubStreamingLLMClient{chunks: []core.StreamChunk{
		{Delta: "he", Index: 0},
		{Delta: "y", Index: 1, Done: true},
	}}

	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) {
		events = append(events, e)
	})
	ctx := runtime.ContextWithEmitter(context.Background(), emitter)

	client := NewContextInstrumentedClient(stub)
	streaming, ok := client.(core.StreamingLLMClient)
	if !ok {
		t.Fatal("context-instrumented wrapper lost the streaming path")
	}
	ch, err := streaming.CompleteStream(ctx, core.LLMRequest{
		Model: "test-model",
		Meta:  map[string]any{"run_id": "run-9", "node_id": "llm-node"},
	})
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	var collected []string
	for chunk := range ch {
		if chunk.Delta != "" {
			collected = append(collected, chunk.Delta)
		}
	}
	if len(collected) != 2 {
		t.Fatalf("stream deltas = %v, want 2", collected)
	}

	// The final llm.response event is emitted from the stream goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for len(events) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (llm.call, llm.response)", len(events))
	}
	if events[1].Kind != runtime.EventLLMResponse || events[1].RunID != "run-9" {
		t.Errorf("response event = %s/%s, want llm.response/run-9", events[1].Kind, events[1].RunID)
	}
}

// stubStreamingLLMClient streams a fixed chunk list.
type stubStreamingLLMClient struct {
	chunks []core.StreamChunk
}

func (s *stubStreamingLLMClient) Complete(_ context.Context, _ core.LLMRequest) (core.LLMResponse, error) {
	return core.LLMResponse{Text: "sync"}, nil
}

func (s *stubStreamingLLMClient) CompleteStream(_ context.Context, _ core.LLMRequest) (<-chan core.StreamChunk, error) {
	ch := make(chan core.StreamChunk, len(s.chunks))
	for _, c := range s.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}
