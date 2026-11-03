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

func TestInstrumentedClientRichResponseFields(t *testing.T) {
	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) { events = append(events, e) })

	provider := "stub"
	client := NewInstrumentedClient(&stubLLMClient{
		resp: core.LLMResponse{
			Text:     "answer with tools",
			Model:    "test-model",
			Provider: provider,
			Status:   "tool_use",
			Usage: core.LLMTokenUsage{
				InputTokens:         7,
				OutputTokens:        3,
				TotalTokens:         10,
				CacheReadTokens:     4,
				CacheCreationTokens: 2,
				CostUSD:             0.01,
			},
			ToolCalls: []core.LLMToolCall{{ID: "call-1", Name: "lookup"}},
			Meta:     map[string]any{"response_id": "resp-9"},
			Reasoning: &core.LLMReasoningOutput{ID: "reason-1", Summary: []string{"thoughts"}},
		},
	}, emitter, LLMEventContext{RunID: "run-1", NodeID: "n1", Provider: provider})

	req := core.LLMRequest{
		Model:        "test-model",
		System:       "system",
		Instructions: "be brief",
		InputText:    "hello",
		Messages:     []core.LLMMessage{{Role: "user", Content: "hello"}},
		JSONSchema:   map[string]any{"type": "object"},
	}
	if _, err := client.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	call, response := events[0], events[1]

	// Call event descriptors for every content field.
	for _, field := range []string{"instructions", "messages", "json_schema"} {
		if _, present := call.Payload[field]; present {
			t.Errorf("llm.call carries %q by default", field)
		}
	}
	if call.Payload["instructions_bytes"] == nil ||
		call.Payload["messages_count"] != 1 ||
		call.Payload["json_schema_bytes"] == nil {
		t.Errorf("llm.call descriptors missing: %+v", call.Payload)
	}

	// Response metadata: usage detail, cost, request id, reasoning id.
	if response.Payload["cache_read_tokens"] != 4 || response.Payload["cache_creation_tokens"] != 2 {
		t.Errorf("cache tokens = %v/%v", response.Payload["cache_read_tokens"], response.Payload["cache_creation_tokens"])
	}
	if response.Payload["cost_usd"] != 0.01 {
		t.Errorf("cost_usd = %v", response.Payload["cost_usd"])
	}
	if response.Payload["request_id"] != "resp-9" {
		t.Errorf("request_id = %v", response.Payload["request_id"])
	}
	if response.Payload["reasoning_id"] != "reason-1" {
		t.Errorf("reasoning_id = %v", response.Payload["reasoning_id"])
	}
	if response.Payload["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v", response.Payload["stop_reason"])
	}
	// Tool calls and reasoning summary are content-classified.
	if _, present := response.Payload["tool_calls"]; present {
		t.Errorf("tool_calls present by default: %+v", response.Payload)
	}
	if response.Payload["tool_calls_count"] != 1 {
		t.Errorf("tool_calls_count = %v, want 1", response.Payload["tool_calls_count"])
	}
	if _, present := response.Payload["reasoning_summary"]; present {
		t.Errorf("reasoning_summary present by default")
	}
	if response.Payload["reasoning_summary_bytes"] == nil {
		t.Errorf("reasoning_summary_bytes missing")
	}
}

func TestInstrumentedClientStreamingFixedEmitter(t *testing.T) {
	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) { events = append(events, e) })

	client := NewInstrumentedStreamingClient(&stubStreamingLLMClient{chunks: []core.StreamChunk{
		{Delta: "he", Index: 0},
		{Delta: "y", Index: 1, Done: true, Accumulated: "hey", Usage: &core.LLMTokenUsage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4}},
	}}, emitter, LLMEventContext{RunID: "run-1", NodeID: "n1"})

	ch, err := client.CompleteStream(context.Background(), core.LLMRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	for range ch {
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(events) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	call, response := events[0], events[1]
	if call.Kind != runtime.EventLLMCall || response.Kind != runtime.EventLLMResponse {
		t.Fatalf("kinds = %s, %s", call.Kind, response.Kind)
	}
	if response.RunID != "run-1" || response.NodeID != "n1" {
		t.Errorf("response identity = %s/%s", response.RunID, response.NodeID)
	}
	if response.Payload["input_tokens"] != 2 || response.Payload["output_tokens"] != 2 {
		t.Errorf("usage tokens = %v/%v", response.Payload["input_tokens"], response.Payload["output_tokens"])
	}
	// ttft_ms is only set when the first delta arrives measurably after
	// the call started; an instant stub stream may omit it.
	// Completion is content-classified by default.
	if _, present := response.Payload["completion"]; present {
		t.Errorf("completion present by default: %+v", response.Payload)
	}
	if response.Payload["completion_bytes"] != len("hey") {
		t.Errorf("completion_bytes = %v, want %d", response.Payload["completion_bytes"], len("hey"))
	}
}

func TestInstrumentedClientStreamingStartErrorEmitsResponseEvent(t *testing.T) {
	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) { events = append(events, e) })

	client := NewInstrumentedStreamingClient(&stubFailingStreamClient{}, emitter, LLMEventContext{RunID: "run-1", NodeID: "n1"})

	if _, err := client.CompleteStream(context.Background(), core.LLMRequest{Model: "test-model"}); err == nil {
		t.Fatal("expected stream start error")
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (call + error response)", len(events))
	}
	response := events[1]
	if response.Payload["status"] != "error" {
		t.Fatalf("status = %v, want error", response.Payload["status"])
	}
	if response.Payload["error_code"] == nil || response.Payload["error_code"] == "" {
		t.Errorf("error_code missing: %+v", response.Payload)
	}
}

func TestInstrumentedClientStreamingChunkError(t *testing.T) {
	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) { events = append(events, e) })

	client := NewInstrumentedStreamingClient(&stubStreamingLLMClient{chunks: []core.StreamChunk{
		{Delta: "par", Index: 0},
		{Done: true, Error: context.DeadlineExceeded},
	}}, emitter, LLMEventContext{RunID: "run-1", NodeID: "n1"})

	ch, err := client.CompleteStream(context.Background(), core.LLMRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	for range ch {
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(events) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
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
}

// stubFailingStreamClient fails stream creation.
type stubFailingStreamClient struct{}

func (s *stubFailingStreamClient) Complete(_ context.Context, _ core.LLMRequest) (core.LLMResponse, error) {
	return core.LLMResponse{}, nil
}

func (s *stubFailingStreamClient) CompleteStream(_ context.Context, _ core.LLMRequest) (<-chan core.StreamChunk, error) {
	return nil, context.DeadlineExceeded
}

func TestContextInstrumentedClientToleratesMissingRequestMeta(t *testing.T) {
	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) { events = append(events, e) })
	ctx := runtime.ContextWithEmitter(context.Background(), emitter)

	// No run/node metadata on the request: events are emitted with empty
	// identity rather than failing.
	client := NewContextInstrumentedClient(&stubLLMClient{
		resp: core.LLMResponse{Text: "answer", Model: "test-model", Provider: "stub"},
	})
	if _, err := client.Complete(ctx, core.LLMRequest{Model: "test-model"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].RunID != "" || events[0].NodeID != "" {
		t.Errorf("identity = %s/%s, want empty", events[0].RunID, events[0].NodeID)
	}
	if events[1].Kind != runtime.EventLLMResponse {
		t.Errorf("kind = %s, want llm.response", events[1].Kind)
	}
}
