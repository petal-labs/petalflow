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
