package irisadapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/petal-labs/iris/core"
	"github.com/petal-labs/petalflow"
)

// mockProvider is a mock implementation of core.Provider for testing.
type mockProvider struct {
	id           string
	chatResponse *core.ChatResponse
	chatError    error
}

func (m *mockProvider) ID() string {
	return m.id
}

func (m *mockProvider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	if m.chatError != nil {
		return nil, m.chatError
	}
	return m.chatResponse, nil
}

func (m *mockProvider) StreamChat(ctx context.Context, req *core.ChatRequest) (*core.ChatStream, error) {
	return nil, nil // not used in tests
}

func (m *mockProvider) Models() []core.ModelInfo {
	return []core.ModelInfo{{ID: "mock-model"}}
}

func (m *mockProvider) Supports(feature core.Feature) bool {
	return feature == core.FeatureChat
}

func TestNewProviderAdapter(t *testing.T) {
	mock := &mockProvider{id: "mock"}
	adapter := NewProviderAdapter(mock)

	if adapter == nil {
		t.Fatal("expected adapter to be non-nil")
	}
	if adapter.ProviderID() != "mock" {
		t.Errorf("expected provider ID 'mock', got %q", adapter.ProviderID())
	}
}

func TestProviderAdapter_Complete_SimplePrompt(t *testing.T) {
	mock := &mockProvider{
		id: "mock",
		chatResponse: &core.ChatResponse{
			ID:     "resp-123",
			Model:  "mock-model",
			Output: "Hello, world!",
			Usage: core.TokenUsage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
	}

	adapter := NewProviderAdapter(mock)

	resp, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model:     "mock-model",
		System:    "You are helpful",
		InputText: "Say hello",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello, world!" {
		t.Errorf("expected text 'Hello, world!', got %q", resp.Text)
	}
	if resp.Provider != "mock" {
		t.Errorf("expected provider 'mock', got %q", resp.Provider)
	}
	if resp.Model != "mock-model" {
		t.Errorf("expected model 'mock-model', got %q", resp.Model)
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("expected input tokens 10, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Errorf("expected output tokens 5, got %d", resp.Usage.OutputTokens)
	}
}

func TestProviderAdapter_Complete_WithMessages(t *testing.T) {
	mock := &mockProvider{
		id: "mock",
		chatResponse: &core.ChatResponse{
			Output: "I'm doing well, thanks!",
		},
	}

	adapter := NewProviderAdapter(mock)

	resp, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model:  "mock-model",
		System: "You are helpful",
		Messages: []petalflow.LLMMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
			{Role: "user", Content: "How are you?"},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "I'm doing well, thanks!" {
		t.Errorf("unexpected response text: %q", resp.Text)
	}

	// Check that messages include the response
	if len(resp.Messages) != 4 {
		t.Errorf("expected 4 messages (3 input + 1 response), got %d", len(resp.Messages))
	}
}

func TestProviderAdapter_Complete_WithToolCalls(t *testing.T) {
	args := map[string]string{"query": "weather"}
	argsJSON, _ := json.Marshal(args)

	mock := &mockProvider{
		id: "mock",
		chatResponse: &core.ChatResponse{
			Output: "",
			ToolCalls: []core.ToolCall{
				{
					ID:        "call-1",
					Name:      "get_weather",
					Arguments: argsJSON,
				},
			},
		},
	}

	adapter := NewProviderAdapter(mock)

	resp, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model:     "mock-model",
		InputText: "What's the weather?",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "call-1" {
		t.Errorf("expected tool call ID 'call-1', got %q", resp.ToolCalls[0].ID)
	}
	if resp.ToolCalls[0].Name != "get_weather" {
		t.Errorf("expected tool name 'get_weather', got %q", resp.ToolCalls[0].Name)
	}
	if resp.ToolCalls[0].Arguments["query"] != "weather" {
		t.Errorf("expected query argument 'weather', got %v", resp.ToolCalls[0].Arguments["query"])
	}
}

func TestProviderAdapter_Complete_WithJSONSchema(t *testing.T) {
	mock := &mockProvider{
		id: "mock",
		chatResponse: &core.ChatResponse{
			Output: `{"name": "Alice", "age": 30}`,
		},
	}

	adapter := NewProviderAdapter(mock)

	resp, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model:     "mock-model",
		InputText: "Get user info",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
				"age":  map[string]any{"type": "integer"},
			},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.JSON == nil {
		t.Fatal("expected JSON to be parsed")
	}
	if resp.JSON["name"] != "Alice" {
		t.Errorf("expected name 'Alice', got %v", resp.JSON["name"])
	}
	// JSON unmarshals numbers as float64
	if resp.JSON["age"] != float64(30) {
		t.Errorf("expected age 30, got %v", resp.JSON["age"])
	}
}

func TestProviderAdapter_Complete_Error(t *testing.T) {
	mock := &mockProvider{
		id:        "mock",
		chatError: context.DeadlineExceeded,
	}

	adapter := NewProviderAdapter(mock)

	_, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model:     "mock-model",
		InputText: "Hello",
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestProviderAdapter_Complete_ResponseMeta(t *testing.T) {
	mock := &mockProvider{
		id: "mock",
		chatResponse: &core.ChatResponse{
			ID:     "resp-456",
			Output: "Test",
		},
	}

	adapter := NewProviderAdapter(mock)

	resp, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model:     "mock-model",
		InputText: "Test",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Meta["response_id"] != "resp-456" {
		t.Errorf("expected response_id 'resp-456', got %v", resp.Meta["response_id"])
	}
}

func TestProviderAdapter_Complete_Temperature(t *testing.T) {
	var capturedReq *core.ChatRequest

	adapter := &ProviderAdapter{
		provider: &capturingProvider{
			mockProvider: &mockProvider{
				id: "mock",
				chatResponse: &core.ChatResponse{
					Output: "Test",
				},
			},
			capture: func(req *core.ChatRequest) { capturedReq = req },
		},
	}

	temp := 0.7
	_, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model:       "mock-model",
		InputText:   "Test",
		Temperature: &temp,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedReq == nil || capturedReq.Temperature == nil {
		t.Fatal("expected temperature to be set")
	}
	if *capturedReq.Temperature != float32(0.7) {
		t.Errorf("expected temperature 0.7, got %v", *capturedReq.Temperature)
	}
}

func TestToRole(t *testing.T) {
	tests := []struct {
		input    string
		expected core.Role
	}{
		{"system", core.RoleSystem},
		{"user", core.RoleUser},
		{"assistant", core.RoleAssistant},
		{"unknown", core.RoleUser}, // default
	}

	for _, tt := range tests {
		result := toRole(tt.input)
		if result != tt.expected {
			t.Errorf("toRole(%q) = %v, expected %v", tt.input, result, tt.expected)
		}
	}
}

func TestLLMClient_InterfaceCompliance(t *testing.T) {
	var _ petalflow.LLMClient = (*ProviderAdapter)(nil)
}

// capturingProvider wraps a mock provider to capture requests.
type capturingProvider struct {
	*mockProvider
	capture func(req *core.ChatRequest)
}

func (c *capturingProvider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	if c.capture != nil {
		c.capture(req)
	}
	return c.mockProvider.Chat(ctx, req)
}

func TestToRole_Tool(t *testing.T) {
	result := toRole("tool")
	if result != core.RoleTool {
		t.Errorf("toRole(\"tool\") = %v, expected %v", result, core.RoleTool)
	}
}

func TestProviderAdapter_Complete_WithToolResults(t *testing.T) {
	var capturedReq *core.ChatRequest

	adapter := &ProviderAdapter{
		provider: &capturingProvider{
			mockProvider: &mockProvider{
				id: "mock",
				chatResponse: &core.ChatResponse{
					Output: "The weather in NYC is sunny and 72°F.",
				},
			},
			capture: func(req *core.ChatRequest) { capturedReq = req },
		},
	}

	_, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model: "mock-model",
		Messages: []petalflow.LLMMessage{
			{Role: "user", Content: "What's the weather in NYC?"},
			{
				Role: "assistant",
				ToolCalls: []petalflow.LLMToolCall{
					{ID: "call-1", Name: "get_weather", Arguments: map[string]any{"city": "NYC"}},
				},
			},
			{
				Role: "tool",
				ToolResults: []petalflow.LLMToolResult{
					{CallID: "call-1", Content: map[string]any{"temp": 72, "condition": "sunny"}, IsError: false},
				},
			},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify tool results were passed through
	if capturedReq == nil {
		t.Fatal("expected request to be captured")
	}
	if len(capturedReq.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(capturedReq.Messages))
	}

	// Check the tool message has tool results
	toolMsg := capturedReq.Messages[2]
	if toolMsg.Role != core.RoleTool {
		t.Errorf("expected tool role, got %v", toolMsg.Role)
	}
	if len(toolMsg.ToolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(toolMsg.ToolResults))
	}
	if toolMsg.ToolResults[0].CallID != "call-1" {
		t.Errorf("expected CallID 'call-1', got %q", toolMsg.ToolResults[0].CallID)
	}

	// Check the assistant message has tool calls
	assistantMsg := capturedReq.Messages[1]
	if len(assistantMsg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(assistantMsg.ToolCalls))
	}
	if assistantMsg.ToolCalls[0].ID != "call-1" {
		t.Errorf("expected tool call ID 'call-1', got %q", assistantMsg.ToolCalls[0].ID)
	}
}

func TestProviderAdapter_Complete_WithReasoning(t *testing.T) {
	mock := &mockProvider{
		id: "mock",
		chatResponse: &core.ChatResponse{
			Output: "42",
			Reasoning: &core.ReasoningOutput{
				ID:      "reason-1",
				Summary: []string{"Analyzed the question", "Applied logic", "Determined answer"},
			},
		},
	}

	adapter := NewProviderAdapter(mock)

	resp, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model:     "mock-model",
		InputText: "What is the meaning of life?",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Reasoning == nil {
		t.Fatal("expected reasoning to be populated")
	}
	if resp.Reasoning.ID != "reason-1" {
		t.Errorf("expected reasoning ID 'reason-1', got %q", resp.Reasoning.ID)
	}
	if len(resp.Reasoning.Summary) != 3 {
		t.Errorf("expected 3 summary items, got %d", len(resp.Reasoning.Summary))
	}
	if resp.Reasoning.Summary[0] != "Analyzed the question" {
		t.Errorf("unexpected first summary item: %q", resp.Reasoning.Summary[0])
	}
}

func TestProviderAdapter_Complete_WithInstructions(t *testing.T) {
	var capturedReq *core.ChatRequest

	adapter := &ProviderAdapter{
		provider: &capturingProvider{
			mockProvider: &mockProvider{
				id: "mock",
				chatResponse: &core.ChatResponse{
					Output: "Hello!",
				},
			},
			capture: func(req *core.ChatRequest) { capturedReq = req },
		},
	}

	_, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model:        "mock-model",
		Instructions: "Be concise and helpful. Always respond in JSON format.",
		InputText:    "Hi",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedReq == nil {
		t.Fatal("expected request to be captured")
	}
	if capturedReq.Instructions != "Be concise and helpful. Always respond in JSON format." {
		t.Errorf("expected instructions to be passed through, got %q", capturedReq.Instructions)
	}
}

func TestProviderAdapter_Complete_WithStatus(t *testing.T) {
	mock := &mockProvider{
		id: "mock",
		chatResponse: &core.ChatResponse{
			Output: "Done",
			Status: "completed",
		},
	}

	adapter := NewProviderAdapter(mock)

	resp, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model:     "mock-model",
		InputText: "Do something",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "completed" {
		t.Errorf("expected status 'completed', got %q", resp.Status)
	}
}

func TestProviderAdapter_Complete_ToolCallsInMessage(t *testing.T) {
	args := map[string]string{"query": "weather"}
	argsJSON, _ := json.Marshal(args)

	mock := &mockProvider{
		id: "mock",
		chatResponse: &core.ChatResponse{
			Output: "",
			ToolCalls: []core.ToolCall{
				{ID: "call-1", Name: "get_weather", Arguments: argsJSON},
			},
		},
	}

	adapter := NewProviderAdapter(mock)

	resp, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model:     "mock-model",
		InputText: "What's the weather?",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify that the assistant message contains the tool calls
	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}
	assistantMsg := resp.Messages[0]
	if assistantMsg.Role != "assistant" {
		t.Errorf("expected assistant role, got %q", assistantMsg.Role)
	}
	if len(assistantMsg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call in message, got %d", len(assistantMsg.ToolCalls))
	}
	if assistantMsg.ToolCalls[0].ID != "call-1" {
		t.Errorf("expected tool call ID 'call-1', got %q", assistantMsg.ToolCalls[0].ID)
	}
}
