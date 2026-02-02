package petalflow

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockLLMClient is a mock implementation of adapters.LLMClient for testing.
type mockLLMClient struct {
	response LLMResponse
	err      error
	requests []LLMRequest // captures all requests
}

func (m *mockLLMClient) Complete(ctx context.Context, req LLMRequest) (LLMResponse, error) {
	m.requests = append(m.requests, req)
	if m.err != nil {
		return LLMResponse{}, m.err
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
	if node.Kind() != NodeKindLLM {
		t.Errorf("expected kind %q, got %q", NodeKindLLM, node.Kind())
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
		response: LLMResponse{
			Text:  "Hello, world!",
			Model: "gpt-4",
			Usage: LLMTokenUsage{
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

	env := NewEnvelope().WithVar("question", "What is 2+2?")
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
	tokenUsage := usage.(TokenUsage)
	if tokenUsage.InputTokens != 10 {
		t.Errorf("expected 10 input tokens, got %d", tokenUsage.InputTokens)
	}
}

func TestLLMNode_Run_WithTemplate(t *testing.T) {
	client := &mockLLMClient{
		response: LLMResponse{Text: "Answer"},
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model:          "gpt-4",
		PromptTemplate: "User: {{.name}}\nQuestion: {{.question}}",
	})

	env := NewEnvelope().
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
		response: LLMResponse{
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

	env := NewEnvelope().WithVar("input", "test")
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

func TestLLMNode_Run_Error(t *testing.T) {
	client := &mockLLMClient{
		err: errors.New("API error"),
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model:       "gpt-4",
		RetryPolicy: RetryPolicy{MaxAttempts: 1, Backoff: time.Millisecond},
	})

	env := NewEnvelope()
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
		successResponse: LLMResponse{
			Text: "Success",
		},
		callCount: &callCount,
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model: "gpt-4",
		RetryPolicy: RetryPolicy{
			MaxAttempts: 3,
			Backoff:     time.Millisecond,
		},
	})

	env := NewEnvelope()
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

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Fatal("expected error due to timeout")
	}
}

func TestLLMNode_Run_RecordMessages(t *testing.T) {
	client := &mockLLMClient{
		response: LLMResponse{
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

	env := NewEnvelope().WithVar("input", "Hello")
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
		response: LLMResponse{
			Text: "Response",
			Usage: LLMTokenUsage{
				InputTokens:  1000,
				OutputTokens: 500,
				TotalTokens:  1500,
			},
		},
	}

	node := NewLLMNode("test", client, LLMNodeConfig{
		Model: "gpt-4",
		Budget: &Budget{
			MaxTotalTokens: 100, // Budget exceeded
		},
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Fatal("expected error due to budget exceeded")
	}
}

func TestLLMNode_Run_Temperature(t *testing.T) {
	client := &mockLLMClient{
		response: LLMResponse{Text: "OK"},
	}

	temp := 0.7
	node := NewLLMNode("test", client, LLMNodeConfig{
		Model:       "gpt-4",
		Temperature: &temp,
	})

	env := NewEnvelope()
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
	var _ Node = (*LLMNode)(nil)
}

// countingMockLLMClient fails a specified number of times before succeeding.
type countingMockLLMClient struct {
	failCount       int
	successResponse LLMResponse
	callCount       *int
}

func (m *countingMockLLMClient) Complete(ctx context.Context, req LLMRequest) (LLMResponse, error) {
	*m.callCount++
	if *m.callCount <= m.failCount {
		return LLMResponse{}, errors.New("temporary failure")
	}
	return m.successResponse, nil
}

// slowMockLLMClient simulates a slow LLM call for timeout testing.
type slowMockLLMClient struct {
	delay time.Duration
}

func (m *slowMockLLMClient) Complete(ctx context.Context, req LLMRequest) (LLMResponse, error) {
	select {
	case <-ctx.Done():
		return LLMResponse{}, ctx.Err()
	case <-time.After(m.delay):
		return LLMResponse{Text: "OK"}, nil
	}
}
