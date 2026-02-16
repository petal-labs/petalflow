package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/agent"
	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/daemon"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/registry"
)

type workflowLifecycleLLMClient struct {
	provider string
}

func (c *workflowLifecycleLLMClient) Complete(_ context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	prompt := strings.TrimSpace(req.InputText)
	if prompt == "" {
		prompt = "(empty prompt)"
	}

	inputTokens := len(prompt)
	if inputTokens == 0 {
		inputTokens = 1
	}

	text := fmt.Sprintf("%s::%s", c.provider, prompt)
	return core.LLMResponse{
		Text:     text,
		Model:    req.Model,
		Provider: c.provider,
		Usage: core.LLMTokenUsage{
			InputTokens:  inputTokens,
			OutputTokens: 8,
			TotalTokens:  inputTokens + 8,
		},
	}, nil
}

func newWorkflowLifecycleServer() *Server {
	return NewServer(ServerConfig{
		Store:     NewMemoryStore(),
		ToolStore: daemon.NewMemoryToolStore(),
		Providers: hydrate.ProviderMap{
			"openai": {},
		},
		ClientFactory: func(name string, _ hydrate.ProviderConfig) (core.LLMClient, error) {
			return &workflowLifecycleLLMClient{provider: name}, nil
		},
		Bus:        bus.NewMemBus(bus.MemBusConfig{}),
		EventStore: bus.NewMemEventStore(),
		CORSOrigin: "*",
		MaxBody:    1 << 20,
	})
}

func TestWorkflowLifecycle_SimpleAgent(t *testing.T) {
	srv := newWorkflowLifecycleServer()
	handler := srv.Handler()

	wf := agent.AgentWorkflow{
		Version: "1.0",
		Kind:    "agent_workflow",
		ID:      "simple_workflow",
		Name:    "Simple Workflow",
		Agents: map[string]agent.Agent{
			"writer": {
				Role:     "Writer",
				Goal:     "Write concise responses",
				Provider: "openai",
				Model:    "gpt-4o-mini",
			},
		},
		Tasks: map[string]agent.Task{
			"draft": {
				Description:    "Write one sentence about {{input.topic}}",
				Agent:          "writer",
				ExpectedOutput: "One sentence response",
			},
		},
		Execution: agent.ExecutionConfig{
			Strategy:  "sequential",
			TaskOrder: []string{"draft"},
		},
	}

	created := postAgentWorkflow(t, handler, wf)
	if created.SchemaKind != "agent_workflow" {
		t.Fatalf("schema kind = %q, want %q", created.SchemaKind, "agent_workflow")
	}
	if created.Compiled == nil {
		t.Fatal("compiled graph should be present in create response")
	}
	if created.Compiled.Entry != "draft__writer" {
		t.Fatalf("compiled entry = %q, want %q", created.Compiled.Entry, "draft__writer")
	}
	if !compiledHasNodeType(created, "llm_prompt") {
		t.Fatal("compiled graph should contain an llm_prompt node")
	}

	stored := getWorkflow(t, handler, wf.ID)
	if stored.Compiled == nil {
		t.Fatal("stored workflow should include compiled graph")
	}

	run := runWorkflow(t, handler, wf.ID, map[string]any{
		"topic": "workflow lifecycle testing",
	})
	if run.Status != "completed" {
		t.Fatalf("run status = %q, want %q", run.Status, "completed")
	}
	fmt.Printf("hard workflow vars=%#v\n", run.Output.Vars)
	if run.RunID == "" {
		t.Fatal("run_id should not be empty")
	}

	output, ok := run.Output.Vars["draft__writer_output"].(string)
	if !ok || strings.TrimSpace(output) == "" {
		t.Fatalf("draft__writer_output missing/invalid: %v", run.Output.Vars["draft__writer_output"])
	}
	if !strings.Contains(strings.ToLower(output), "workflow lifecycle testing") {
		t.Fatalf("draft output should contain topic, got: %q", output)
	}
}

func TestWorkflowLifecycle_MediumSequentialAgent(t *testing.T) {
	srv := newWorkflowLifecycleServer()
	handler := srv.Handler()

	wf := agent.AgentWorkflow{
		Version: "1.0",
		Kind:    "agent_workflow",
		ID:      "medium_workflow",
		Name:    "Medium Workflow",
		Agents: map[string]agent.Agent{
			"researcher": {
				Role:     "Researcher",
				Goal:     "Collect source facts",
				Provider: "openai",
				Model:    "gpt-4o-mini",
			},
			"writer": {
				Role:     "Writer",
				Goal:     "Create structured summaries",
				Provider: "openai",
				Model:    "gpt-4o-mini",
			},
		},
		Tasks: map[string]agent.Task{
			"a_research": {
				Description:    "Research {{input.topic}} and extract key findings",
				Agent:          "researcher",
				ExpectedOutput: "Research findings",
			},
			"b_outline": {
				Description:    "Create an outline from {{tasks.a_research.output}}",
				Agent:          "writer",
				ExpectedOutput: "Outline",
			},
			"c_summary": {
				Description:    "Write a concise summary from {{tasks.b_outline.output}} and {{tasks.a_research.output}}",
				Agent:          "writer",
				ExpectedOutput: "Final summary",
			},
		},
		Execution: agent.ExecutionConfig{
			Strategy:  "sequential",
			TaskOrder: []string{"a_research", "b_outline", "c_summary"},
		},
	}

	created := postAgentWorkflow(t, handler, wf)
	if created.Compiled == nil {
		t.Fatal("compiled graph should be present in create response")
	}
	if !compiledHasEdge(created, "a_research__researcher", "b_outline__writer") {
		t.Fatal("compiled graph should include edge a_research__researcher -> b_outline__writer")
	}
	if !compiledHasEdge(created, "b_outline__writer", "c_summary__writer") {
		t.Fatal("compiled graph should include edge b_outline__writer -> c_summary__writer")
	}

	run := runWorkflow(t, handler, wf.ID, map[string]any{
		"topic": "PetalFlow architecture",
	})
	if run.Status != "completed" {
		t.Fatalf("run status = %q, want %q", run.Status, "completed")
	}

	for _, key := range []string{
		"a_research__researcher_output",
		"b_outline__writer_output",
		"c_summary__writer_output",
	} {
		value, ok := run.Output.Vars[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			t.Fatalf("%s missing/invalid in output vars: %v", key, run.Output.Vars[key])
		}
	}

	summary := run.Output.Vars["c_summary__writer_output"].(string)
	if !strings.Contains(strings.ToLower(summary), "petalflow") {
		t.Fatalf("summary output should include topic context, got: %q", summary)
	}
}

func TestWorkflowLifecycle_HardCustomAgentWithStandaloneTool(t *testing.T) {
	registerStandaloneToolNodeType(t, registry.NodeTypeDef{
		Type:     "template_render.render",
		Category: "tool",
		IsTool:   true,
		ToolMode: "standalone",
		Ports: registry.PortSchema{
			Inputs: []registry.PortDef{
				{Name: "template", Type: "string", Required: true},
				{Name: "values", Type: "object", Required: false},
			},
			Outputs: []registry.PortDef{
				{Name: "output", Type: "object"},
				{Name: "rendered", Type: "string"},
			},
		},
	})

	srv := newWorkflowLifecycleServer()
	handler := srv.Handler()

	wf := agent.AgentWorkflow{
		Version: "1.0",
		Kind:    "agent_workflow",
		ID:      "hard_workflow",
		Name:    "Hard Workflow",
		Agents: map[string]agent.Agent{
			"scout": {
				Role:     "Scout",
				Goal:     "Prepare raw context",
				Provider: "openai",
				Model:    "gpt-4o-mini",
				Tools:    []string{"template_render.render"},
			},
			"analyst": {
				Role:     "Analyst",
				Goal:     "Analyze prepared context",
				Provider: "openai",
				Model:    "gpt-4o-mini",
			},
			"scorer": {
				Role:     "Scorer",
				Goal:     "Score findings",
				Provider: "openai",
				Model:    "gpt-4o-mini",
			},
			"reporter": {
				Role:     "Reporter",
				Goal:     "Produce final report",
				Provider: "openai",
				Model:    "gpt-4o-mini",
			},
		},
		Tasks: map[string]agent.Task{
			"a_ingest": {
				Description:    "Ingest context for {{input.topic}}",
				Agent:          "scout",
				ExpectedOutput: "Ingested context",
			},
			"b_analyze": {
				Description:    "Analyze {{tasks.a_ingest.output}}",
				Agent:          "analyst",
				ExpectedOutput: "Analysis",
			},
			"c_score": {
				Description:    "Score {{tasks.a_ingest.output}}",
				Agent:          "scorer",
				ExpectedOutput: "Score",
			},
			"d_report": {
				Description:    "Report from {{tasks.b_analyze.output}} and {{tasks.c_score.output}}",
				Agent:          "reporter",
				ExpectedOutput: "Report",
			},
		},
		Execution: agent.ExecutionConfig{
			Strategy: "custom",
			Tasks: map[string]agent.TaskDependencies{
				"a_ingest":  {DependsOn: []string{}},
				"b_analyze": {DependsOn: []string{"a_ingest"}, Condition: `tasks.a_ingest.output != ""`},
				"c_score":   {DependsOn: []string{"a_ingest"}},
				"d_report":  {DependsOn: []string{"b_analyze", "c_score"}},
			},
		},
	}

	created := postAgentWorkflow(t, handler, wf)
	if created.Compiled == nil {
		t.Fatal("compiled graph should be present in create response")
	}
	if created.Compiled.Entry != "a_ingest__template_render.render" {
		t.Fatalf("compiled entry = %q, want %q", created.Compiled.Entry, "a_ingest__template_render.render")
	}
	if !compiledHasNodeType(created, "template_render.render") {
		t.Fatal("compiled graph should include standalone tool node type template_render.render")
	}
	if !compiledHasNodeType(created, "conditional") {
		t.Fatal("compiled graph should include conditional node for custom dependency condition")
	}

	run := runWorkflow(t, handler, wf.ID, map[string]any{
		"topic":    "PetalFlow",
		"template": "workflow e2e for {{.name}}",
		"values": map[string]any{
			"name": "PetalFlow",
		},
	})
	if run.Status != "completed" {
		t.Fatalf("run status = %q, want %q", run.Status, "completed")
	}

	toolOutputRaw, ok := run.Output.Vars["a_ingest__template_render.render_output"]
	if !ok {
		t.Fatal("expected a_ingest__template_render.render_output in run output vars")
	}
	toolOutput, ok := toolOutputRaw.(map[string]any)
	if !ok {
		t.Fatalf("tool output type = %T, want map[string]any", toolOutputRaw)
	}
	rendered, _ := toolOutput["rendered"].(string)
	if strings.TrimSpace(rendered) != "workflow e2e for PetalFlow" {
		t.Fatalf("rendered tool output = %q, want %q", rendered, "workflow e2e for PetalFlow")
	}

	for _, key := range []string{
		"a_ingest__scout_output",
		"b_analyze__analyst_output",
		"c_score__scorer_output",
		"d_report__reporter_output",
	} {
		value, ok := run.Output.Vars[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			t.Fatalf("%s missing/invalid in output vars: %v (all vars=%#v)", key, run.Output.Vars[key], run.Output.Vars)
		}
	}
}

func postAgentWorkflow(t *testing.T, handler http.Handler, wf agent.AgentWorkflow) WorkflowRecord {
	t.Helper()
	body := mustJSON(t, wf)
	r := httptest.NewRequest(http.MethodPost, "/api/workflows/agent", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("create workflow failed: status=%d body=%s", w.Code, w.Body.String())
	}

	var rec WorkflowRecord
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	return rec
}

func getWorkflow(t *testing.T, handler http.Handler, id string) WorkflowRecord {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/workflows/"+id, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("get workflow failed: status=%d body=%s", w.Code, w.Body.String())
	}

	var rec WorkflowRecord
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal get response: %v", err)
	}
	return rec
}

func runWorkflow(t *testing.T, handler http.Handler, id string, input map[string]any) RunResponse {
	t.Helper()
	body := mustJSON(t, RunRequest{
		Input:   input,
		Options: RunReqOptions{Timeout: "30s"},
	})

	r := httptest.NewRequest(http.MethodPost, "/api/workflows/"+id+"/run", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("run workflow failed: status=%d body=%s", w.Code, w.Body.String())
	}

	var resp RunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal run response: %v", err)
	}
	return resp
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return data
}

func compiledHasNodeType(rec WorkflowRecord, nodeType string) bool {
	if rec.Compiled == nil {
		return false
	}
	for _, node := range rec.Compiled.Nodes {
		if node.Type == nodeType {
			return true
		}
	}
	return false
}

func compiledHasEdge(rec WorkflowRecord, source, target string) bool {
	if rec.Compiled == nil {
		return false
	}
	for _, edge := range rec.Compiled.Edges {
		if edge.Source == source && edge.Target == target {
			return true
		}
	}
	return false
}

func registerStandaloneToolNodeType(t *testing.T, def registry.NodeTypeDef) {
	t.Helper()
	reg := registry.Global()
	prev, hadPrev := reg.Get(def.Type)
	reg.Register(def)
	t.Cleanup(func() {
		if hadPrev {
			reg.Register(prev)
			return
		}
		reg.Delete(def.Type)
	})
}
