//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/agent"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/registry"
	"github.com/petal-labs/petalflow/runtime"
)

type staticLLMClient struct{}

func (c *staticLLMClient) Complete(_ context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	return core.LLMResponse{
		Text:     "ack",
		Model:    req.Model,
		Provider: "mock",
		Usage: core.LLMTokenUsage{
			InputTokens:  1,
			OutputTokens: 1,
			TotalTokens:  2,
		},
	}, nil
}

func TestAgentCompile_StandaloneToolExecution_HTTPFetch(t *testing.T) {
	registry.Global().Register(registry.NodeTypeDef{
		Type:     "http_fetch.fetch",
		Category: "tool",
		IsTool:   true,
		ToolMode: "standalone",
		Ports: registry.PortSchema{
			Inputs: []registry.PortDef{
				{Name: "url", Type: "string", Required: true},
				{Name: "method", Type: "string"},
			},
			Outputs: []registry.PortDef{
				{Name: "status_code", Type: "integer"},
				{Name: "body", Type: "string"},
			},
		},
	})
	t.Cleanup(func() {
		registry.Global().Delete("http_fetch.fetch")
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("tool-ok"))
	}))
	defer server.Close()

	wf := &agent.AgentWorkflow{
		ID:      "standalone_tool_exec",
		Version: "1.0",
		Kind:    "agent_workflow",
		Agents: map[string]agent.Agent{
			"researcher": {
				Role:     "Researcher",
				Goal:     "Collect and summarize",
				Provider: "mock",
				Model:    "mock-model",
				Tools:    []string{"http_fetch.fetch"},
			},
		},
		Tasks: map[string]agent.Task{
			"fetch": {
				Description:    "Summarize fetched content",
				Agent:          "researcher",
				ExpectedOutput: "Summary",
			},
		},
		Execution: agent.ExecutionConfig{
			Strategy:  "sequential",
			TaskOrder: []string{"fetch"},
		},
	}

	gd, err := agent.Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if gd.Entry != "fetch__http_fetch.fetch" {
		t.Fatalf("Entry = %q, want fetch__http_fetch.fetch", gd.Entry)
	}

	toolRegistry, err := hydrate.BuildActionToolRegistry(context.Background(), nil)
	if err != nil {
		t.Fatalf("BuildActionToolRegistry() error = %v", err)
	}

	providers := hydrate.ProviderMap{
		"mock": {},
	}
	factory := hydrate.NewLiveNodeFactory(providers, func(_ string, _ hydrate.ProviderConfig) (core.LLMClient, error) {
		return &staticLLMClient{}, nil
	}, hydrate.WithToolRegistry(toolRegistry))

	execGraph, err := hydrate.HydrateGraph(gd, providers, factory)
	if err != nil {
		t.Fatalf("HydrateGraph() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := core.NewEnvelope().
		WithVar("url", server.URL).
		WithVar("method", "GET")

	result, err := runtime.NewRuntime().Run(ctx, execGraph, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("runtime.Run() error = %v", err)
	}

	toolOutputKey := "fetch__http_fetch.fetch_output"
	rawToolOutput, ok := result.GetVar(toolOutputKey)
	if !ok {
		t.Fatalf("expected %q in envelope vars", toolOutputKey)
	}
	toolOutput, ok := rawToolOutput.(map[string]any)
	if !ok {
		t.Fatalf("%s type = %T, want map[string]any", toolOutputKey, rawToolOutput)
	}

	statusCode, ok := toolOutput["status_code"].(int)
	if !ok {
		t.Fatalf("status_code type = %T, want int", toolOutput["status_code"])
	}
	if statusCode != http.StatusOK {
		t.Fatalf("status_code = %d, want %d", statusCode, http.StatusOK)
	}

	body, _ := toolOutput["body"].(string)
	if body != "tool-ok" {
		t.Fatalf("body = %q, want %q", body, "tool-ok")
	}

	if _, ok := result.GetVar("fetch__researcher_output"); !ok {
		t.Fatal("expected downstream LLM node output fetch__researcher_output")
	}
}
