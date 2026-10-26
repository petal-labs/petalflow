package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/runtime"
)

// NonReplayablePolicy defines what the replay runner does when it reaches a
// node marked non-replayable (core.ReplayAware returning false).
type NonReplayablePolicy string

const (
	// ReplayNonReplayableFail fails the replay before execution starts
	// (default). Non-replayable nodes are never silently invoked.
	ReplayNonReplayableFail NonReplayablePolicy = "fail"

	// ReplayNonReplayableStub substitutes the node's recorded output
	// envelope from the capture artifact instead of executing it.
	ReplayNonReplayableStub NonReplayablePolicy = "stub"
)

// ReplayOptions configures an offline replay.
type ReplayOptions struct {
	// OnNonReplayable selects behavior for non-replayable nodes.
	// Defaults to ReplayNonReplayableFail.
	//
	// Fixture behavior (matching, miss policy, live fallback) is configured
	// on the replay.FixtureLLMClient / replay.FixtureTool instances used to
	// build the graph, since fixture-backed dependencies are injected at
	// graph construction time.
	OnNonReplayable NonReplayablePolicy
}

// ReplayResult carries the outputs of an offline replay.
type ReplayResult struct {
	// Final is the final envelope of the replayed run.
	Final *core.Envelope

	// Recording is the recording of the replay run itself (captured at
	// full fidelity internally) so callers can diff it against the original
	// with Verify.
	Recording *Recording
}

// Replayer replays a captured run offline against a caller-constructed
// graph. The graph must be the same workflow that was captured; build it
// with fixture-backed dependencies for full determinism:
//
//	store := replay.NewFixtureStore(recording)
//	client := replay.NewFixtureLLMClient(store, replay.FixtureMissFail, nil).ForNode("answer")
//	// build the graph with client, then:
//	r := replay.NewReplayer(recording, replay.ReplayOptions{})
//	result, err := r.Replay(ctx, g, runtime.RunOptions{})
//	diffs := replay.Verify(recording, result.Recording)
type Replayer struct {
	rec  *Recording
	opts ReplayOptions
}

// NewReplayer creates a replay runner for a captured recording.
func NewReplayer(rec *Recording, opts ReplayOptions) *Replayer {
	if opts.OnNonReplayable == "" {
		opts.OnNonReplayable = ReplayNonReplayableFail
	}
	return &Replayer{rec: rec, opts: opts}
}

// Replay executes the graph offline and returns the final envelope plus the
// replay's own recording for verification.
//
// Determinism rules:
//   - The runtime clock is frozen at the recording's start time, so elapsed
//     measurements are deterministic (zero). Event timestamps and token
//     usage metadata are observation-time values and are not reproduced.
//   - Node timeouts are disabled: a node that timed out during the live run
//     executes to completion offline. Node retries do not fire because
//     fixture-backed dependencies never fail transiently.
//   - Parallel maps execute through the same runtime path as live runs;
//     fixture stores are concurrency-safe and consume fixtures once, so
//     branch interleavings cannot corrupt matching.
//   - Human gates cannot wait offline: they are covered by the
//     non-replayable policy (fail, or stub from the recording). To exercise
//     a human gate interactively, run the live runtime instead.
func (r *Replayer) Replay(ctx context.Context, g graph.Graph, base runtime.RunOptions) (*ReplayResult, error) {
	target, err := r.prepareGraph(g)
	if err != nil {
		return nil, err
	}

	// Capture the replay run at full fidelity so Verify can diff outputs.
	capture := NewCapture(AllowAllPolicy())

	// Thread the replayed run ID through to fixture clients so their
	// emitted events carry it.
	ctx = context.WithValue(ctx, replayRunIDKey{}, r.rec.RunID)

	opts := base
	// Freeze the clock at the recording's start so elapsed measurements are
	// deterministic (zero) during replay.
	opts.Now = func() time.Time { return r.rec.StartedAt }
	if base.Now != nil {
		baseNow := base.Now
		opts.Now = baseNow
	}
	// Timeouts are wall-clock dependent and non-deterministic offline.
	opts.NodeTimeout = 0
	opts.EventHandler = capture.Handle
	opts.NodeRecorder = capture
	if base.EventHandler != nil {
		baseHandler := base.EventHandler
		opts.EventHandler = func(e runtime.Event) {
			baseHandler(e)
			capture.Handle(e)
		}
	}

	// Seed the initial envelope from the recording so the replay starts
	// from the recorded input (falling back to an empty envelope).
	initial := initialEnvelope(r.rec)

	final, runErr := runtime.NewRuntime().Run(ctx, target, initial, opts)
	return &ReplayResult{
		Final:     final,
		Recording: capture.Finish(),
	}, runErr
}

// initialEnvelope reconstructs the starting envelope from the recording.
func initialEnvelope(rec *Recording) *core.Envelope {
	for i := range rec.Nodes {
		if state := rec.Nodes[i].Input; state != nil {
			return envelopeFromState(state)
		}
	}
	if rec.Inputs != nil {
		return core.NewEnvelope().WithInput(rec.Inputs)
	}
	return core.NewEnvelope()
}

// prepareGraph validates that the graph matches the recording and applies
// the non-replayable policy, returning the graph to execute.
func (r *Replayer) prepareGraph(g graph.Graph) (graph.Graph, error) {
	byID := make(map[string]*NodeRecord, len(r.rec.Nodes))
	for i := range r.rec.Nodes {
		// Keep the last record per node ID for stub lookup.
		byID[r.rec.Nodes[i].NodeID] = &r.rec.Nodes[i]
	}

	var blocked []core.Node
	for _, node := range g.Nodes() {
		if aware, ok := node.(core.ReplayAware); ok && !aware.Replayable() {
			blocked = append(blocked, node)
		}
	}

	if len(blocked) == 0 {
		return g, nil
	}

	if r.opts.OnNonReplayable != ReplayNonReplayableStub {
		ids := make([]string, 0, len(blocked))
		for _, node := range blocked {
			ids = append(ids, node.ID())
		}
		return nil, fmt.Errorf(
			"replay: graph contains non-replayable nodes %s; they would perform external side effects or require live interaction (use ReplayNonReplayableStub to substitute recorded outputs)",
			joinIDs(ids))
	}

	decorated := &replayGraph{
		Graph:    g,
		stubs:    make(map[string]core.Node, len(blocked)),
		replaced: make(map[string]bool, len(blocked)),
	}
	for _, node := range blocked {
		record, ok := byID[node.ID()]
		if !ok || record.Output == nil {
			return nil, fmt.Errorf(
				"replay: node %q is non-replayable and the recording has no captured output to stub it with",
				node.ID())
		}
		decorated.stubs[node.ID()] = newStubNode(node.ID(), node.Kind(), record.Output)
		decorated.replaced[node.ID()] = true
	}
	return decorated, nil
}

// replayGraph decorates a graph, substituting stub nodes for non-replayable
// nodes. All structure (name, edges, entry, ordering) is forwarded.
type replayGraph struct {
	graph.Graph
	stubs    map[string]core.Node
	replaced map[string]bool
}

// NodeByID returns the stub for replaced nodes, the original otherwise.
func (d *replayGraph) NodeByID(id string) (core.Node, bool) {
	if stub, ok := d.stubs[id]; ok {
		return stub, true
	}
	return d.Graph.NodeByID(id)
}

// Nodes returns the graph's nodes with stubs substituted.
func (d *replayGraph) Nodes() []core.Node {
	nodes := d.Graph.Nodes()
	out := make([]core.Node, len(nodes))
	for i, node := range nodes {
		if stub, ok := d.stubs[node.ID()]; ok {
			out[i] = stub
		} else {
			out[i] = node
		}
	}
	return out
}

// stubNode replays a non-replayable node by returning its recorded output.
type stubNode struct {
	core.BaseNode
	output *EnvelopeState
}

func newStubNode(id string, kind core.NodeKind, output *EnvelopeState) *stubNode {
	return &stubNode{
		BaseNode: core.NewBaseNode(id, kind),
		output:   output,
	}
}

// Run returns the recorded output envelope. The stub never executes the
// original node.
func (s *stubNode) Run(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return envelopeFromState(s.output), nil
}

// envelopeFromState rebuilds an envelope from a captured snapshot.
func envelopeFromState(state *EnvelopeState) *core.Envelope {
	env := core.NewEnvelope()
	if state == nil {
		return env
	}
	env.Input = state.Input
	for k, v := range state.Vars {
		env.SetVar(k, v)
	}
	for _, a := range state.Artifacts {
		env.AppendArtifact(core.Artifact{
			ID:       a.ID,
			Type:     a.Type,
			MimeType: a.MimeType,
			URI:      a.URI,
			Text:     a.Text,
			Bytes:    a.Bytes,
			Meta:     a.Meta,
		})
	}
	for _, m := range state.Messages {
		env.AppendMessage(core.Message{
			Role:    m.Role,
			Name:    m.Name,
			Content: m.Content,
			Meta:    m.Meta,
		})
	}
	return env
}

// joinIDs joins node IDs for error messages.
func joinIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%q", id)
	}
	return out
}

// Difference describes a mismatch between an original recording and the
// recording of its replay.
type Difference struct {
	NodeID  string // empty for run-level differences
	Kind    string // "node_sequence", "node_output", "run_status"
	Message string
}

// Verify compares an original recording against the recording of its replay
// and returns the differences. An empty slice means the replay reproduced
// the recorded outputs: same node sequence, same per-node output content,
// same terminal status. Usage/latency are not compared (they vary between
// runs by design).
func Verify(original, replayed *Recording) []Difference {
	var diffs []Difference

	if original == nil || replayed == nil {
		return []Difference{{Kind: "recording", Message: "nil recording"}}
	}

	if original.Status != replayed.Status {
		diffs = append(diffs, Difference{
			Kind:    "run_status",
			Message: fmt.Sprintf("status %q replayed as %q", original.Status, replayed.Status),
		})
	}

	if len(original.Nodes) != len(replayed.Nodes) {
		diffs = append(diffs, Difference{
			Kind: "node_sequence",
			Message: fmt.Sprintf("recorded %d node executions, replay executed %d",
				len(original.Nodes), len(replayed.Nodes)),
		})
	}

	n := min(len(original.Nodes), len(replayed.Nodes))
	for i := 0; i < n; i++ {
		orig, rep := original.Nodes[i], replayed.Nodes[i]
		if orig.NodeID != rep.NodeID || orig.NodeKind != rep.NodeKind {
			diffs = append(diffs, Difference{
				NodeID: rep.NodeID,
				Kind:   "node_sequence",
				Message: fmt.Sprintf("execution %d: recorded %s(%s), replayed %s(%s)",
					i, orig.NodeID, orig.NodeKind, rep.NodeID, rep.NodeKind),
			})
			continue
		}
		if !sameState(orig.Output, rep.Output) {
			diffs = append(diffs, Difference{
				NodeID:  rep.NodeID,
				Kind:    "node_output",
				Message: fmt.Sprintf("execution %d (%s): output differs from recording", i, rep.NodeID),
			})
		}
		diffs = append(diffs, verifyInteractions(i, orig, rep)...)
	}
	return diffs
}

// verifyInteractions compares the recorded LLM calls and tool invocations
// of a node execution.
func verifyInteractions(seq int, orig, rep NodeRecord) []Difference {
	var diffs []Difference
	if len(orig.LLM) != len(rep.LLM) {
		diffs = append(diffs, Difference{
			NodeID: rep.NodeID,
			Kind:   "llm_calls",
			Message: fmt.Sprintf("execution %d (%s): recorded %d LLM calls, replayed %d",
				seq, rep.NodeID, len(orig.LLM), len(rep.LLM)),
		})
	}
	for i := 0; i < min(len(orig.LLM), len(rep.LLM)); i++ {
		o, r := orig.LLM[i], rep.LLM[i]
		if o.Model != r.Model || o.Completion != r.Completion || o.StopReason != r.StopReason ||
			o.Usage != r.Usage {
			diffs = append(diffs, Difference{
				NodeID:  rep.NodeID,
				Kind:    "llm_calls",
				Message: fmt.Sprintf("execution %d (%s): LLM call %d differs from recording", seq, rep.NodeID, i),
			})
		}
	}
	if len(orig.Tools) != len(rep.Tools) {
		diffs = append(diffs, Difference{
			NodeID: rep.NodeID,
			Kind:   "tool_calls",
			Message: fmt.Sprintf("execution %d (%s): recorded %d tool calls, replayed %d",
				seq, rep.NodeID, len(orig.Tools), len(rep.Tools)),
		})
	}
	for i := 0; i < min(len(orig.Tools), len(rep.Tools)); i++ {
		o, r := orig.Tools[i], rep.Tools[i]
		if o.ToolName != r.ToolName || o.IsError != r.IsError || !sameMap(o.Result, r.Result) {
			diffs = append(diffs, Difference{
				NodeID:  rep.NodeID,
				Kind:    "tool_calls",
				Message: fmt.Sprintf("execution %d (%s): tool call %d differs from recording", seq, rep.NodeID, i),
			})
		}
	}
	return diffs
}

// sameMap compares two tool result maps for content equality.
func sameMap(a, b map[string]any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	aJSON, aErr := json.Marshal(a)
	bJSON, bErr := json.Marshal(b)
	if aErr != nil || bErr != nil {
		return false
	}
	return string(aJSON) == string(bJSON)
}

// sameState compares two envelope snapshots for content equality.
func sameState(a, b *EnvelopeState) bool {
	if a == nil || b == nil {
		return a == b
	}
	aJSON, aErr := json.Marshal(a)
	bJSON, bErr := json.Marshal(b)
	if aErr != nil || bErr != nil {
		return false
	}
	return string(aJSON) == string(bJSON)
}

// ErrReplayMismatch is returned when replay verification is strict and
// differences exist.
var ErrReplayMismatch = errors.New("replay differs from recording")
