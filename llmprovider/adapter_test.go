package llmprovider

import (
	"context"
	"encoding/json"
	"errors"
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

// --- Streaming tests ---

// streamingMockProvider extends mockProvider with configurable StreamChat behavior.
type streamingMockProvider struct {
	mockProvider
	streamFn func(ctx context.Context, req *iriscore.ChatRequest) (*iriscore.ChatStream, error)
}

func (m *streamingMockProvider) StreamChat(ctx context.Context, req *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	if m.streamFn != nil {
		return m.streamFn(ctx, req)
	}
	return nil, errors.New("StreamChat not configured")
}

// newMockStream creates a ChatStream from a list of deltas, an optional final response, and an optional error.
func newMockStream(deltas []string, final *iriscore.ChatResponse, streamErr error) *iriscore.ChatStream {
	chunkCh := make(chan iriscore.ChatChunk, len(deltas))
	errCh := make(chan error, 1)
	finalCh := make(chan *iriscore.ChatResponse, 1)

	for _, d := range deltas {
		chunkCh <- iriscore.ChatChunk{Delta: d}
	}
	close(chunkCh)

	if streamErr != nil {
		errCh <- streamErr
	}
	close(errCh)

	if final != nil {
		finalCh <- final
	}
	close(finalCh)

	return &iriscore.ChatStream{
		Ch:    chunkCh,
		Err:   errCh,
		Final: finalCh,
	}
}

func TestCompleteStream_Basic(t *testing.T) {
	mock := &streamingMockProvider{
		mockProvider: mockProvider{id: "mock"},
		streamFn: func(ctx context.Context, req *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
			return newMockStream(
				[]string{"Hello", ", ", "world", "!"},
				&iriscore.ChatResponse{
					Model: "mock-model",
					Usage: iriscore.TokenUsage{
						PromptTokens:     10,
						CompletionTokens: 4,
						TotalTokens:      14,
					},
				},
				nil,
			), nil
		},
	}

	adapter := &irisAdapter{provider: mock}

	ch, err := adapter.CompleteStream(context.Background(), core.LLMRequest{
		Model:     "mock-model",
		InputText: "Say hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks []core.StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}

	// We expect 4 delta chunks + 1 final Done chunk = 5 total
	if len(chunks) != 5 {
		t.Fatalf("expected 5 chunks, got %d", len(chunks))
	}

	// Verify delta chunks
	expectedDeltas := []string{"Hello", ", ", "world", "!"}
	for i, expected := range expectedDeltas {
		if chunks[i].Delta != expected {
			t.Errorf("chunk %d: expected delta %q, got %q", i, expected, chunks[i].Delta)
		}
		if chunks[i].Index != i {
			t.Errorf("chunk %d: expected index %d, got %d", i, i, chunks[i].Index)
		}
		if chunks[i].Done {
			t.Errorf("chunk %d: should not be done", i)
		}
	}

	// Verify accumulated text builds up correctly
	if chunks[0].Accumulated != "Hello" {
		t.Errorf("chunk 0: expected accumulated 'Hello', got %q", chunks[0].Accumulated)
	}
	if chunks[1].Accumulated != "Hello, " {
		t.Errorf("chunk 1: expected accumulated 'Hello, ', got %q", chunks[1].Accumulated)
	}
	if chunks[3].Accumulated != "Hello, world!" {
		t.Errorf("chunk 3: expected accumulated 'Hello, world!', got %q", chunks[3].Accumulated)
	}

	// Verify final chunk
	final := chunks[4]
	if !final.Done {
		t.Error("final chunk should have Done=true")
	}
	if final.Accumulated != "Hello, world!" {
		t.Errorf("final chunk: expected accumulated 'Hello, world!', got %q", final.Accumulated)
	}
	if final.Usage == nil {
		t.Fatal("final chunk should have Usage")
	}
	if final.Usage.InputTokens != 10 {
		t.Errorf("expected input tokens 10, got %d", final.Usage.InputTokens)
	}
	if final.Usage.OutputTokens != 4 {
		t.Errorf("expected output tokens 4, got %d", final.Usage.OutputTokens)
	}
	if final.Usage.TotalTokens != 14 {
		t.Errorf("expected total tokens 14, got %d", final.Usage.TotalTokens)
	}
	if final.Error != nil {
		t.Errorf("final chunk should have no error, got %v", final.Error)
	}
}

func TestCompleteStream_SetupError(t *testing.T) {
	expectedErr := errors.New("connection refused")

	mock := &streamingMockProvider{
		mockProvider: mockProvider{id: "mock"},
		streamFn: func(ctx context.Context, req *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
			return nil, expectedErr
		},
	}

	adapter := &irisAdapter{provider: mock}

	_, err := adapter.CompleteStream(context.Background(), core.LLMRequest{
		Model:     "mock-model",
		InputText: "Hello",
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected wrapped error to contain %v, got %v", expectedErr, err)
	}
}

func TestCompleteStream_StreamError(t *testing.T) {
	streamErr := errors.New("stream interrupted")

	mock := &streamingMockProvider{
		mockProvider: mockProvider{id: "mock"},
		streamFn: func(ctx context.Context, req *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
			return newMockStream(
				[]string{"Hello"},
				nil,
				streamErr,
			), nil
		},
	}

	adapter := &irisAdapter{provider: mock}

	ch, err := adapter.CompleteStream(context.Background(), core.LLMRequest{
		Model:     "mock-model",
		InputText: "Hello",
	})
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}

	var chunks []core.StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}

	// Should have 1 delta chunk + 1 error/done chunk
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	// The last chunk should be Done with an error
	last := chunks[len(chunks)-1]
	if !last.Done {
		t.Error("last chunk should have Done=true")
	}
	if last.Error == nil {
		t.Error("last chunk should have an error")
	}
	if !errors.Is(last.Error, streamErr) {
		t.Errorf("expected stream error, got %v", last.Error)
	}
}

func TestCompleteStream_EmptyStream(t *testing.T) {
	mock := &streamingMockProvider{
		mockProvider: mockProvider{id: "mock"},
		streamFn: func(ctx context.Context, req *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
			return newMockStream(
				nil, // no deltas
				&iriscore.ChatResponse{
					Model: "mock-model",
					Usage: iriscore.TokenUsage{
						PromptTokens:     5,
						CompletionTokens: 0,
						TotalTokens:      5,
					},
				},
				nil,
			), nil
		},
	}

	adapter := &irisAdapter{provider: mock}

	ch, err := adapter.CompleteStream(context.Background(), core.LLMRequest{
		Model:     "mock-model",
		InputText: "Hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks []core.StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}

	// Should have exactly 1 final chunk
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	if !chunks[0].Done {
		t.Error("expected Done=true")
	}
	if chunks[0].Accumulated != "" {
		t.Errorf("expected empty accumulated, got %q", chunks[0].Accumulated)
	}
}

func TestStreamingLLMClient_InterfaceCompliance(t *testing.T) {
	var _ core.StreamingLLMClient = (*irisAdapter)(nil)
}
