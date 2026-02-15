//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/runtime"
)

func TestLLMNode_NonStreaming(t *testing.T) {
	skipIfNoAPIKey(t)

	client := newNonStreamingClient(t)

	node := nodes.NewLLMNode("hello", client, nodes.LLMNodeConfig{
		Model:     "gpt-4o-mini",
		System:    "You are a helpful assistant. Be very brief.",
		InputVars: []string{"prompt"},
		OutputKey: "result",
		RetryPolicy: core.RetryPolicy{
			MaxAttempts: 1,
			Backoff:     0,
		},
		Timeout: 30 * time.Second,
	})

	g, err := graph.NewGraphBuilder("non-streaming-test").
		AddNode(node).
		Build()
	if err != nil {
		t.Fatalf("building graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("prompt", "Say hello in one sentence")

	rt := runtime.NewRuntime()
	result, err := rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("runtime.Run: %v", err)
	}

	// Check output text
	output, ok := result.GetVar("result")
	if !ok {
		t.Fatal("expected 'result' var in envelope")
	}
	text, ok := output.(string)
	if !ok {
		t.Fatalf("expected string output, got %T", output)
	}
	if text == "" {
		t.Fatal("expected non-empty output text")
	}
	t.Logf("Non-streaming output: %s", text)

	// Check token usage
	usageVal, ok := result.GetVar("result_usage")
	if !ok {
		t.Fatal("expected 'result_usage' var in envelope")
	}
	usage, ok := usageVal.(core.TokenUsage)
	if !ok {
		t.Fatalf("expected core.TokenUsage, got %T", usageVal)
	}
	if usage.TotalTokens == 0 {
		t.Fatal("expected non-zero total tokens")
	}
	t.Logf("Token usage: input=%d output=%d total=%d", usage.InputTokens, usage.OutputTokens, usage.TotalTokens)
}

func TestLLMNode_Streaming(t *testing.T) {
	skipIfNoAPIKey(t)

	client := newOpenAIClient(t)

	node := nodes.NewLLMNode("stream-hello", client, nodes.LLMNodeConfig{
		Model:     "gpt-4o-mini",
		System:    "You are a helpful assistant. Be very brief.",
		InputVars: []string{"prompt"},
		OutputKey: "result",
		RetryPolicy: core.RetryPolicy{
			MaxAttempts: 1,
			Backoff:     0,
		},
		Timeout: 30 * time.Second,
	})

	g, err := graph.NewGraphBuilder("streaming-test").
		AddNode(node).
		Build()
	if err != nil {
		t.Fatalf("building graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("prompt", "Say hello in one sentence")

	// Collect events
	var mu sync.Mutex
	var deltas []runtime.Event
	var finals []runtime.Event

	opts := runtime.DefaultRunOptions()
	opts.EventHandler = func(e runtime.Event) {
		mu.Lock()
		defer mu.Unlock()
		switch e.Kind {
		case runtime.EventNodeOutputDelta:
			deltas = append(deltas, e)
		case runtime.EventNodeOutputFinal:
			finals = append(finals, e)
		}
	}

	rt := runtime.NewRuntime()
	result, err := rt.Run(ctx, g, env, opts)
	if err != nil {
		t.Fatalf("runtime.Run: %v", err)
	}

	mu.Lock()
	deltaCount := len(deltas)
	finalCount := len(finals)
	mu.Unlock()

	// Assert: at least 1 delta event received
	if deltaCount == 0 {
		t.Fatal("expected at least 1 delta event")
	}
	t.Logf("Received %d delta events", deltaCount)

	// Assert: exactly 1 final event
	if finalCount != 1 {
		t.Fatalf("expected 1 final event, got %d", finalCount)
	}

	// Assert: final event has non-empty text
	mu.Lock()
	finalText, _ := finals[0].Payload["text"].(string)
	mu.Unlock()
	if finalText == "" {
		t.Fatal("expected non-empty text in final event")
	}
	t.Logf("Streaming final text: %s", finalText)

	// Assert: output var matches final event text
	output, ok := result.GetVar("result")
	if !ok {
		t.Fatal("expected 'result' var in envelope")
	}
	text, ok := output.(string)
	if !ok {
		t.Fatalf("expected string output, got %T", output)
	}
	if text != finalText {
		t.Errorf("output var %q does not match final event text %q", text, finalText)
	}
}

func TestLLMNode_JSONSchema(t *testing.T) {
	skipIfNoAPIKey(t)

	client := newNonStreamingClient(t)

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"greeting": map[string]any{
				"type": "string",
			},
		},
		"required":             []any{"greeting"},
		"additionalProperties": false,
	}

	node := nodes.NewLLMNode("json-hello", client, nodes.LLMNodeConfig{
		Model:      "gpt-4o-mini",
		System:     "You are a helpful assistant. Respond with a JSON object containing a greeting field.",
		InputVars:  []string{"prompt"},
		OutputKey:  "result",
		JSONSchema: schema,
		RetryPolicy: core.RetryPolicy{
			MaxAttempts: 1,
			Backoff:     0,
		},
		Timeout: 30 * time.Second,
	})

	g, err := graph.NewGraphBuilder("json-schema-test").
		AddNode(node).
		Build()
	if err != nil {
		t.Fatalf("building graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("prompt", "Say hello")

	rt := runtime.NewRuntime()
	result, err := rt.Run(ctx, g, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("runtime.Run: %v", err)
	}

	// When JSONSchema is set, the output may be stored as map[string]any (parsed JSON).
	output, ok := result.GetVar("result")
	if !ok {
		t.Fatal("expected 'result' var in envelope")
	}

	// The output could be map[string]any (if the adapter parsed it) or string.
	switch v := output.(type) {
	case map[string]any:
		greeting, ok := v["greeting"].(string)
		if !ok || greeting == "" {
			t.Fatalf("expected non-empty 'greeting' field in JSON output, got: %v", v)
		}
		t.Logf("JSON output (parsed): %v", v)
	case string:
		// Verify it parses as valid JSON with the expected shape.
		var parsed map[string]any
		candidate := trimJSONCodeFence(v)
		if err := json.Unmarshal([]byte(candidate), &parsed); err != nil {
			t.Fatalf("output is not valid JSON: %v\nraw: %s", err, v)
		}
		greeting, ok := parsed["greeting"].(string)
		if !ok || greeting == "" {
			t.Fatalf("expected non-empty 'greeting' field, got: %v", parsed)
		}
		t.Logf("JSON output (string): %s", v)
	default:
		t.Fatalf("unexpected output type %T: %v", output, output)
	}
}

func trimJSONCodeFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) == 0 {
		return trimmed
	}

	// Drop opening fence line (``` or ```json).
	lines = lines[1:]

	// Drop closing fence if present.
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}
