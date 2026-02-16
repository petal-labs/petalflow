package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/agent"
	"github.com/petal-labs/petalflow/daemon"
	petalotel "github.com/petal-labs/petalflow/otel"
	"github.com/petal-labs/petalflow/registry"
	"github.com/petal-labs/petalflow/runtime"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestWorkflowLifecycle_DaemonAPI_SimpleAgent(t *testing.T) {
	handler := newDaemonWorkflowLifecycleHandler(t)
	assertDaemonToolsEndpointHealthy(t, handler)

	wf := daemonSimpleAgentWorkflow("daemon_simple_workflow")
	created := postAgentWorkflow(t, handler, wf)
	if created.Compiled == nil {
		t.Fatal("compiled graph should be present in create response")
	}
	if created.Compiled.Entry != "draft__writer" {
		t.Fatalf("compiled entry = %q, want %q", created.Compiled.Entry, "draft__writer")
	}

	run := runWorkflow(t, handler, wf.ID, map[string]any{
		"topic": "workflow lifecycle testing via daemon API",
	})
	if run.Status != "completed" {
		t.Fatalf("run status = %q, want %q", run.Status, "completed")
	}
	if run.RunID == "" {
		t.Fatal("run_id should not be empty")
	}

	output, ok := run.Output.Vars["draft__writer_output"].(string)
	if !ok || strings.TrimSpace(output) == "" {
		t.Fatalf("draft__writer_output missing/invalid: %v", run.Output.Vars["draft__writer_output"])
	}
	if !strings.Contains(strings.ToLower(output), "daemon api") {
		t.Fatalf("draft output should contain daemon api context, got: %q", output)
	}

	events := getRunEvents(t, handler, run.RunID)
	assertRunLifecycleEvents(t, events, run.RunID)
	assertEventKindsPresent(t, events, runtime.EventNodeOutputFinal)
}

func TestWorkflowLifecycle_DaemonAPI_MediumSequentialAgent(t *testing.T) {
	handler := newDaemonWorkflowLifecycleHandler(t)

	wf := daemonMediumAgentWorkflow("daemon_medium_workflow")
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
		"topic": "PetalFlow daemon API architecture",
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

	events := getRunEvents(t, handler, run.RunID)
	assertRunLifecycleEvents(t, events, run.RunID)
	if got := countEventKind(events, runtime.EventNodeFinished); got < 3 {
		t.Fatalf("node.finished count = %d, want >= 3", got)
	}
}

func TestWorkflowLifecycle_DaemonAPI_HardCustomAgentWithStandaloneTool(t *testing.T) {
	handler := newDaemonWorkflowLifecycleHandler(t)

	registerDaemonTemplateRenderStandaloneTool(t, handler)
	t.Cleanup(func() {
		deleteDaemonTool(t, handler, "template_render")
	})

	assertNodeTypeAvailable(t, handler, "template_render.render")

	wf := daemonHardAgentWorkflow("daemon_hard_workflow")
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
		"topic":    "PetalFlow daemon API",
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

	events := getRunEvents(t, handler, run.RunID)
	assertRunLifecycleEvents(t, events, run.RunID)
	assertEventKindsPresent(
		t,
		events,
		runtime.EventNodeOutputFinal,
		runtime.EventToolCall,
		runtime.EventToolResult,
	)
	if got := countEventKind(events, runtime.EventNodeFinished); got < 5 {
		t.Fatalf("node.finished count = %d, want >= 5 (standalone tool + 4 agent tasks)", got)
	}
}

func TestWorkflowLifecycle_DaemonAPI_EventsIncludeTraceMetadataWhenTracingEnabled(t *testing.T) {
	handler, spans := newDaemonWorkflowLifecycleHandlerWithTracing(t)

	wf := daemonSimpleAgentWorkflow("daemon_simple_workflow_with_tracing")
	postAgentWorkflow(t, handler, wf)

	run := runWorkflow(t, handler, wf.ID, map[string]any{
		"topic": "OpenTelemetry via daemon API",
	})
	if run.Status != "completed" {
		t.Fatalf("run status = %q, want %q", run.Status, "completed")
	}

	events := getRunEvents(t, handler, run.RunID)
	assertRunLifecycleEvents(t, events, run.RunID)

	var traced []runtime.Event
	for _, event := range events {
		if event.TraceID != "" || event.SpanID != "" {
			traced = append(traced, event)
		}
	}
	if len(traced) == 0 {
		t.Fatal("expected at least one event with trace metadata")
	}

	traceID := traced[0].TraceID
	for i, event := range traced {
		if event.TraceID == "" {
			t.Fatalf("traced event[%d] kind=%s missing TraceID", i, event.Kind)
		}
		if event.SpanID == "" {
			t.Fatalf("traced event[%d] kind=%s missing SpanID", i, event.Kind)
		}
		if traceID != "" && event.TraceID != traceID {
			t.Fatalf("traced event[%d] trace_id=%q, want %q", i, event.TraceID, traceID)
		}
	}

	endedSpans := spans.Ended()
	if len(endedSpans) < 2 {
		t.Fatalf("ended span count = %d, want >= 2", len(endedSpans))
	}
}

func newDaemonWorkflowLifecycleHandler(t *testing.T) http.Handler {
	t.Helper()

	toolStore := daemon.NewMemoryToolStore()
	daemonServer, err := daemon.NewServer(daemon.ServerConfig{Store: toolStore})
	if err != nil {
		t.Fatalf("new daemon server: %v", err)
	}

	cfg := workflowLifecycleServerConfig()
	cfg.ToolStore = toolStore
	workflowServer := NewServer(cfg)

	mux := http.NewServeMux()
	workflowServer.RegisterRoutes(mux)
	daemonHandler := daemonServer.Handler()
	mux.Handle("/api/tools/", daemonHandler)
	mux.Handle("/api/tools", daemonHandler)

	return mux
}

func newDaemonWorkflowLifecycleHandlerWithTracing(t *testing.T) (http.Handler, *tracetest.SpanRecorder) {
	t.Helper()

	toolStore := daemon.NewMemoryToolStore()
	daemonServer, err := daemon.NewServer(daemon.ServerConfig{Store: toolStore})
	if err != nil {
		t.Fatalf("new daemon server: %v", err)
	}

	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(spanRecorder),
	)
	t.Cleanup(func() {
		_ = tracerProvider.Shutdown(context.Background())
	})

	tracing := petalotel.NewTracingHandler(tracerProvider.Tracer("workflow-lifecycle-daemon-test"))
	cfg := workflowLifecycleServerConfig()
	cfg.ToolStore = toolStore
	cfg.RuntimeEvents = tracing.Handle
	cfg.EmitDecorator = func(emit runtime.EventEmitter) runtime.EventEmitter {
		return petalotel.EnrichEmitter(emit, tracing)
	}
	workflowServer := NewServer(cfg)

	mux := http.NewServeMux()
	workflowServer.RegisterRoutes(mux)
	daemonHandler := daemonServer.Handler()
	mux.Handle("/api/tools/", daemonHandler)
	mux.Handle("/api/tools", daemonHandler)

	return mux, spanRecorder
}

func registerDaemonTemplateRenderStandaloneTool(t *testing.T, handler http.Handler) {
	t.Helper()

	body := mustJSON(t, map[string]any{
		"name": "template_render",
		"type": "native",
		"manifest": map[string]any{
			"manifest_version": "1.0",
			"tool": map[string]any{
				"name":        "template_render",
				"description": "Render a Go template string with provided variables.",
				"version":     "built-in",
			},
			"transport": map[string]any{
				"type": "native",
			},
			"actions": map[string]any{
				"render": map[string]any{
					"description":  "Render template content into a final string.",
					"llm_callable": false,
					"inputs": map[string]any{
						"template": map[string]any{
							"type":     "string",
							"required": true,
						},
						"values": map[string]any{
							"type": "object",
						},
					},
					"outputs": map[string]any{
						"output": map[string]any{
							"type": "object",
						},
						"rendered": map[string]any{
							"type": "string",
						},
					},
					"idempotent": true,
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/tools", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("register daemon tool failed: status=%d body=%s", w.Code, w.Body.String())
	}
}

func deleteDaemonTool(t *testing.T, handler http.Handler, name string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodDelete, "/api/tools/"+name, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("delete daemon tool failed: status=%d body=%s", w.Code, w.Body.String())
	}
}

func assertDaemonToolsEndpointHealthy(t *testing.T, handler http.Handler) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/tools", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/tools status=%d body=%s", w.Code, w.Body.String())
	}
}

func assertNodeTypeAvailable(t *testing.T, handler http.Handler, nodeType string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/node-types", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/node-types status=%d body=%s", w.Code, w.Body.String())
	}

	var nodeTypes []registry.NodeTypeDef
	if err := json.Unmarshal(w.Body.Bytes(), &nodeTypes); err != nil {
		t.Fatalf("unmarshal /api/node-types response: %v", err)
	}

	for _, def := range nodeTypes {
		if def.Type == nodeType {
			return
		}
	}
	t.Fatalf("node type %q not found in /api/node-types", nodeType)
}

func daemonSimpleAgentWorkflow(id string) agent.AgentWorkflow {
	return agent.AgentWorkflow{
		Version: "1.0",
		Kind:    "agent_workflow",
		ID:      id,
		Name:    "Simple Workflow (Daemon API)",
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
}

func daemonMediumAgentWorkflow(id string) agent.AgentWorkflow {
	return agent.AgentWorkflow{
		Version: "1.0",
		Kind:    "agent_workflow",
		ID:      id,
		Name:    "Medium Workflow (Daemon API)",
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
}

func daemonHardAgentWorkflow(id string) agent.AgentWorkflow {
	return agent.AgentWorkflow{
		Version: "1.0",
		Kind:    "agent_workflow",
		ID:      id,
		Name:    "Hard Workflow (Daemon API)",
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
}
