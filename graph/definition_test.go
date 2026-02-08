package graph

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/petal-labs/petalflow/core"
)

func TestGraphDefinition_JSONRoundTrip(t *testing.T) {
	gd := GraphDefinition{
		ID:      "test_workflow",
		Version: "1.0",
		Metadata: map[string]string{
			"source_kind": "agent_workflow",
		},
		Nodes: []NodeDef{
			{ID: "a", Type: "llm_prompt", Config: map[string]any{"model": "gpt-4"}},
			{ID: "b", Type: "transform"},
		},
		Edges: []EdgeDef{
			{Source: "a", SourceHandle: "output", Target: "b", TargetHandle: "input"},
		},
		Entry: "a",
	}

	data, err := json.Marshal(gd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got GraphDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != gd.ID {
		t.Errorf("ID = %q, want %q", got.ID, gd.ID)
	}
	if got.Version != gd.Version {
		t.Errorf("Version = %q, want %q", got.Version, gd.Version)
	}
	if got.Entry != gd.Entry {
		t.Errorf("Entry = %q, want %q", got.Entry, gd.Entry)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("Nodes count = %d, want 2", len(got.Nodes))
	}
	if got.Nodes[0].Type != "llm_prompt" {
		t.Errorf("Nodes[0].Type = %q, want %q", got.Nodes[0].Type, "llm_prompt")
	}
	if len(got.Edges) != 1 {
		t.Fatalf("Edges count = %d, want 1", len(got.Edges))
	}
	if got.Edges[0].SourceHandle != "output" {
		t.Errorf("Edges[0].SourceHandle = %q, want %q", got.Edges[0].SourceHandle, "output")
	}
	if got.Metadata["source_kind"] != "agent_workflow" {
		t.Errorf("Metadata[source_kind] = %q, want %q", got.Metadata["source_kind"], "agent_workflow")
	}
}

func TestGraphDefinition_JSONOmitsEmpty(t *testing.T) {
	gd := GraphDefinition{
		ID:      "minimal",
		Version: "1.0",
		Nodes:   []NodeDef{{ID: "a", Type: "noop"}},
		Edges:   []EdgeDef{},
	}

	data, err := json.Marshal(gd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// metadata and entry should be omitted
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, ok := raw["metadata"]; ok {
		t.Error("metadata should be omitted when nil")
	}
	if _, ok := raw["entry"]; ok {
		t.Error("entry should be omitted when empty")
	}
}

// --- Validate tests ---

func TestValidate_ValidGraph(t *testing.T) {
	gd := GraphDefinition{
		ID:      "valid",
		Version: "1.0",
		Nodes: []NodeDef{
			{ID: "a", Type: "llm_prompt"},
			{ID: "b", Type: "transform"},
		},
		Edges: []EdgeDef{
			{Source: "a", SourceHandle: "output", Target: "b", TargetHandle: "input"},
		},
		Entry: "a",
	}

	diags := gd.Validate()
	if HasErrors(diags) {
		t.Errorf("expected no errors, got: %v", diags)
	}
}

func TestValidate_SingleNode(t *testing.T) {
	gd := GraphDefinition{
		ID:      "single",
		Version: "1.0",
		Nodes:   []NodeDef{{ID: "only", Type: "noop"}},
		Edges:   []EdgeDef{},
		Entry:   "only",
	}

	diags := gd.Validate()
	if HasErrors(diags) {
		t.Errorf("single node graph should be valid, got: %v", diags)
	}
	if len(Warnings(diags)) != 0 {
		t.Errorf("single node graph should have no warnings, got: %v", Warnings(diags))
	}
}

func TestValidate_GR001_UnknownEdgeSource(t *testing.T) {
	gd := GraphDefinition{
		ID:      "bad_source",
		Version: "1.0",
		Nodes:   []NodeDef{{ID: "a", Type: "noop"}},
		Edges: []EdgeDef{
			{Source: "missing", SourceHandle: "output", Target: "a", TargetHandle: "input"},
		},
		Entry: "a",
	}

	diags := gd.Validate()
	found := findDiag(diags, "GR-001")
	if found == nil {
		t.Fatal("expected GR-001 diagnostic for unknown source")
	}
	if found.Severity != SeverityError {
		t.Errorf("GR-001 severity = %q, want %q", found.Severity, SeverityError)
	}
}

func TestValidate_GR001_UnknownEdgeTarget(t *testing.T) {
	gd := GraphDefinition{
		ID:      "bad_target",
		Version: "1.0",
		Nodes:   []NodeDef{{ID: "a", Type: "noop"}},
		Edges: []EdgeDef{
			{Source: "a", SourceHandle: "output", Target: "missing", TargetHandle: "input"},
		},
		Entry: "a",
	}

	diags := gd.Validate()
	found := findDiag(diags, "GR-001")
	if found == nil {
		t.Fatal("expected GR-001 diagnostic for unknown target")
	}
}

func TestValidate_GR002_OrphanNode(t *testing.T) {
	gd := GraphDefinition{
		ID:      "orphan",
		Version: "1.0",
		Nodes: []NodeDef{
			{ID: "a", Type: "noop"},
			{ID: "b", Type: "noop"},
			{ID: "orphan", Type: "noop"},
		},
		Edges: []EdgeDef{
			{Source: "a", SourceHandle: "output", Target: "b", TargetHandle: "input"},
		},
		Entry: "a",
	}

	diags := gd.Validate()
	warnings := Warnings(diags)
	if len(warnings) == 0 {
		t.Fatal("expected GR-002 warning for orphan node")
	}
	found := findDiag(diags, "GR-002")
	if found == nil {
		t.Fatal("expected GR-002 diagnostic")
	}
	if found.Severity != SeverityWarning {
		t.Errorf("GR-002 severity = %q, want %q", found.Severity, SeverityWarning)
	}
}

func TestValidate_GR002_EntryNodeNotOrphan(t *testing.T) {
	// An entry node with outbound edges but no inbound is NOT orphan
	gd := GraphDefinition{
		ID:      "entry_ok",
		Version: "1.0",
		Nodes: []NodeDef{
			{ID: "entry", Type: "noop"},
			{ID: "next", Type: "noop"},
		},
		Edges: []EdgeDef{
			{Source: "entry", SourceHandle: "output", Target: "next", TargetHandle: "input"},
		},
		Entry: "entry",
	}

	diags := gd.Validate()
	found := findDiag(diags, "GR-002")
	if found != nil {
		t.Errorf("entry node with outbound edges should not be flagged as orphan")
	}
}

func TestValidate_GR004_CycleDetected(t *testing.T) {
	gd := GraphDefinition{
		ID:      "cycle",
		Version: "1.0",
		Nodes: []NodeDef{
			{ID: "a", Type: "noop"},
			{ID: "b", Type: "noop"},
			{ID: "c", Type: "noop"},
		},
		Edges: []EdgeDef{
			{Source: "a", SourceHandle: "out", Target: "b", TargetHandle: "in"},
			{Source: "b", SourceHandle: "out", Target: "c", TargetHandle: "in"},
			{Source: "c", SourceHandle: "out", Target: "a", TargetHandle: "in"},
		},
		Entry: "a",
	}

	diags := gd.Validate()
	found := findDiag(diags, "GR-004")
	if found == nil {
		t.Fatal("expected GR-004 diagnostic for cycle")
	}
	if found.Severity != SeverityError {
		t.Errorf("GR-004 severity = %q, want %q", found.Severity, SeverityError)
	}
}

func TestValidate_GR004_NoCycleInDAG(t *testing.T) {
	gd := GraphDefinition{
		ID:      "dag",
		Version: "1.0",
		Nodes: []NodeDef{
			{ID: "a", Type: "noop"},
			{ID: "b", Type: "noop"},
			{ID: "c", Type: "noop"},
		},
		Edges: []EdgeDef{
			{Source: "a", SourceHandle: "out", Target: "b", TargetHandle: "in"},
			{Source: "a", SourceHandle: "out", Target: "c", TargetHandle: "in"},
			{Source: "b", SourceHandle: "out", Target: "c", TargetHandle: "in"},
		},
		Entry: "a",
	}

	diags := gd.Validate()
	found := findDiag(diags, "GR-004")
	if found != nil {
		t.Errorf("DAG should not trigger cycle detection, got: %v", found)
	}
}

func TestValidate_GR005_DuplicateNodeID(t *testing.T) {
	gd := GraphDefinition{
		ID:      "dupes",
		Version: "1.0",
		Nodes: []NodeDef{
			{ID: "a", Type: "noop"},
			{ID: "a", Type: "transform"},
		},
		Edges: []EdgeDef{},
		Entry: "a",
	}

	diags := gd.Validate()
	found := findDiag(diags, "GR-005")
	if found == nil {
		t.Fatal("expected GR-005 diagnostic for duplicate ID")
	}
	if found.Severity != SeverityError {
		t.Errorf("GR-005 severity = %q, want %q", found.Severity, SeverityError)
	}
}

func TestValidate_GR007_InvalidEntry(t *testing.T) {
	gd := GraphDefinition{
		ID:      "bad_entry",
		Version: "1.0",
		Nodes:   []NodeDef{{ID: "a", Type: "noop"}},
		Edges:   []EdgeDef{},
		Entry:   "nonexistent",
	}

	diags := gd.Validate()
	found := findDiag(diags, "GR-007")
	if found == nil {
		t.Fatal("expected GR-007 diagnostic for invalid entry")
	}
	if found.Severity != SeverityError {
		t.Errorf("GR-007 severity = %q, want %q", found.Severity, SeverityError)
	}
}

func TestValidate_GR007_EmptyEntryIsOK(t *testing.T) {
	gd := GraphDefinition{
		ID:      "no_entry",
		Version: "1.0",
		Nodes:   []NodeDef{{ID: "a", Type: "noop"}},
		Edges:   []EdgeDef{},
	}

	diags := gd.Validate()
	found := findDiag(diags, "GR-007")
	if found != nil {
		t.Errorf("empty entry should not trigger GR-007")
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	gd := GraphDefinition{
		ID:      "many_errors",
		Version: "1.0",
		Nodes: []NodeDef{
			{ID: "a", Type: "noop"},
			{ID: "a", Type: "noop"}, // GR-005: duplicate
		},
		Edges: []EdgeDef{
			{Source: "missing", SourceHandle: "out", Target: "a", TargetHandle: "in"}, // GR-001
		},
		Entry: "gone", // GR-007
	}

	diags := gd.Validate()
	errors := Errors(diags)
	if len(errors) < 3 {
		t.Errorf("expected at least 3 errors, got %d: %v", len(errors), errors)
	}
}

// --- Diagnostic helpers tests ---

func TestHasErrors(t *testing.T) {
	tests := []struct {
		name  string
		diags []Diagnostic
		want  bool
	}{
		{"nil", nil, false},
		{"empty", []Diagnostic{}, false},
		{"warning only", []Diagnostic{{Severity: SeverityWarning}}, false},
		{"error present", []Diagnostic{{Severity: SeverityError}}, true},
		{"mixed", []Diagnostic{{Severity: SeverityWarning}, {Severity: SeverityError}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasErrors(tt.diags); got != tt.want {
				t.Errorf("HasErrors() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestErrors_And_Warnings(t *testing.T) {
	diags := []Diagnostic{
		{Code: "E1", Severity: SeverityError},
		{Code: "W1", Severity: SeverityWarning},
		{Code: "E2", Severity: SeverityError},
		{Code: "W2", Severity: SeverityWarning},
	}

	errs := Errors(diags)
	if len(errs) != 2 {
		t.Errorf("Errors() returned %d, want 2", len(errs))
	}

	warns := Warnings(diags)
	if len(warns) != 2 {
		t.Errorf("Warnings() returned %d, want 2", len(warns))
	}
}

// --- ToGraph tests ---

func TestToGraph_RequiresNodeFactory(t *testing.T) {
	gd := GraphDefinition{
		ID:      "test",
		Version: "1.0",
		Nodes:   []NodeDef{{ID: "a", Type: "noop"}},
	}

	_, err := gd.ToGraph()
	if err == nil {
		t.Fatal("expected error when no node factory provided")
	}
}

func TestToGraph_BasicLinearGraph(t *testing.T) {
	gd := GraphDefinition{
		ID:      "linear",
		Version: "1.0",
		Nodes: []NodeDef{
			{ID: "a", Type: "noop"},
			{ID: "b", Type: "noop"},
			{ID: "c", Type: "noop"},
		},
		Edges: []EdgeDef{
			{Source: "a", SourceHandle: "output", Target: "b", TargetHandle: "input"},
			{Source: "b", SourceHandle: "output", Target: "c", TargetHandle: "input"},
		},
		Entry: "a",
	}

	g, err := gd.ToGraph(WithNodeFactory(noopFactory))
	if err != nil {
		t.Fatalf("ToGraph: %v", err)
	}

	if g.Name() != "linear" {
		t.Errorf("Name = %q, want %q", g.Name(), "linear")
	}
	if len(g.Nodes()) != 3 {
		t.Errorf("Nodes count = %d, want 3", len(g.Nodes()))
	}
	if len(g.Edges()) != 2 {
		t.Errorf("Edges count = %d, want 2", len(g.Edges()))
	}
	if g.Entry() != "a" {
		t.Errorf("Entry = %q, want %q", g.Entry(), "a")
	}

	// Check connectivity
	succs := g.Successors("a")
	if len(succs) != 1 || succs[0] != "b" {
		t.Errorf("Successors(a) = %v, want [b]", succs)
	}
	succs = g.Successors("b")
	if len(succs) != 1 || succs[0] != "c" {
		t.Errorf("Successors(b) = %v, want [c]", succs)
	}
}

func TestToGraph_DefaultEntryNode(t *testing.T) {
	gd := GraphDefinition{
		ID:      "auto_entry",
		Version: "1.0",
		Nodes: []NodeDef{
			{ID: "a", Type: "noop"},
			{ID: "b", Type: "noop"},
		},
		Edges: []EdgeDef{
			{Source: "a", SourceHandle: "output", Target: "b", TargetHandle: "input"},
		},
		// Entry intentionally omitted
	}

	g, err := gd.ToGraph(WithNodeFactory(noopFactory))
	if err != nil {
		t.Fatalf("ToGraph: %v", err)
	}

	// Should auto-select "a" as entry (no inbound edges)
	if g.Entry() != "a" {
		t.Errorf("Entry = %q, want %q (auto-detected)", g.Entry(), "a")
	}
}

func TestToGraph_DefaultEntryFallback(t *testing.T) {
	// All nodes have inbound edges (cycle) — fallback to first node
	gd := GraphDefinition{
		ID:      "all_inbound",
		Version: "1.0",
		Nodes: []NodeDef{
			{ID: "a", Type: "noop"},
			{ID: "b", Type: "noop"},
		},
		Edges: []EdgeDef{
			{Source: "a", SourceHandle: "out", Target: "b", TargetHandle: "in"},
			{Source: "b", SourceHandle: "out", Target: "a", TargetHandle: "in"},
		},
	}

	g, err := gd.ToGraph(WithNodeFactory(noopFactory))
	if err != nil {
		t.Fatalf("ToGraph: %v", err)
	}

	if g.Entry() != "a" {
		t.Errorf("Entry = %q, want %q (fallback to first)", g.Entry(), "a")
	}
}

func TestToGraph_FactoryError(t *testing.T) {
	gd := GraphDefinition{
		ID:      "fail",
		Version: "1.0",
		Nodes:   []NodeDef{{ID: "bad", Type: "unknown_type"}},
	}

	failFactory := func(nd NodeDef) (core.Node, error) {
		return nil, fmt.Errorf("unknown type %q", nd.Type)
	}

	_, err := gd.ToGraph(WithNodeFactory(failFactory))
	if err == nil {
		t.Fatal("expected error from factory failure")
	}
}

func TestToGraph_FanOutTopology(t *testing.T) {
	gd := GraphDefinition{
		ID:      "fanout",
		Version: "1.0",
		Nodes: []NodeDef{
			{ID: "start", Type: "noop"},
			{ID: "branch1", Type: "noop"},
			{ID: "branch2", Type: "noop"},
			{ID: "merge", Type: "noop"},
		},
		Edges: []EdgeDef{
			{Source: "start", SourceHandle: "out", Target: "branch1", TargetHandle: "in"},
			{Source: "start", SourceHandle: "out", Target: "branch2", TargetHandle: "in"},
			{Source: "branch1", SourceHandle: "out", Target: "merge", TargetHandle: "in"},
			{Source: "branch2", SourceHandle: "out", Target: "merge", TargetHandle: "in"},
		},
		Entry: "start",
	}

	g, err := gd.ToGraph(WithNodeFactory(noopFactory))
	if err != nil {
		t.Fatalf("ToGraph: %v", err)
	}

	if len(g.Nodes()) != 4 {
		t.Errorf("Nodes count = %d, want 4", len(g.Nodes()))
	}
	succs := g.Successors("start")
	if len(succs) != 2 {
		t.Errorf("start should have 2 successors, got %d", len(succs))
	}
	preds := g.Predecessors("merge")
	if len(preds) != 2 {
		t.Errorf("merge should have 2 predecessors, got %d", len(preds))
	}
}

// --- test helpers ---

// noopFactory creates a NoopNode for any NodeDef.
func noopFactory(nd NodeDef) (core.Node, error) {
	return core.NewNoopNode(nd.ID), nil
}

// findDiag returns the first diagnostic with the given code, or nil.
func findDiag(diags []Diagnostic, code string) *Diagnostic {
	for i := range diags {
		if diags[i].Code == code {
			return &diags[i]
		}
	}
	return nil
}
