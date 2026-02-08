package loader

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/petal-labs/petalflow/graph"
)

func testdataPath(name string) string {
	return filepath.Join("testdata", name)
}

func TestLoadWorkflow_AgentJSON(t *testing.T) {
	gd, kind, err := LoadWorkflow(testdataPath("agent.json"))
	if err != nil {
		t.Fatalf("LoadWorkflow() error = %v", err)
	}
	if kind != SchemaKindAgent {
		t.Errorf("kind = %q, want %q", kind, SchemaKindAgent)
	}
	if gd.ID != "test_agent" {
		t.Errorf("ID = %q, want %q", gd.ID, "test_agent")
	}
	if len(gd.Nodes) == 0 {
		t.Error("expected at least one node")
	}
}

func TestLoadWorkflow_AgentYAML(t *testing.T) {
	gd, kind, err := LoadWorkflow(testdataPath("agent.yaml"))
	if err != nil {
		t.Fatalf("LoadWorkflow() error = %v", err)
	}
	if kind != SchemaKindAgent {
		t.Errorf("kind = %q, want %q", kind, SchemaKindAgent)
	}
	if gd.ID != "test_agent_yaml" {
		t.Errorf("ID = %q, want %q", gd.ID, "test_agent_yaml")
	}
}

func TestLoadWorkflow_GraphJSON(t *testing.T) {
	gd, kind, err := LoadWorkflow(testdataPath("graph.json"))
	if err != nil {
		t.Fatalf("LoadWorkflow() error = %v", err)
	}
	if kind != SchemaKindGraph {
		t.Errorf("kind = %q, want %q", kind, SchemaKindGraph)
	}
	if gd.ID != "test_graph" {
		t.Errorf("ID = %q, want %q", gd.ID, "test_graph")
	}
	if len(gd.Nodes) != 2 {
		t.Errorf("Nodes count = %d, want 2", len(gd.Nodes))
	}
}

func TestLoadWorkflow_GraphYAML(t *testing.T) {
	gd, kind, err := LoadWorkflow(testdataPath("graph.yaml"))
	if err != nil {
		t.Fatalf("LoadWorkflow() error = %v", err)
	}
	if kind != SchemaKindGraph {
		t.Errorf("kind = %q, want %q", kind, SchemaKindGraph)
	}
	if gd.ID != "test_graph_yaml" {
		t.Errorf("ID = %q, want %q", gd.ID, "test_graph_yaml")
	}
}

func TestLoadWorkflow_InvalidContent(t *testing.T) {
	_, _, err := LoadWorkflow(testdataPath("invalid.json"))
	if err == nil {
		t.Fatal("expected error for invalid content")
	}
}

func TestLoadWorkflow_FileNotFound(t *testing.T) {
	_, _, err := LoadWorkflow("nonexistent.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadAgentWorkflow_Direct(t *testing.T) {
	gd, err := LoadAgentWorkflow(testdataPath("agent.json"))
	if err != nil {
		t.Fatalf("LoadAgentWorkflow() error = %v", err)
	}
	if gd.ID != "test_agent" {
		t.Errorf("ID = %q, want %q", gd.ID, "test_agent")
	}
}

func TestLoadGraphDefinition_Direct(t *testing.T) {
	gd, err := LoadGraphDefinition(testdataPath("graph.json"))
	if err != nil {
		t.Fatalf("LoadGraphDefinition() error = %v", err)
	}
	if gd.ID != "test_graph" {
		t.Errorf("ID = %q, want %q", gd.ID, "test_graph")
	}
}

func TestLoadWorkflow_YAMLProducesSameAsJSON(t *testing.T) {
	jsonGD, _, err := LoadWorkflow(testdataPath("graph.json"))
	if err != nil {
		t.Fatalf("LoadWorkflow(JSON) error = %v", err)
	}
	yamlGD, _, err := LoadWorkflow(testdataPath("graph.yaml"))
	if err != nil {
		t.Fatalf("LoadWorkflow(YAML) error = %v", err)
	}

	// Core structure should match (IDs differ by design)
	if len(jsonGD.Nodes) != len(yamlGD.Nodes) {
		t.Errorf("JSON nodes = %d, YAML nodes = %d", len(jsonGD.Nodes), len(yamlGD.Nodes))
	}
	if len(jsonGD.Edges) != len(yamlGD.Edges) {
		t.Errorf("JSON edges = %d, YAML edges = %d", len(jsonGD.Edges), len(yamlGD.Edges))
	}
}

func TestDiagnosticError_SingleError(t *testing.T) {
	err := &DiagnosticError{
		Diagnostics: []graph.Diagnostic{
			{Code: "AT-001", Severity: "error", Message: "test error"},
		},
	}
	if err.Error() != "validation error: test error" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestDiagnosticError_MultipleErrors(t *testing.T) {
	err := &DiagnosticError{
		Diagnostics: []graph.Diagnostic{
			{Code: "AT-001", Severity: "error", Message: "first error"},
			{Code: "AT-002", Severity: "error", Message: "second error"},
		},
	}
	got := err.Error()
	if got != "2 validation errors (first: first error)" {
		t.Errorf("Error() = %q", got)
	}
}

func TestDiagnosticError_Unwrap(t *testing.T) {
	loadErr := &DiagnosticError{
		Diagnostics: []graph.Diagnostic{
			{Code: "AT-001", Severity: "error", Message: "test"},
		},
	}
	var diagErr *DiagnosticError
	if !errors.As(loadErr, &diagErr) {
		t.Error("should be unwrappable as *DiagnosticError")
	}
}
