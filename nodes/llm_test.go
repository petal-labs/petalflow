package nodes

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

// mockLLMClient is a mock implementation of adapters.LLMClient for testing.
type mockLLMClient struct {
	response core.LLMResponse
	err      error
	requests []core.LLMRequest // captures all requests
}

func (m *mockLLMClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	m.requests = append(m.requests, req)
	if m.err != nil {
		return core.LLMResponse{}, m.err
	}
	return m.response, nil
}

func TestNewLLMNode(t *testing.T) {
	client := &mockLLMClient{}
	node := NewLLMNode("test-llm", client, LLMNodeConfig{
		Model:  "gpt-4",
		System: "You are helpful",
	})

	if node.ID() != "test-llm" {
		t.Errorf("expected ID 'test-llm', got %q", node.ID())
	}
	if node.Kind() != core.NodeKindLLM {
		t.Errorf("expected kind %q, got %q", core.NodeKindLLM, node.Kind())
	}
}

func TestNewLLMNode_DefaultOutputKey(t *testing.T) {
	client := &mockLLMClient{}
	node := NewLLMNode("my-node", client, LLMNodeConfig{})

	config := node.Config()
	if config.OutputKey != "my-node_output" {
		t.Errorf("expected default output key 'my-node_output', got %q", config.OutputKey)
	}
}

func TestNewLLMNode_DefaultRetryPolicy(t *testing.T) {
	client := &mockLLMClient{}
	node := NewLLMNode("test", client, LLMNodeConfig{})

	config := node.Config()
	if config.RetryPolicy.MaxAttempts != 3 {
		t.Errorf("expected default MaxAttempts 3, got %d", config.RetryPolicy.MaxAttempts)
	}
}

func TestNewLLMNode_DefaultTimeout(t *testing.T) {
	client := &mockLLMClient{}
	node := NewLLMNode("test", client, LLMNodeConfig{})

	config := node.Config()
	if config.Timeout != 60*time.Second {
		t.Errorf("expected default timeout 60s, got %v", config.Timeout)
	}
}

func TestLLMNode_Run_SimplePrompt(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{
			Text:  "Hello, world!",
			Model: "gpt-4",
			Usage: core.LLMTokenUsage{
				InputTokens:  10,
				OutputTokens: 5,
				TotalTokens:  15,
			},
		},
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model:     "gpt-4",
		System:    "You are helpful",
		InputVars: []string{"question"},
		OutputKey: "answer",
	})

	env := core.NewEnvelope().WithVar("question", "What is 2+2?")
	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check output was stored
	answer, ok := result.GetVar("answer")
	if !ok {
		t.Fatal("expected answer to be stored in envelope")
	}
	if answer != "Hello, world!" {
		t.Errorf("expected answer 'Hello, world!', got %v", answer)
	}

	// Check usage was stored
	usage, ok := result.GetVar("answer_usage")
	if !ok {
		t.Fatal("expected usage to be stored in envelope")
	}
	tokenUsage := usage.(core.TokenUsage)
	if tokenUsage.InputTokens != 10 {
		t.Errorf("expected 10 input tokens, got %d", tokenUsage.InputTokens)
	}
}

func TestLLMNode_Run_WithTemplate(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{Text: "Answer"},
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model:          "gpt-4",
		PromptTemplate: "User: {{.name}}\nQuestion: {{.question}}",
	})

	env := core.NewEnvelope().
		WithVar("name", "Alice").
		WithVar("question", "How are you?")
	_, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check the request that was sent
	if len(client.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(client.requests))
	}
	req := client.requests[0]
	expected := "User: Alice\nQuestion: How are you?"
	if req.InputText != expected {
		t.Errorf("expected prompt %q, got %q", expected, req.InputText)
	}
}

func TestLLMNode_Run_WithJSONSchema(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{
			Text: `{"name": "test"}`,
			JSON: map[string]any{"name": "test"},
		},
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model: "gpt-4",
		JSONSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
		},
		OutputKey: "result",
	})

	env := core.NewEnvelope().WithVar("input", "test")
	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check JSON output was stored
	output, ok := result.GetVar("result")
	if !ok {
		t.Fatal("expected result to be stored")
	}
	jsonOutput := output.(map[string]any)
	if jsonOutput["name"] != "test" {
		t.Errorf("expected name 'test', got %v", jsonOutput["name"])
	}
}

func TestLLMNode_Run_WithJSONSchema_InvalidJSONErrors(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{
			Text: "not json at all",
			// JSON nil: the provider could not parse structured output.
		},
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model:       "gpt-4",
		JSONSchema:  map[string]any{"type": "object"},
		OutputKey:   "result",
		RetryPolicy: core.RetryPolicy{MaxAttempts: 1, Backoff: time.Millisecond},
	})

	_, err := node.Run(context.Background(), core.NewEnvelope())
	if err == nil {
		t.Fatal("expected an error when structured output is requested but the model returned non-JSON, got nil")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("error = %v, want it to mention invalid JSON", err)
	}
}

func TestLLMNode_Run_WithJSONSchema_ParsesTextWhenNotPreParsed(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{
			Text: `{"name": "parsed"}`,
			// JSON nil: force the node to parse the text itself.
		},
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model:      "gpt-4",
		JSONSchema: map[string]any{"type": "object"},
		OutputKey:  "result",
	})

	result, err := node.Run(context.Background(), core.NewEnvelope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := result.GetVar("result")
	if !ok {
		t.Fatal("expected result to be stored")
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected result to be a JSON object, got %T", out)
	}
	if m["name"] != "parsed" {
		t.Errorf("result[name] = %v, want %q", m["name"], "parsed")
	}
}

func TestLLMNode_Run_Streaming_WithJSONSchema_ParsesJSON(t *testing.T) {
	client := &mockStreamingLLMClient{
		chunks: []core.StreamChunk{
			{Delta: `{"name":`, Index: 0},
			{Delta: ` "streamed"}`, Index: 1},
			{Done: true},
		},
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model:      "gpt-4",
		JSONSchema: map[string]any{"type": "object"},
		OutputKey:  "result",
	})

	ctx := runtime.ContextWithEmitter(context.Background(), func(e runtime.Event) {})
	env := core.NewEnvelope()
	env.Trace.RunID = "r"

	result, err := node.Run(ctx, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, _ := result.GetVar("result")
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected streamed structured output to be a JSON object, got %T", out)
	}
	if m["name"] != "streamed" {
		t.Errorf("result[name] = %v, want %q", m["name"], "streamed")
	}
}

func TestLLMNode_Run_Streaming_WithJSONSchema_InvalidJSONErrors(t *testing.T) {
	client := &mockStreamingLLMClient{
		chunks: []core.StreamChunk{
			{Delta: "plain text", Index: 0},
			{Done: true},
		},
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model:      "gpt-4",
		JSONSchema: map[string]any{"type": "object"},
		OutputKey:  "result",
	})

	ctx := runtime.ContextWithEmitter(context.Background(), func(e runtime.Event) {})
	env := core.NewEnvelope()
	env.Trace.RunID = "r"

	_, err := node.Run(ctx, env)
	if err == nil {
		t.Fatal("expected an error when streamed structured output is not valid JSON, got nil")
	}
}

func TestLLMNode_Run_StructuredOutput_NonConformingErrors(t *testing.T) {
	// Valid JSON object, but missing the required "name" field.
	client := &mockLLMClient{
		response: core.LLMResponse{Text: `{"other": 1}`},
	}
	node := NewLLMNode("t", client, LLMNodeConfig{
		Model: "gpt-4",
		JSONSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
			"required":   []any{"name"},
		},
		OutputKey: "out",
	})

	_, err := node.Run(context.Background(), core.NewEnvelope())
	if err == nil {
		t.Fatal("expected a schema-conformance error for output missing a required field, got nil")
	}
	if !strings.Contains(err.Error(), "schema") && !strings.Contains(err.Error(), "conform") {
		t.Errorf("error = %v, want it to mention schema conformance", err)
	}
}

func TestLLMNode_Run_StructuredOutput_TypeMismatchErrors(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{Text: `{"age": "not a number"}`},
	}
	node := NewLLMNode("t", client, LLMNodeConfig{
		Model: "gpt-4",
		JSONSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"age": map[string]any{"type": "integer"}},
			"required":   []any{"age"},
		},
		OutputKey: "out",
	})

	if _, err := node.Run(context.Background(), core.NewEnvelope()); err == nil {
		t.Fatal("expected a schema-conformance error for a wrong-typed field, got nil")
	}
}

func TestLLMNode_Run_StructuredOutput_ConformingPasses(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{Text: `{"name": "x"}`},
	}
	node := NewLLMNode("t", client, LLMNodeConfig{
		Model: "gpt-4",
		JSONSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
			"required":   []any{"name"},
		},
		OutputKey: "out",
	})

	result, err := node.Run(context.Background(), core.NewEnvelope())
	if err != nil {
		t.Fatalf("unexpected error for conforming output: %v", err)
	}
	out, _ := result.GetVar("out")
	m, ok := out.(map[string]any)
	if !ok || m["name"] != "x" {
		t.Errorf("output = %v, want a JSON object with name=x", out)
	}
}

func TestLLMNode_Run_Error(t *testing.T) {
	client := &mockLLMClient{
		err: errors.New("API error"),
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model:       "gpt-4",
		RetryPolicy: core.RetryPolicy{MaxAttempts: 1, Backoff: time.Millisecond},
	})

	env := core.NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLLMNode_Run_RetryOnError(t *testing.T) {
	callCount := 0

	// Fail first two calls, succeed on third
	client := &countingMockLLMClient{
		failCount: 2,
		successResponse: core.LLMResponse{
			Text: "Success",
		},
		callCount: &callCount,
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model: "gpt-4",
		RetryPolicy: core.RetryPolicy{
			MaxAttempts: 3,
			Backoff:     time.Millisecond,
		},
	})

	env := core.NewEnvelope()
	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}

	output, _ := result.GetVar("test_output")
	if output != "Success" {
		t.Errorf("expected 'Success', got %v", output)
	}
}

func TestLLMNode_Run_ContextCancellation(t *testing.T) {
	// Use a slow mock that checks context
	client := &slowMockLLMClient{
		delay: 500 * time.Millisecond,
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model:   "gpt-4",
		Timeout: 50 * time.Millisecond, // Short timeout
	})

	env := core.NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Fatal("expected error due to timeout")
	}
}

func TestLLMNode_Run_RecordMessages(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{
			Text:     "Response",
			Model:    "gpt-4",
			Provider: "openai",
		},
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model:          "gpt-4",
		InputVars:      []string{"input"},
		RecordMessages: true,
	})

	env := core.NewEnvelope().WithVar("input", "Hello")
	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}

	if result.Messages[0].Role != "user" {
		t.Errorf("expected first message role 'user', got %q", result.Messages[0].Role)
	}
	if result.Messages[1].Role != "assistant" {
		t.Errorf("expected second message role 'assistant', got %q", result.Messages[1].Role)
	}
}

func TestLLMNode_Run_BudgetExceeded(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{
			Text: "Response",
			Usage: core.LLMTokenUsage{
				InputTokens:  1000,
				OutputTokens: 500,
				TotalTokens:  1500,
			},
		},
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model: "gpt-4",
		Budget: &core.Budget{
			MaxTotalTokens: 100, // Budget exceeded
		},
	})

	env := core.NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Fatal("expected error due to budget exceeded")
	}
}

func TestLLMNode_Run_Temperature(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{Text: "OK"},
	}

	temp := 0.7
	node := NewLLMNode("test", client, LLMNodeConfig{
		Model:       "gpt-4",
		Temperature: &temp,
	})

	env := core.NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(client.requests))
	}
	if client.requests[0].Temperature == nil || *client.requests[0].Temperature != 0.7 {
		t.Errorf("expected temperature 0.7, got %v", client.requests[0].Temperature)
	}
}

func TestLLMNode_InterfaceCompliance(t *testing.T) {
	var _ core.Node = (*LLMNode)(nil)
}

func TestLLMNode_Run_EmitsOutputFinalEvent(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{
			Text:  "Hello!",
			Model: "gpt-4",
			Usage: core.LLMTokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
	}

	node := NewLLMNode("test-llm", client, LLMNodeConfig{
		Model:     "gpt-4",
		OutputKey: "answer",
	})

	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) {
		events = append(events, e)
	})

	ctx := runtime.ContextWithEmitter(context.Background(), emitter)
	env := core.NewEnvelope()
	env.Trace.RunID = "test-run"

	_, err := node.Run(ctx, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should emit node.output.final
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Kind != runtime.EventNodeOutputFinal {
		t.Errorf("kind = %v, want %v", events[0].Kind, runtime.EventNodeOutputFinal)
	}
	if events[0].Payload["text"] != "Hello!" {
		t.Errorf("text = %v, want 'Hello!'", events[0].Payload["text"])
	}
}

// mockStreamingLLMClient implements core.StreamingLLMClient
type mockStreamingLLMClient struct {
	chunks []core.StreamChunk
	err    error
}

func (m *mockStreamingLLMClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	return core.LLMResponse{Text: "sync-fallback"}, nil
}

func (m *mockStreamingLLMClient) CompleteStream(ctx context.Context, req core.LLMRequest) (<-chan core.StreamChunk, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan core.StreamChunk, len(m.chunks))
	for _, c := range m.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

// stallingStreamingLLMClient returns a stream that never sends and never
// closes, simulating a streaming client that stalls and ignores context.
type stallingStreamingLLMClient struct{}

func (m *stallingStreamingLLMClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	return core.LLMResponse{}, nil
}

func (m *stallingStreamingLLMClient) CompleteStream(ctx context.Context, req core.LLMRequest) (<-chan core.StreamChunk, error) {
	return make(chan core.StreamChunk), nil // never sent to, never closed
}

func TestLLMNode_Run_StreamingStallHonorsContext(t *testing.T) {
	node := NewLLMNode("test-llm", &stallingStreamingLLMClient{}, LLMNodeConfig{
		Model:     "gpt-4",
		OutputKey: "answer",
		Timeout:   50 * time.Millisecond,
	})

	env := core.NewEnvelope()
	env.Trace.RunID = "test-run"

	done := make(chan error, 1)
	go func() {
		_, err := node.Run(context.Background(), env)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a context error from a stalled stream, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LLMNode.Run did not return when the streaming client stalled")
	}
}

func TestLLMNode_Run_Streaming(t *testing.T) {
	client := &mockStreamingLLMClient{
		chunks: []core.StreamChunk{
			{Delta: "Hello", Index: 0},
			{Delta: " world", Index: 1},
			{Delta: "!", Index: 2},
			{Done: true, Usage: &core.LLMTokenUsage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8}},
		},
	}

	node := NewLLMNode("test-llm", client, LLMNodeConfig{
		Model:     "gpt-4",
		OutputKey: "answer",
	})

	var events []runtime.Event
	emitter := runtime.EventEmitter(func(e runtime.Event) {
		events = append(events, e)
	})
	ctx := runtime.ContextWithEmitter(context.Background(), emitter)
	env := core.NewEnvelope()
	env.Trace.RunID = "test-run"

	result, err := node.Run(ctx, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check output stored correctly
	answer, ok := result.GetVar("answer")
	if !ok || answer != "Hello world!" {
		t.Errorf("answer = %v, want 'Hello world!'", answer)
	}

	// Check delta events emitted
	var deltas, finals int
	for _, e := range events {
		switch e.Kind {
		case runtime.EventNodeOutputDelta:
			deltas++
		case runtime.EventNodeOutputFinal:
			finals++
		}
	}
	if deltas != 3 {
		t.Errorf("delta events = %d, want 3", deltas)
	}
	if finals != 1 {
		t.Errorf("final events = %d, want 1", finals)
	}
}

// countingMockLLMClient fails a specified number of times before succeeding.
type countingMockLLMClient struct {
	failCount       int
	successResponse core.LLMResponse
	callCount       *int
}

func (m *countingMockLLMClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	*m.callCount++
	if *m.callCount <= m.failCount {
		return core.LLMResponse{}, errors.New("temporary failure")
	}
	return m.successResponse, nil
}

// slowMockLLMClient simulates a slow LLM call for timeout testing.
type slowMockLLMClient struct {
	delay time.Duration
}

func (m *slowMockLLMClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	select {
	case <-ctx.Done():
		return core.LLMResponse{}, ctx.Err()
	case <-time.After(m.delay):
		return core.LLMResponse{Text: "OK"}, nil
	}
}
