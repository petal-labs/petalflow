package llmprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	iriscore "github.com/petal-labs/iris/core"

	"github.com/petal-labs/petalflow/core"
)

// mockProvider implements iriscore.Provider for testing.
type mockProvider struct {
	id           string
	chatResponse *iriscore.ChatResponse
	chatError    error
	capturedReq  *iriscore.ChatRequest
}

func (m *mockProvider) ID() string { return m.id }

func (m *mockProvider) Chat(_ context.Context, req *iriscore.ChatRequest) (*iriscore.ChatResponse, error) {
	m.capturedReq = req
	if m.chatError != nil {
		return nil, m.chatError
	}
	return m.chatResponse, nil
}

func (m *mockProvider) StreamChat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	return nil, nil
}

func (m *mockProvider) Models() []iriscore.ModelInfo {
	return []iriscore.ModelInfo{{ID: "mock-model"}}
}

func (m *mockProvider) Supports(f iriscore.Feature) bool {
	return f == iriscore.FeatureChat
}

// --- interface compliance ---

func TestInterfaceCompliance(t *testing.T) {
	var _ core.LLMClient = (*irisAdapter)(nil)
}

// --- Complete tests ---

func TestComplete_SimplePrompt(t *testing.T) {
	mock := &mockProvider{
		id: "test-provider",
		chatResponse: &iriscore.ChatResponse{
			ID:     "resp-1",
			Model:  "claude-3",
			Output: "Hello from LLM",
			Usage: iriscore.TokenUsage{
				PromptTokens:     12,
				CompletionTokens: 8,
				TotalTokens:      20,
			},
		},
	}
	adapter := &irisAdapter{provider: mock}

	resp, err := adapter.Complete(context.Background(), core.LLMRequest{
		Model:     "claude-3",
		System:    "You are helpful",
		InputText: "Say hello",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello from LLM" {
		t.Errorf("Text = %q, want %q", resp.Text, "Hello from LLM")
	}
	if resp.Provider != "test-provider" {
		t.Errorf("Provider = %q, want %q", resp.Provider, "test-provider")
	}
	if resp.Model != "claude-3" {
		t.Errorf("Model = %q, want %q", resp.Model, "claude-3")
	}
	if resp.Usage.InputTokens != 12 {
		t.Errorf("InputTokens = %d, want 12", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 8 {
		t.Errorf("OutputTokens = %d, want 8", resp.Usage.OutputTokens)
	}
	if resp.Usage.TotalTokens != 20 {
		t.Errorf("TotalTokens = %d, want 20", resp.Usage.TotalTokens)
	}
	if resp.Meta["response_id"] != "resp-1" {
		t.Errorf("Meta[response_id] = %v, want %q", resp.Meta["response_id"], "resp-1")
	}

	// Verify iris request construction
	if mock.capturedReq == nil {
		t.Fatal("expected request to be captured")
	}
	if len(mock.capturedReq.Messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(mock.capturedReq.Messages))
	}
	if mock.capturedReq.Messages[0].Role != iriscore.RoleSystem {
		t.Errorf("first message role = %v, want system", mock.capturedReq.Messages[0].Role)
	}
	if mock.capturedReq.Messages[1].Content != "Say hello" {
		t.Errorf("user message content = %q, want %q", mock.capturedReq.Messages[1].Content, "Say hello")
	}
}

func TestComplete_TemperatureAndMaxTokens(t *testing.T) {
	mock := &mockProvider{
		id:           "test",
		chatResponse: &iriscore.ChatResponse{Output: "ok"},
	}
	adapter := &irisAdapter{provider: mock}

	temp := 0.7
	maxTok := 256
	_, err := adapter.Complete(context.Background(), core.LLMRequest{
		Model:       "m",
		InputText:   "test",
		Temperature: &temp,
		MaxTokens:   &maxTok,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.capturedReq.Temperature == nil {
		t.Fatal("expected temperature to be set")
	}
	if *mock.capturedReq.Temperature != float32(0.7) {
		t.Errorf("temperature = %v, want 0.7", *mock.capturedReq.Temperature)
	}
	if mock.capturedReq.MaxTokens == nil || *mock.capturedReq.MaxTokens != 256 {
		t.Errorf("max_tokens = %v, want 256", mock.capturedReq.MaxTokens)
	}
}

func TestComplete_ToolCalls(t *testing.T) {
	argsJSON, _ := json.Marshal(map[string]string{"city": "NYC"})

	mock := &mockProvider{
		id: "test",
		chatResponse: &iriscore.ChatResponse{
			Output: "",
			ToolCalls: []iriscore.ToolCall{
				{ID: "call-1", Name: "get_weather", Arguments: argsJSON},
			},
		},
	}
	adapter := &irisAdapter{provider: mock}

	resp, err := adapter.Complete(context.Background(), core.LLMRequest{
		Model:     "m",
		InputText: "weather?",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call-1" {
		t.Errorf("ToolCall.ID = %q, want %q", tc.ID, "call-1")
	}
	if tc.Name != "get_weather" {
		t.Errorf("ToolCall.Name = %q, want %q", tc.Name, "get_weather")
	}
	if tc.Arguments["city"] != "NYC" {
		t.Errorf("ToolCall.Arguments[city] = %v, want %q", tc.Arguments["city"], "NYC")
	}
}

func TestComplete_ErrorPropagation(t *testing.T) {
	mock := &mockProvider{
		id:        "test",
		chatError: fmt.Errorf("rate limited"),
	}
	adapter := &irisAdapter{provider: mock}

	_, err := adapter.Complete(context.Background(), core.LLMRequest{
		Model:     "m",
		InputText: "hello",
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "provider chat failed: rate limited" {
		t.Errorf("error = %q, want prefix 'provider chat failed:'", got)
	}
}

func TestComplete_Reasoning(t *testing.T) {
	mock := &mockProvider{
		id: "test",
		chatResponse: &iriscore.ChatResponse{
			Output: "42",
			Reasoning: &iriscore.ReasoningOutput{
				ID:      "reason-1",
				Summary: []string{"Step 1", "Step 2"},
			},
		},
	}
	adapter := &irisAdapter{provider: mock}

	resp, err := adapter.Complete(context.Background(), core.LLMRequest{
		Model:     "m",
		InputText: "meaning?",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Reasoning == nil {
		t.Fatal("expected reasoning output")
	}
	if resp.Reasoning.ID != "reason-1" {
		t.Errorf("Reasoning.ID = %q, want %q", resp.Reasoning.ID, "reason-1")
	}
	if len(resp.Reasoning.Summary) != 2 {
		t.Errorf("Reasoning.Summary len = %d, want 2", len(resp.Reasoning.Summary))
	}
}

func TestComplete_JSONSchema(t *testing.T) {
	mock := &mockProvider{
		id: "test",
		chatResponse: &iriscore.ChatResponse{
			Output: `{"name":"Alice","age":30}`,
		},
	}
	adapter := &irisAdapter{provider: mock}

	resp, err := adapter.Complete(context.Background(), core.LLMRequest{
		Model:     "m",
		InputText: "user info",
		JSONSchema: map[string]any{
			"type": "object",
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.JSON == nil {
		t.Fatal("expected JSON to be parsed")
	}
	if resp.JSON["name"] != "Alice" {
		t.Errorf("JSON[name] = %v, want %q", resp.JSON["name"], "Alice")
	}
}

func TestComplete_MessagesPassthrough(t *testing.T) {
	mock := &mockProvider{
		id:           "test",
		chatResponse: &iriscore.ChatResponse{Output: "ok"},
	}
	adapter := &irisAdapter{provider: mock}

	resp, err := adapter.Complete(context.Background(), core.LLMRequest{
		Model: "m",
		Messages: []core.LLMMessage{
			{Role: "user", Content: "Hi"},
			{Role: "assistant", Content: "Hello"},
		},
		InputText: "Thanks",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 input messages + 1 assistant response
	if len(resp.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(resp.Messages))
	}
	if resp.Messages[2].Role != "assistant" {
		t.Errorf("last message role = %q, want %q", resp.Messages[2].Role, "assistant")
	}

	// Verify iris request: system omitted, 2 conversation + 1 InputText user msg
	if len(mock.capturedReq.Messages) != 3 {
		t.Fatalf("expected 3 iris messages, got %d", len(mock.capturedReq.Messages))
	}
}

func TestToIrisRole(t *testing.T) {
	tests := []struct {
		input string
		want  iriscore.Role
	}{
		{"system", iriscore.RoleSystem},
		{"user", iriscore.RoleUser},
		{"assistant", iriscore.RoleAssistant},
		{"tool", iriscore.RoleTool},
		{"unknown", iriscore.RoleUser},
	}
	for _, tt := range tests {
		if got := toIrisRole(tt.input); got != tt.want {
			t.Errorf("toIrisRole(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
