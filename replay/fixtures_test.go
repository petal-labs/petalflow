package replay

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/petal-labs/petalflow/core"
)

func fixtureRecording() *Recording {
	return &Recording{
		Version: FormatVersion,
		RunID:   "run-1",
		Status:  "succeeded",
		Nodes: []NodeRecord{
			{
				NodeID: "answer",
				LLM: []LLMCallRecord{{
					NodeID:     "answer",
					Model:      "test-model",
					System:     "be helpful",
					InputText:  "question one",
					Completion: "recorded answer one",
					StopReason: "end_turn",
					Usage:      UsageRecord{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
				}},
			},
			{
				NodeID: "summarize",
				LLM: []LLMCallRecord{{
					NodeID:     "summarize",
					Model:      "test-model",
					InputText:  "question two",
					Completion: "recorded answer two",
				}},
			},
		},
	}
}

func llmRequest(input string) core.LLMRequest {
	return core.LLMRequest{Model: "test-model", InputText: input}
}

func llmRequestWithSystem(input, system string) core.LLMRequest {
	return core.LLMRequest{Model: "test-model", System: system, InputText: input}
}

func TestFixtureLLMClientFingerprintMatch(t *testing.T) {
	store := NewFixtureStore(fixtureRecording()) // full fidelity: prompts present
	client := NewFixtureLLMClient(store, FixtureMissFail, nil)

	resp, err := client.Complete(context.Background(), llmRequestWithSystem("question one", "be helpful"))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "recorded answer one" {
		t.Errorf("Text = %q", resp.Text)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}

func TestFixtureLLMClientRedactedOrderMatch(t *testing.T) {
	rec := fixtureRecording()
	// Simulate a redacted recording: only prompts are placeholders.
	for i := range rec.Nodes {
		for j := range rec.Nodes[i].LLM {
			call := &rec.Nodes[i].LLM[j]
			if call.System != "" {
				call.System = defaultPlaceholder
			}
			if call.InputText != "" {
				call.InputText = defaultPlaceholder
			}
			for k := range call.Messages {
				call.Messages[k].Content = defaultPlaceholder
			}
		}
	}

	client := NewFixtureLLMClient(NewFixtureStore(rec), FixtureMissFail, nil)

	// Requests replay in recorded order regardless of content.
	first, err := client.Complete(context.Background(), llmRequest("anything"))
	if err != nil {
		t.Fatalf("Complete 1: %v", err)
	}
	second, err := client.Complete(context.Background(), llmRequest("else"))
	if err != nil {
		t.Fatalf("Complete 2: %v", err)
	}
	if first.Text != "recorded answer one" || second.Text != "recorded answer two" {
		t.Errorf("order replay = %q, %q", first.Text, second.Text)
	}
}

func TestFixtureLLMClientMissFails(t *testing.T) {
	client := NewFixtureLLMClient(NewFixtureStore(fixtureRecording()), FixtureMissFail, nil)

	if _, err := client.Complete(context.Background(), llmRequestWithSystem("question one", "be helpful")); err != nil {
		t.Fatalf("Complete 1: %v", err)
	}
	if _, err := client.Complete(context.Background(), llmRequest("question two")); err != nil {
		t.Fatalf("Complete 2: %v", err)
	}
	_, err := client.Complete(context.Background(), llmRequest("unknown"))
	if err == nil {
		t.Fatal("expected miss error")
	}
	if !errors.Is(err, ErrNoFixture) {
		t.Errorf("error should wrap ErrNoFixture: %v", err)
	}
	if !strings.Contains(err.Error(), "re-record") {
		t.Errorf("error should guide re-recording: %v", err)
	}
}

func TestFixtureLLMClientMissLiveFallback(t *testing.T) {
	live := &scriptedClient{text: "live answer"}
	client := NewFixtureLLMClient(NewFixtureStore(&Recording{}), FixtureMissLive, live)

	resp, err := client.Complete(context.Background(), llmRequest("anything"))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "live answer" {
		t.Errorf("fallback response = %q", resp.Text)
	}
}

func TestFixtureLLMClientLiveFallbackWithoutClient(t *testing.T) {
	client := NewFixtureLLMClient(NewFixtureStore(&Recording{}), FixtureMissLive, nil)
	if _, err := client.Complete(context.Background(), llmRequest("x")); !errors.Is(err, ErrNoFixture) {
		t.Errorf("expected ErrNoFixture, got %v", err)
	}
}

// scriptedClient returns a canned response (simulates a live provider).
type scriptedClient struct {
	text string
}

func (c *scriptedClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	return core.LLMResponse{Text: c.text, Model: req.Model}, nil
}

func TestFixtureToolMatchAndScrub(t *testing.T) {
	rec := &Recording{
		Version: FormatVersion,
		RunID:   "run-1",
		Nodes: []NodeRecord{{
			NodeID: "fetch",
			Tools: []ToolCallRecord{{
				NodeID:   "fetch",
				ToolName: "http_fetch",
				Args:     map[string]any{"url": "https://example.com"},
				Result:   map[string]any{"status": 200.0},
			}},
		}},
	}
	store := NewFixtureStore(rec)
	tool := NewFixtureTool(&scriptedTool{name: "http_fetch"}, store, FixtureMissFail)

	if tool.Name() != "http_fetch" {
		t.Errorf("wrapped tool name = %q", tool.Name())
	}

	result, err := tool.Invoke(context.Background(), map[string]any{"url": "https://example.com"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if result["status"] != 200.0 {
		t.Errorf("result = %v", result)
	}

	if _, err := tool.Invoke(context.Background(), map[string]any{"url": "https://other.com"}); !errors.Is(err, ErrNoFixture) {
		t.Errorf("mismatched args should miss: %v", err)
	}
}

func TestFixtureToolRecordedError(t *testing.T) {
	store := &FixtureStore{
		tools: map[string][]toolFixture{
			"http_fetch": {{ToolName: "http_fetch", IsError: true, Error: "connection refused"}},
		},
	}
	tool := NewFixtureTool(&scriptedTool{name: "http_fetch"}, store, FixtureMissFail)

	if _, err := tool.Invoke(context.Background(), map[string]any{}); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("expected recorded error, got %v", err)
	}
}

func TestFixtureToolLiveFallback(t *testing.T) {
	inner := &scriptedTool{result: map[string]any{"live": true}}
	store := &FixtureStore{tools: map[string][]toolFixture{}}
	tool := NewFixtureTool(inner, store, FixtureMissLive)

	result, err := tool.Invoke(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if result["live"] != true {
		t.Errorf("fallback result = %v", result)
	}
	if tool.Name() != "scripted" {
		t.Errorf("wrapped name = %q", tool.Name())
	}
}

type scriptedTool struct {
	name   string
	result map[string]any
	calls  int
}

func (t *scriptedTool) Name() string {
	if t.name != "" {
		return t.name
	}
	return "scripted"
}

func (t *scriptedTool) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	t.calls++
	return t.result, nil
}

func TestReplayToolRegistry(t *testing.T) {
	inner := core.NewToolRegistry()
	inner.Register(&scriptedTool{})

	wrapped := ReplayToolRegistry(inner, NewFixtureStore(&Recording{}), FixtureMissFail)
	if wrapped == nil {
		t.Fatal("nil registry")
	}
	tool, ok := wrapped.Get("scripted")
	if !ok {
		t.Fatal("wrapped registry missing tool")
	}
	if _, isFixture := tool.(*FixtureTool); !isFixture {
		t.Errorf("tool = %T, want *FixtureTool", tool)
	}

	if ReplayToolRegistry(nil, nil, "") != nil {
		t.Error("nil inner should return nil registry")
	}
}

func TestFixtureStoreConcurrencySafety(t *testing.T) {
	// Fixture stores are consumed under parallel execution; hammer matching
	// from multiple goroutines to surface races (run with -race).
	rec := &Recording{
		Version: FormatVersion,
		RunID:   "run-race",
		Nodes: []NodeRecord{
			{NodeID: "n", LLM: []LLMCallRecord{{NodeID: "n", Model: "m", InputText: "x", Completion: "a"}}},
			{NodeID: "n", Tools: []ToolCallRecord{{NodeID: "n", ToolName: "t", Args: map[string]any{"k": "v"}, Result: map[string]any{}}}},
			{NodeID: "n", LLM: []LLMCallRecord{{NodeID: "n", Model: "m", InputText: "y", Completion: "b"}}},
		},
	}

	store := NewFixtureStore(rec)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Only two LLM fixtures exist; extra matchers miss cleanly.
			_, _ = store.matchLLM("m", "", "x", nil)
			_, _ = store.matchTool("t", map[string]any{"k": "v"})
		}()
	}
	wg.Wait()
}
