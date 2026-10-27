package llmprovider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/capability"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
)

// recordingClient records whether it was called (preflight must happen
// before the provider request).
type recordingClient struct {
	called bool
}

func (c *recordingClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	c.called = true
	return core.LLMResponse{Text: "ok"}, nil
}

func toolsRequest(model string) core.LLMRequest {
	return core.LLMRequest{
		Model: model,
		Tools: []core.LLMToolDefinition{{Name: "search", Parameters: map[string]any{}}},
	}
}

func TestCapabilityAwareClientRejectsBeforeDispatch(t *testing.T) {
	// text-embedding-3-small registers embeddings only: a tools request
	// must be rejected without the inner client being called.
	inner := &recordingClient{}
	client := NewCapabilityAwareClient(inner, capability.NewRegistry(), "openai")

	_, err := client.Complete(context.Background(), toolsRequest("text-embedding-3-small"))
	if err == nil {
		t.Fatal("expected preflight rejection")
	}
	if inner.called {
		t.Fatal("inner client must not be called when preflight fails")
	}
	if !errors.Is(err, capability.ErrUnsupportedCapability) {
		t.Errorf("error should wrap capability.ErrUnsupportedCapability: %v", err)
	}
	if !strings.Contains(err.Error(), "lacks [tools]") {
		t.Errorf("error should name missing capabilities: %v", err)
	}
}

func TestCapabilityAwareClientPassesSupportedRequest(t *testing.T) {
	inner := &recordingClient{}
	client := NewCapabilityAwareClient(inner, capability.NewRegistry(), "openai")

	resp, err := client.Complete(context.Background(), toolsRequest("gpt-4o"))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "ok" {
		t.Errorf("response = %v", resp)
	}
	if !inner.called {
		t.Error("inner client should be called for supported requests")
	}
}

func TestCapabilityAwareClientUnknownModelFailsClosed(t *testing.T) {
	inner := &recordingClient{}
	client := NewCapabilityAwareClient(inner, capability.NewRegistry(), "acme")

	_, err := client.Complete(context.Background(), core.LLMRequest{
		Model:           "private-model",
		InputText:       "hi",
		ReasoningEffort: "high",
	})
	if err == nil {
		t.Fatal("unknown model should fail closed on reasoning")
	}
	if inner.called {
		t.Error("inner client must not be called")
	}
	if !strings.Contains(err.Error(), "unknown model") {
		t.Errorf("error should explain fail-closed: %v", err)
	}
}

func TestCapabilityAwareStreamingRequiresStreaming(t *testing.T) {
	inner := &fakeStreamingClient{client: &recordingClient{}}
	// Anthropic provider default has streaming; use a provider/model
	// combination without it.
	registry := capability.NewRegistry()
	registry.ApplyConfig("acme", map[string][]string{"model": {"tools"}})
	client := NewCapabilityAwareStreamingClient(inner, registry, "acme")

	if _, err := client.CompleteStream(context.Background(), core.LLMRequest{Model: "model"}); err == nil {
		t.Fatal("expected streaming capability rejection")
	}
	if inner.called {
		t.Error("inner client must not be called")
	}

	// With the streaming capability configured, the call proceeds.
	registry.ApplyConfig("acme", map[string][]string{"model": {"streaming"}})
	if _, err := client.CompleteStream(context.Background(), core.LLMRequest{Model: "model"}); err != nil {
		t.Errorf("streaming-capable model should pass: %v", err)
	}
}

type fakeStreamingClient struct {
	client *recordingClient
	called bool
}

func (c *fakeStreamingClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	return c.client.Complete(ctx, req)
}

func (c *fakeStreamingClient) CompleteStream(ctx context.Context, req core.LLMRequest) (<-chan core.StreamChunk, error) {
	c.called = true
	ch := make(chan core.StreamChunk, 1)
	ch <- core.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

func TestCapabilityAwareClientNodeHintInError(t *testing.T) {
	client := NewCapabilityAwareClient(&recordingClient{}, capability.NewRegistry(), "openai")
	req := toolsRequest("text-embedding-3-small")
	req.Meta = map[string]any{"node_id": "answer"}

	_, err := client.Complete(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), `node "answer"`) {
		t.Errorf("error should name the node from request meta: %v", err)
	}
}

func TestNewCapabilityRegistryWarnings(t *testing.T) {
	registry, warnings := NewCapabilityRegistry("acme", hydrate.ProviderConfig{
		Capabilities: map[string][]string{
			"model-a": {"tools", "not-a-capability"},
		},
	})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "not-a-capability") {
		t.Errorf("warnings = %v", warnings)
	}
	if caps, known := registry.Model("acme", "model-a"); !known || !caps.Has(capability.CapabilityTools) {
		t.Errorf("valid capabilities should register: %v known=%v", caps.Names(), known)
	}
}

func TestNewClientDetailedWarnings(t *testing.T) {
	client, warnings, err := NewClientDetailed("openai", hydrate.ProviderConfig{
		APIKey: "test-key",
		Capabilities: map[string][]string{
			"gpt-4o": {"tools", "nonsense"},
		},
	})
	if err != nil {
		t.Fatalf("NewClientDetailed: %v", err)
	}
	if client == nil {
		t.Fatal("nil client")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "nonsense") {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestNewClientDropsWarnings(t *testing.T) {
	client, err := NewClient("openai", hydrate.ProviderConfig{APIKey: "test-key"})
	if err != nil || client == nil {
		t.Fatalf("NewClient: %v", err)
	}
}
