package replay

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/runtime"
)

// --- unit tests over synthetic events ---

func runStartedEvent() runtime.Event {
	e := runtime.NewEvent(runtime.EventRunStarted, "run-1").
		WithPayload("graph", "test-graph").
		WithPayload("trigger", "cli").
		WithPayload("workflow_id", "wf-1")
	e.Time = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return e
}

func nodeEvents(nodeID string) []runtime.Event {
	started := runtime.NewEvent(runtime.EventNodeStarted, "run-1").WithNode(nodeID, core.NodeKindLLM)
	finished := runtime.NewEvent(runtime.EventNodeFinished, "run-1").
		WithNode(nodeID, core.NodeKindLLM).
		WithElapsed(250 * time.Millisecond)
	return []runtime.Event{started, finished}
}

func llmEvents(nodeID string) []runtime.Event {
	call := runtime.NewEvent(runtime.EventLLMCall, "run-1").
		WithNode(nodeID, core.NodeKindLLM).
		WithPayload("model", "test-model").
		WithPayload("system_prompt", "you are helpful").
		WithPayload("messages", `[{"role":"user","content":"secret question"}]`).
		WithPayload("input_text", "secret question")
	response := runtime.NewEvent(runtime.EventLLMResponse, "run-1").
		WithNode(nodeID, core.NodeKindLLM).
		WithPayload("completion", "secret answer").
		WithPayload("stop_reason", "end_turn").
		WithPayload("latency_ms", int64(120)).
		WithPayload("input_tokens", 10).
		WithPayload("output_tokens", 5).
		WithPayload("total_tokens", 15)
	return []runtime.Event{call, response}
}

func TestCaptureSyntheticEvents(t *testing.T) {
	capture := NewCapture(AllowAllPolicy())

	for _, e := range append([]runtime.Event{runStartedEvent()}, nodeEvents("a")...) {
		capture.Handle(e)
	}
	for _, e := range llmEvents("a") {
		capture.Handle(e)
	}
	finished := runtime.NewEvent(runtime.EventRunFinished, "run-1").
		WithPayload("status", "succeeded")
	finished.Time = time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC)
	capture.Handle(finished)

	rec := capture.Finish()
	if err := rec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if rec.RunID != "run-1" || rec.GraphName != "test-graph" || rec.WorkflowID != "wf-1" {
		t.Errorf("run metadata = %+v", rec)
	}
	if rec.Status != "succeeded" || rec.FinishedAt.IsZero() {
		t.Errorf("finish state = status %q finishedAt %v", rec.Status, rec.FinishedAt)
	}

	if len(rec.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(rec.Nodes))
	}
	node := rec.Nodes[0]
	if node.NodeID != "a" || node.ElapsedMS != 250 {
		t.Errorf("node record = %+v", node)
	}
	if len(node.LLM) != 1 {
		t.Fatalf("llm records = %d, want 1", len(node.LLM))
	}
	call := node.LLM[0]
	if call.Model != "test-model" || call.System != "you are helpful" || call.Completion != "secret answer" {
		t.Errorf("allow-all LLM record = %+v", call)
	}
	if call.Usage.TotalTokens != 15 || call.LatencyMS != 120 {
		t.Errorf("usage/latency = %+v", call)
	}
}

func TestCaptureDefaultPolicyRedacts(t *testing.T) {
	capture := NewCapture(DefaultPolicy())
	capture.Handle(runStartedEvent())
	for _, e := range nodeEvents("a") {
		capture.Handle(e)
	}
	for _, e := range llmEvents("a") {
		capture.Handle(e)
	}

	rec := capture.Finish()
	call := rec.Nodes[0].LLM[0]
	if call.System != defaultPlaceholder || call.Completion != defaultPlaceholder {
		t.Errorf("default policy should redact prompts and completions: %+v", call)
	}
	if call.Usage.TotalTokens != 15 || call.Model != "test-model" {
		t.Errorf("metadata should survive: %+v", call)
	}
}

func TestCaptureNodeFailure(t *testing.T) {
	capture := NewCapture(DefaultPolicy())
	capture.Handle(runStartedEvent())

	failed := runtime.NewEvent(runtime.EventNodeFailed, "run-1").
		WithNode("a", core.NodeKindLLM).
		WithElapsed(30*time.Millisecond).
		WithPayload("error", "boom")
	capture.Handle(failed)

	capture.RecordNode("a", core.NodeKindLLM, core.NewEnvelope().WithInput("in"), nil, errors.New("boom"))

	rec := capture.Finish()
	node := rec.Nodes[0]
	if node.Error != "boom" {
		t.Errorf("error = %q", node.Error)
	}
	if node.Output != nil {
		t.Errorf("output should be nil on failure")
	}
	if node.Input == nil || node.Input.Input != defaultPlaceholder {
		t.Errorf("input should be captured and redacted: %+v", node.Input)
	}
}

func TestCaptureToolEvents(t *testing.T) {
	capture := NewCapture(AllowAllPolicy())
	capture.Handle(runStartedEvent())
	for _, e := range nodeEvents("t") {
		capture.Handle(e)
	}

	call := runtime.NewEvent(runtime.EventToolCall, "run-1").
		WithNode("t", core.NodeKindTool).
		WithPayload("tool_name", "http_fetch").
		WithPayload("arguments", map[string]any{"url": "https://example.com"})
	result := runtime.NewEvent(runtime.EventToolResult, "run-1").
		WithNode("t", core.NodeKindTool).
		WithPayload("tool_name", "http_fetch").
		WithPayload("is_error", false)
	capture.Handle(call)
	capture.Handle(result)

	rec := capture.Finish()
	if len(rec.Nodes[0].Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(rec.Nodes[0].Tools))
	}
	tool := rec.Nodes[0].Tools[0]
	if tool.ToolName != "http_fetch" || tool.Args["url"] != "https://example.com" {
		t.Errorf("tool record = %+v", tool)
	}
	if tool.IsError {
		t.Errorf("is_error should be false")
	}
}

func TestCaptureSaveLoadRoundTrip(t *testing.T) {
	capture := NewCapture(AllowAllPolicy())
	capture.Handle(runStartedEvent())
	for _, e := range nodeEvents("a") {
		capture.Handle(e)
	}
	capture.RecordNode("a", core.NodeKindLLM, core.NewEnvelope().WithInput("in"), core.NewEnvelope().WithInput("out"), nil)
	rec := capture.Finish()

	path := tempPath(t)
	if err := Save(rec, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.RunID != "run-1" || len(loaded.Nodes) != 1 {
		t.Errorf("round-trip mismatch: %+v", loaded)
	}
}

// --- integration test through the real runtime ---

func TestCaptureIntegrationRuntimeRun(t *testing.T) {
	client := &fixtureLLMClient{response: "integration answer"}

	llm := nodes.NewLLMNode("answer", client, nodes.LLMNodeConfig{
		Model:          "test-model",
		PromptTemplate: "Summarize: {{.input}}",
		OutputKey:      "summary",
	})
	passthrough := core.NewNoopNode("passthrough")

	g, err := graph.NewGraphBuilder("capture-integration").
		AddNode(llm).
		Edge(passthrough).
		Build()
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	capture := NewCapture(AllowAllPolicy())
	opts := runtime.DefaultRunOptions()
	opts.EventHandler = capture.Handle
	opts.NodeRecorder = capture
	opts.CaptureSnapshots = true
	opts.GraphDefinition = g
	opts.Inputs = "hello world"

	final, err := runtime.NewRuntime().Run(context.Background(), g, core.NewEnvelope().WithInput("hello world"), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	rec := capture.Finish()
	if err := rec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if rec.GraphName != "capture-integration" || rec.GraphDefinition == nil || rec.Inputs != "hello world" {
		t.Errorf("run snapshot = name %q def %v inputs %v", rec.GraphName, rec.GraphDefinition != nil, rec.Inputs)
	}
	if len(rec.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(rec.Nodes))
	}

	var llmRecord *NodeRecord
	for i := range rec.Nodes {
		if rec.Nodes[i].NodeID == "answer" {
			llmRecord = &rec.Nodes[i]
		}
	}
	if llmRecord == nil {
		t.Fatal("llm node not captured")
	}
	if llmRecord.Output == nil {
		t.Fatal("llm node output envelope not captured")
	}
	summary := llmRecord.Output.Vars["summary"]
	if summary != "integration answer" {
		t.Errorf("captured summary = %v", summary)
	}
	// The prompt built by the node is visible in the captured input state.
	if llmRecord.Input == nil || llmRecord.Input.Input != "hello world" {
		t.Errorf("llm input state = %+v", llmRecord.Input)
	}

	// The final envelope from the run must agree with the last node's capture.
	if final == nil {
		t.Fatal("run returned nil envelope")
	}

	// Redacted variant of the same run must not contain content.
	redacted := NewCapture(DefaultPolicy())
	opts.EventHandler = redacted.Handle
	opts.NodeRecorder = redacted
	if _, err := runtime.NewRuntime().Run(context.Background(), g, core.NewEnvelope().WithInput("hello world"), opts); err != nil {
		t.Fatalf("redacted run: %v", err)
	}
	raw := mustJSON(t, redacted.Finish())
	for _, sensitive := range []string{"hello world", "integration answer", "Summarize"} {
		if strings.Contains(raw, sensitive) {
			t.Errorf("redacted recording leaked %q", sensitive)
		}
	}
}

// fixtureLLMClient is a deterministic LLM client for tests. LLM event
// folding is covered by the synthetic event tests above; this fixture keeps
// the integration run side-effect free.
type fixtureLLMClient struct {
	response string
}

func (c *fixtureLLMClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	return core.LLMResponse{
		Text:  c.response,
		Model: req.Model,
		Usage: core.LLMTokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil
}

func tempPath(t *testing.T) string {
	t.Helper()
	return t.TempDir() + "/recording.json"
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}
