package irisadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petal-labs/iris/core"
	"github.com/petal-labs/petalflow"
)

// streamingMockProvider extends mockProvider with configurable StreamChat behavior.
type streamingMockProvider struct {
	mockProvider
	streamFn func(ctx context.Context, req *core.ChatRequest) (*core.ChatStream, error)
}

func (m *streamingMockProvider) StreamChat(ctx context.Context, req *core.ChatRequest) (*core.ChatStream, error) {
	if m.streamFn != nil {
		return m.streamFn(ctx, req)
	}
	return nil, errors.New("StreamChat not configured")
}

// newMockStream creates a ChatStream from a list of deltas, an optional final response, and an optional error.
func newMockStream(deltas []string, final *core.ChatResponse, streamErr error) *core.ChatStream {
	chunkCh := make(chan core.ChatChunk, len(deltas))
	errCh := make(chan error, 1)
	finalCh := make(chan *core.ChatResponse, 1)

	for _, d := range deltas {
		chunkCh <- core.ChatChunk{Delta: d}
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

	return &core.ChatStream{
		Ch:    chunkCh,
		Err:   errCh,
		Final: finalCh,
	}
}

func TestProviderAdapter_CompleteStream_Basic(t *testing.T) {
	mock := &streamingMockProvider{
		mockProvider: mockProvider{id: "mock"},
		streamFn: func(ctx context.Context, req *core.ChatRequest) (*core.ChatStream, error) {
			return newMockStream(
				[]string{"Hello", ", ", "world", "!"},
				&core.ChatResponse{
					Model: "mock-model",
					Usage: core.TokenUsage{
						PromptTokens:     10,
						CompletionTokens: 4,
						TotalTokens:      14,
					},
				},
				nil,
			), nil
		},
	}

	adapter := NewProviderAdapter(mock)

	ch, err := adapter.CompleteStream(context.Background(), petalflow.LLMRequest{
		Model:     "mock-model",
		InputText: "Say hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks []petalflow.StreamChunk
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

func TestProviderAdapter_CompleteStream_SetupError(t *testing.T) {
	expectedErr := errors.New("connection refused")

	mock := &streamingMockProvider{
		mockProvider: mockProvider{id: "mock"},
		streamFn: func(ctx context.Context, req *core.ChatRequest) (*core.ChatStream, error) {
			return nil, expectedErr
		},
	}

	adapter := NewProviderAdapter(mock)

	_, err := adapter.CompleteStream(context.Background(), petalflow.LLMRequest{
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

func TestProviderAdapter_CompleteStream_StreamError(t *testing.T) {
	streamErr := errors.New("stream interrupted")

	mock := &streamingMockProvider{
		mockProvider: mockProvider{id: "mock"},
		streamFn: func(ctx context.Context, req *core.ChatRequest) (*core.ChatStream, error) {
			return newMockStream(
				[]string{"Hello"},
				nil,
				streamErr,
			), nil
		},
	}

	adapter := NewProviderAdapter(mock)

	ch, err := adapter.CompleteStream(context.Background(), petalflow.LLMRequest{
		Model:     "mock-model",
		InputText: "Hello",
	})
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}

	var chunks []petalflow.StreamChunk
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

func TestProviderAdapter_CompleteStream_EmptyStream(t *testing.T) {
	mock := &streamingMockProvider{
		mockProvider: mockProvider{id: "mock"},
		streamFn: func(ctx context.Context, req *core.ChatRequest) (*core.ChatStream, error) {
			return newMockStream(
				nil, // no deltas
				&core.ChatResponse{
					Model: "mock-model",
					Usage: core.TokenUsage{
						PromptTokens:     5,
						CompletionTokens: 0,
						TotalTokens:      5,
					},
				},
				nil,
			), nil
		},
	}

	adapter := NewProviderAdapter(mock)

	ch, err := adapter.CompleteStream(context.Background(), petalflow.LLMRequest{
		Model:     "mock-model",
		InputText: "Hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks []petalflow.StreamChunk
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

func TestProviderAdapter_CompleteStream_ContextCancellation(t *testing.T) {
	// Create a stream that blocks until context is canceled
	mock := &streamingMockProvider{
		mockProvider: mockProvider{id: "mock"},
		streamFn: func(ctx context.Context, req *core.ChatRequest) (*core.ChatStream, error) {
			chunkCh := make(chan core.ChatChunk)
			errCh := make(chan error, 1)
			finalCh := make(chan *core.ChatResponse, 1)

			// This goroutine simulates a slow stream
			go func() {
				defer close(chunkCh)
				defer close(errCh)
				defer close(finalCh)

				// Send one chunk then block
				select {
				case chunkCh <- core.ChatChunk{Delta: "Start"}:
				case <-ctx.Done():
					return
				}

				// Block until context cancels
				<-ctx.Done()
			}()

			return &core.ChatStream{
				Ch:    chunkCh,
				Err:   errCh,
				Final: finalCh,
			}, nil
		},
	}

	adapter := NewProviderAdapter(mock)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	ch, err := adapter.CompleteStream(ctx, petalflow.LLMRequest{
		Model:     "mock-model",
		InputText: "Hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks []petalflow.StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// The last chunk should indicate context cancellation
	last := chunks[len(chunks)-1]
	if !last.Done {
		t.Error("last chunk should have Done=true")
	}
	if last.Error == nil {
		t.Error("last chunk should have context error")
	}
}

func TestProviderAdapter_CompleteStream_NoFinalResponse(t *testing.T) {
	mock := &streamingMockProvider{
		mockProvider: mockProvider{id: "mock"},
		streamFn: func(ctx context.Context, req *core.ChatRequest) (*core.ChatStream, error) {
			return newMockStream(
				[]string{"Hello"},
				nil, // no final response
				nil, // no error
			), nil
		},
	}

	adapter := NewProviderAdapter(mock)

	ch, err := adapter.CompleteStream(context.Background(), petalflow.LLMRequest{
		Model:     "mock-model",
		InputText: "Hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks []petalflow.StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}

	// Should have 1 delta + 1 final
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}

	// Final chunk should have Done=true but nil Usage (no final response)
	final := chunks[1]
	if !final.Done {
		t.Error("expected Done=true")
	}
	if final.Usage != nil {
		t.Error("expected nil Usage when no final response")
	}
	if final.Accumulated != "Hello" {
		t.Errorf("expected accumulated 'Hello', got %q", final.Accumulated)
	}
}

func TestStreamingLLMClient_InterfaceCompliance(t *testing.T) {
	var _ petalflow.StreamingLLMClient = (*ProviderAdapter)(nil)
}
