package replay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/runtime"
)

// emittingClient wraps an LLM client and emits llm.call / llm.response
// events the way an instrumented client does, so captures record LLM
// interactions. The emitter is read from the node's execution context.
type emittingClient struct {
	inner  core.LLMClient
	nodeID string
}

func (c *emittingClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	emit := runtime.EmitterFromContext(ctx)
	runID := runIDFromEnv(ctx)

	emit(runtime.NewEvent(runtime.EventLLMCall, runID).
		WithNode(c.nodeID, core.NodeKindLLM).
		WithPayload("model", req.Model).
		WithPayload("system_prompt", req.System).
		WithPayload("input_text", req.InputText))

	start := time.Now()
	resp, err := c.inner.Complete(ctx, req)

	event := runtime.NewEvent(runtime.EventLLMResponse, runID).
		WithNode(c.nodeID, core.NodeKindLLM).
		WithPayload("latency_ms", time.Since(start).Milliseconds())
	if err != nil {
		event = event.WithPayload("error", err.Error()).WithPayload("status", "error")
	} else {
		event = event.
			WithPayload("completion", resp.Text).
			WithPayload("stop_reason", resp.Status).
			WithPayload("input_tokens", resp.Usage.InputTokens).
			WithPayload("output_tokens", resp.Usage.OutputTokens).
			WithPayload("total_tokens", resp.Usage.TotalTokens)
	}
	emit(event)
	return resp, err
}

// runIDFromEnv reads the run ID injected into the test context.
type runIDKey struct{}

func runIDFromEnv(ctx context.Context) string {
	if id, ok := ctx.Value(runIDKey{}).(string); ok {
		return id
	}
	return ""
}

// --- replay runner tests ---

const recordPromptTemplate = "Summarize: {{.input}}"

// recordRun captures a recording of a scripted LLM workflow, mirroring a
// live run with instrumented clients.
func recordRun(t *testing.T) (*Recording, string) {
	t.Helper()

	client := &emittingClient{
		inner:  &scriptedClient{text: "the original answer"},
		nodeID: "answer",
	}
	llm := nodes.NewLLMNode("answer", client, nodes.LLMNodeConfig{
		Model:          "test-model",
		PromptTemplate: recordPromptTemplate,
		OutputKey:      "answer_output",
	})
	passthrough := core.NewNoopNode("passthrough")

	g, err := graph.NewGraphBuilder("record-run").AddNode(llm).Edge(passthrough).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	record := NewCapture(AllowAllPolicy())
	opts := runtime.DefaultRunOptions()
	opts.EventHandler = record.Handle
	opts.NodeRecorder = record
	opts.CaptureSnapshots = true

	ctx := context.WithValue(context.Background(), runIDKey{}, "recorded-run")
	_, err = runtime.NewRuntime().Run(ctx, g, core.NewEnvelope().WithInput("subject"), opts)
	if err != nil {
		t.Fatalf("record run: %v", err)
	}

	recording := record.Finish()
	if err := recording.Validate(); err != nil {
		t.Fatalf("recording invalid: %v", err)
	}
	return recording, "the original answer"
}

// buildReplayGraph rebuilds the recorded workflow shape with fixture-backed
// dependencies: identical node IDs, prompt construction, and output keys.
// store may be nil to derive fixtures from the recording.
func buildReplayGraph(t *testing.T, rec *Recording, store *FixtureStore) graph.Graph {
	t.Helper()

	if store == nil {
		store = NewFixtureStore(rec)
	}
	var chain []core.Node
	for _, node := range rec.Nodes {
		if len(node.LLM) > 0 {
			client := NewFixtureLLMClient(store, FixtureMissFail, nil).ForNode(node.NodeID)
			chain = append(chain, nodes.NewLLMNode(node.NodeID, client,
				nodes.LLMNodeConfig{
					Model:          node.LLM[0].Model,
					PromptTemplate: recordPromptTemplate,
					OutputKey:      node.NodeID + "_output",
				}))
			continue
		}
		chain = append(chain, core.NewNoopNode(node.NodeID))
	}

	builder := graph.NewGraphBuilder("replay-run").AddNode(chain[0])
	for _, node := range chain[1:] {
		builder = builder.Edge(node)
	}
	g, err := builder.Build()
	if err != nil {
		t.Fatalf("build replay graph: %v", err)
	}
	return g
}

func TestReplayOfflineProducesRecordedOutputs(t *testing.T) {
	recording, wantAnswer := recordRun(t)

	replayer := NewReplayer(recording, ReplayOptions{})
	result, err := replayer.Replay(context.Background(), buildReplayGraph(t, recording, nil), runtime.RunOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	// The replay produced the recorded outputs.
	if result.Final == nil {
		t.Fatal("replay returned nil envelope")
	}
	got, _ := result.Final.GetVar("answer_output")
	if got != wantAnswer {
		t.Errorf("replayed output = %v, want recorded answer", got)
	}

	// The replay's own recording captured the fixture-backed LLM interaction.
	if len(result.Recording.Nodes) == 0 || len(result.Recording.Nodes[0].LLM) != 1 {
		t.Fatalf("replay recording missing LLM records: %+v", result.Recording.Nodes)
	}
	if result.Recording.Nodes[0].LLM[0].Completion != wantAnswer {
		t.Errorf("replay LLM record = %+v", result.Recording.Nodes[0].LLM[0])
	}

	// Verify finds no differences between original and replay recordings.
	if diffs := Verify(recording, result.Recording); len(diffs) != 0 {
		t.Errorf("Verify differences: %+v", diffs)
	}
}

func TestReplayNonReplayableFail(t *testing.T) {
	human := nodes.NewHumanNode("approve", nodes.HumanNodeConfig{
		RequestType: nodes.HumanRequestApproval,
		Prompt:      "approve?",
	})
	g, err := graph.NewGraphBuilder("human-run").AddNode(human).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	recording := minimalRecordingWithNode("approve", "human")
	replayer := NewReplayer(recording, ReplayOptions{})

	_, err = replayer.Replay(context.Background(), g, runtime.RunOptions{})
	if err == nil {
		t.Fatal("expected failure for non-replayable node under fail policy")
	}
	if !strings.Contains(err.Error(), "non-replayable") {
		t.Errorf("error should name the problem: %v", err)
	}
}

func TestReplayNonReplayableStubUsesRecordedOutput(t *testing.T) {
	state := &EnvelopeState{Vars: map[string]any{"approval": "approved"}}
	recording := minimalRecordingWithNodeState("approve", "human", state)
	replayer := NewReplayer(recording, ReplayOptions{
		OnNonReplayable: ReplayNonReplayableStub,
	})

	// The live graph contains a human node wired to no handler: if the
	// stub failed to substitute, the run would fail with ErrHumanPending
	// instead of completing.
	human := nodes.NewHumanNode("approve", nodes.HumanNodeConfig{
		RequestType: nodes.HumanRequestApproval,
		Prompt:      "approve?",
	})
	g, err := graph.NewGraphBuilder("human-run").AddNode(human).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	result, err := replayer.Replay(context.Background(), g, runtime.RunOptions{})
	if err != nil {
		t.Fatalf("replay with stub: %v", err)
	}

	approval, _ := result.Final.GetVar("approval")
	if approval != "approved" {
		t.Errorf("stubbed output = %v, want recorded approval", approval)
	}
}

func TestReplayNonReplayableStubWithoutRecordedOutput(t *testing.T) {
	recording := minimalRecordingWithNode("approve", "human")
	replayer := NewReplayer(recording, ReplayOptions{
		OnNonReplayable: ReplayNonReplayableStub,
	})

	human := nodes.NewHumanNode("approve", nodes.HumanNodeConfig{RequestType: nodes.HumanRequestApproval})
	g, _ := graph.NewGraphBuilder("human-run").AddNode(human).Build()

	_, err := replayer.Replay(context.Background(), g, runtime.RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "no captured output") {
		t.Errorf("expected error for missing stub output, got %v", err)
	}
}

func TestReplayAwareMarkers(t *testing.T) {
	webhook := nodes.NewWebhookCallNode("notify", nodes.WebhookCallNodeConfig{URL: "https://example.com"})
	if aware, ok := core.Node(webhook).(core.ReplayAware); !ok || aware.Replayable() {
		t.Error("webhook_call must be marked non-replayable")
	}

	trigger := nodes.NewWebhookTriggerNode("trigger", nodes.WebhookTriggerNodeConfig{})
	if aware, ok := core.Node(trigger).(core.ReplayAware); !ok || aware.Replayable() {
		t.Error("webhook_trigger must be marked non-replayable")
	}

	human := nodes.NewHumanNode("gate", nodes.HumanNodeConfig{})
	if aware, ok := core.Node(human).(core.ReplayAware); !ok || aware.Replayable() {
		t.Error("human must be marked non-replayable")
	}

	// Nodes without the marker are replayable by default.
	llm := nodes.NewLLMNode("a", &scriptedClient{}, nodes.LLMNodeConfig{})
	if _, ok := core.Node(llm).(core.ReplayAware); ok {
		t.Error("llm nodes should not implement ReplayAware")
	}
}

func TestReplayFixtureMissFailsRun(t *testing.T) {
	recording, _ := recordRun(t)

	// Swap in an empty fixture store: every interaction misses. The miss
	// policy is configured on the fixture client at graph construction.
	empty := NewFixtureStore(&Recording{Version: FormatVersion, RunID: "x"})
	replayer := NewReplayer(recording, ReplayOptions{})

	_, err := replayer.Replay(context.Background(), buildReplayGraph(t, recording, empty), runtime.RunOptions{})
	if err == nil {
		t.Fatal("expected fixture miss to fail the run")
	}
	if !errors.Is(err, ErrNoFixture) {
		t.Errorf("error should wrap ErrNoFixture: %v", err)
	}
}

func TestReplayDeterministicTiming(t *testing.T) {
	recording, _ := recordRun(t)
	replayer := NewReplayer(recording, ReplayOptions{})

	result, err := replayer.Replay(context.Background(), buildReplayGraph(t, recording, nil), runtime.RunOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	// The frozen clock makes elapsed measurements deterministic: every
	// captured node execution reports zero elapsed time.
	for _, node := range result.Recording.Nodes {
		if node.ElapsedMS != 0 {
			t.Errorf("node %s elapsed = %dms, want 0 under frozen clock", node.NodeID, node.ElapsedMS)
		}
	}
}

func TestReplayParallelMapFixtures(t *testing.T) {
	// A map node fans items out to a mapper LLM node concurrently; all
	// branches share one fixture store. The mapper prompt is constant so
	// each item's request fingerprints to the same recorded interaction.
	recording := &Recording{
		Version: FormatVersion,
		RunID:   "run-parallel",
		Status:  "succeeded",
		Nodes: []NodeRecord{
			{
				NodeID: "process",
				Input:  &EnvelopeState{Input: "item"},
				LLM: []LLMCallRecord{{
					NodeID:     "process",
					Model:      "m",
					InputText:  "process item",
					Completion: "processed",
				}},
			},
			{
				NodeID: "process",
				LLM: []LLMCallRecord{{
					NodeID:     "process",
					Model:      "m",
					InputText:  "process item",
					Completion: "processed",
				}},
			},
			{
				NodeID: "process",
				LLM: []LLMCallRecord{{
					NodeID:     "process",
					Model:      "m",
					InputText:  "process item",
					Completion: "processed",
				}},
			},
		},
	}

	store := NewFixtureStore(recording)
	mapper := nodes.NewLLMNode("process", NewFixtureLLMClient(store, FixtureMissFail, nil), nodes.LLMNodeConfig{
		Model:          "m",
		PromptTemplate: "process item",
		OutputKey:      "mapped",
	})

	items := []any{"one", "two", "three"}
	env := core.NewEnvelope()
	env.SetVar("collection", items)

	mapNode := nodes.NewMapNode("fanout", nodes.MapNodeConfig{
		InputVar:    "collection",
		OutputVar:   "results",
		MapperNode:  mapper,
		Concurrency: 3,
	})

	g, err := graph.NewGraphBuilder("parallel-map").AddNode(mapNode).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	result, err := runtime.NewRuntime().Run(context.Background(), g, env, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	results, ok := result.GetVar("results")
	if !ok {
		t.Fatal("map results missing")
	}
	collected, err := resultsAsSlice(results)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if len(collected) != len(items) {
		t.Fatalf("collected %d results, want %d", len(collected), len(items))
	}
	for _, r := range collected {
		itemVars, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("mapped item = %T, want var map", r)
		}
		if itemVars["mapped"] != "processed" {
			t.Errorf("mapped result = %v, want recorded completion", itemVars["mapped"])
		}
	}
}

// resultsAsSlice normalizes map output collections to a slice.
func resultsAsSlice(v any) ([]any, error) {
	switch typed := v.(type) {
	case []any:
		return typed, nil
	case []string:
		out := make([]any, len(typed))
		for i, s := range typed {
			out[i] = s
		}
		return out, nil
	case nil:
		return nil, fmt.Errorf("nil results")
	default:
		return nil, fmt.Errorf("unexpected results type %T", v)
	}
}

func TestReplayToolRetriesDeterministic(t *testing.T) {
	// A recorded tool result replays deterministically even with retries
	// configured: the fixture never fails transiently.
	recording := &Recording{
		Version: FormatVersion,
		RunID:   "run-tool",
		Status:  "succeeded",
		Nodes: []NodeRecord{{
			NodeID: "fetch",
			Tools: []ToolCallRecord{{
				NodeID:   "fetch",
				ToolName: "lookup",
				Args:     map[string]any{"id": "42"},
				Result:   map[string]any{"name": "recorded"},
			}},
		}},
	}

	for i := 0; i < 3; i++ {
		// Fixtures are consumed once per replay: build a fresh store each run.
		store := NewFixtureStore(recording)
		tool := NewFixtureTool(&scriptedTool{name: "lookup"}, store, FixtureMissFail)

		toolNode := nodes.NewToolNode("fetch", tool, nodes.ToolNodeConfig{
			ToolName:   "lookup",
			StaticArgs: map[string]any{"id": "42"},
			OutputKey:  "result",
		})
		g, err := graph.NewGraphBuilder("tools").AddNode(toolNode).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}

		result, err := runtime.NewRuntime().Run(context.Background(), g, core.NewEnvelope(), runtime.DefaultRunOptions())
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		got, _ := result.GetVar("result")
		if m, ok := got.(map[string]any); !ok || m["name"] != "recorded" {
			t.Fatalf("run %d: result = %v", i, got)
		}
	}
}

func TestVerifyDifferences(t *testing.T) {
	original := fixtureRecording()

	t.Run("identical", func(t *testing.T) {
		replayed := fixtureRecording()
		if diffs := Verify(original, replayed); len(diffs) != 0 {
			t.Errorf("expected no differences, got %+v", diffs)
		}
	})

	t.Run("node sequence changed", func(t *testing.T) {
		replayed := fixtureRecording()
		replayed.Nodes = replayed.Nodes[:1]
		diffs := Verify(original, replayed)
		if len(diffs) == 0 || diffs[0].Kind != "node_sequence" {
			t.Errorf("expected node_sequence difference, got %+v", diffs)
		}
	})

	t.Run("output content changed", func(t *testing.T) {
		replayed := fixtureRecording()
		replayed.Nodes[0].Output = &EnvelopeState{Input: "changed"}
		diffs := Verify(original, replayed)
		if len(diffs) == 0 || diffs[0].Kind != "node_output" || diffs[0].NodeID != "answer" {
			t.Errorf("expected node_output difference, got %+v", diffs)
		}
	})

	t.Run("llm completion changed", func(t *testing.T) {
		replayed := fixtureRecording()
		replayed.Nodes[0].LLM[0].Completion = "different answer"
		diffs := Verify(original, replayed)
		found := false
		for _, d := range diffs {
			if d.Kind == "llm_calls" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected llm_calls difference, got %+v", diffs)
		}
	})

	t.Run("status changed", func(t *testing.T) {
		replayed := fixtureRecording()
		replayed.Status = "failed"
		diffs := Verify(original, replayed)
		found := false
		for _, d := range diffs {
			if d.Kind == "run_status" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected run_status difference, got %+v", diffs)
		}
	})

	t.Run("tool result changed", func(t *testing.T) {
		replayed := fixtureRecording()
		replayed.Nodes[0].Tools = []ToolCallRecord{{
			NodeID: "answer", ToolName: "lookup", Result: map[string]any{"name": "changed"},
		}}
		originalWithTool := fixtureRecording()
		originalWithTool.Nodes[0].Tools = []ToolCallRecord{{
			NodeID: "answer", ToolName: "lookup", Result: map[string]any{"name": "recorded"},
		}}
		diffs := Verify(originalWithTool, replayed)
		found := false
		for _, d := range diffs {
			if d.Kind == "tool_calls" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected tool_calls difference, got %+v", diffs)
		}
	})

	t.Run("nil recordings", func(t *testing.T) {
		if diffs := Verify(nil, original); len(diffs) == 0 {
			t.Error("nil original should differ")
		}
	})
}

// minimalRecordingWithNode builds a recording with one node record carrying
// no captured output.
func minimalRecordingWithNode(nodeID, kind string) *Recording {
	return &Recording{
		Version: FormatVersion,
		RunID:   "run-min",
		Status:  "succeeded",
		Nodes: []NodeRecord{
			{NodeID: nodeID, NodeKind: kind},
		},
	}
}

// minimalRecordingWithNodeState builds a recording whose node record has an
// output envelope state.
func minimalRecordingWithNodeState(nodeID, kind string, state *EnvelopeState) *Recording {
	rec := minimalRecordingWithNode(nodeID, kind)
	rec.Nodes[0].Output = state
	return rec
}
