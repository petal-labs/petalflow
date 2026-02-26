package loader

import (
	"encoding/json"
	"testing"
)

func TestDetectSchema_AgentJSON_KindField(t *testing.T) {
	data := []byte(`{"kind": "agent_workflow", "agents": {}, "tasks": {}}`)
	kind, err := DetectSchema(data, "workflow.json")
	if err != nil {
		t.Fatalf("DetectSchema() error = %v", err)
	}
	if kind != SchemaKindAgent {
		t.Errorf("kind = %q, want %q", kind, SchemaKindAgent)
	}
}

func TestDetectSchema_AgentJSON_Fallback(t *testing.T) {
	data := []byte(`{"agents": {"a": {}}, "tasks": {"t": {}}}`)
	kind, err := DetectSchema(data, "workflow.json")
	if err != nil {
		t.Fatalf("DetectSchema() error = %v", err)
	}
	if kind != SchemaKindAgent {
		t.Errorf("kind = %q, want %q", kind, SchemaKindAgent)
	}
}

func TestDetectSchema_GraphJSON(t *testing.T) {
	data := []byte(`{"nodes": [], "edges": []}`)
	kind, err := DetectSchema(data, "workflow.json")
	if err != nil {
		t.Fatalf("DetectSchema() error = %v", err)
	}
	if kind != SchemaKindGraph {
		t.Errorf("kind = %q, want %q", kind, SchemaKindGraph)
	}
}

func TestDetectSchema_NodesEdgesWithAgents_IsAgent(t *testing.T) {
	// Has nodes, edges, AND agents -> agents takes priority per §2.3
	data := []byte(`{"nodes": [], "edges": [], "agents": {}, "tasks": {}}`)
	kind, err := DetectSchema(data, "workflow.json")
	if err != nil {
		t.Fatalf("DetectSchema() error = %v", err)
	}
	if kind != SchemaKindAgent {
		t.Errorf("kind = %q, want %q", kind, SchemaKindAgent)
	}
}

func TestDetectSchema_YAML(t *testing.T) {
	data := []byte("kind: agent_workflow\nagents: {}\ntasks: {}\n")
	kind, err := DetectSchema(data, "workflow.yaml")
	if err != nil {
		t.Fatalf("DetectSchema() error = %v", err)
	}
	if kind != SchemaKindAgent {
		t.Errorf("kind = %q, want %q", kind, SchemaKindAgent)
	}
}

func TestDetectSchema_YML_Extension(t *testing.T) {
	data := []byte("nodes:\n  - id: a\nedges: []\n")
	kind, err := DetectSchema(data, "workflow.yml")
	if err != nil {
		t.Fatalf("DetectSchema() error = %v", err)
	}
	if kind != SchemaKindGraph {
		t.Errorf("kind = %q, want %q", kind, SchemaKindGraph)
	}
}

func TestDetectSchema_InvalidContent(t *testing.T) {
	data := []byte(`{"foo": "bar"}`)
	_, err := DetectSchema(data, "workflow.json")
	if err == nil {
		t.Fatal("expected error for unrecognizable content")
	}
}

func TestDetectSchema_InvalidJSON(t *testing.T) {
	data := []byte(`{invalid`)
	_, err := DetectSchema(data, "workflow.json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestDetectSchema_InvalidYAML(t *testing.T) {
	data := []byte("\t\tinvalid yaml content\n\t- broken")
	_, err := DetectSchema(data, "workflow.yaml")
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestDetectSchema_AgentJSON_LegacyKindAlias(t *testing.T) {
	data := []byte(`{"kind": "agent-workflow", "agents": {}, "tasks": {}}`)
	kind, err := DetectSchema(data, "workflow.json")
	if err != nil {
		t.Fatalf("DetectSchema() error = %v", err)
	}
	if kind != SchemaKindAgent {
		t.Errorf("kind = %q, want %q", kind, SchemaKindAgent)
	}
}

func TestDetectSchema_InvalidKind(t *testing.T) {
	data := []byte(`{"kind": "workflow", "agents": {}, "tasks": {}}`)
	_, err := DetectSchema(data, "workflow.json")
	if err == nil {
		t.Fatal("expected error for invalid kind")
	}
}

func TestDetectSchema_InvalidSchemaVersion(t *testing.T) {
	data := []byte(`{"kind": "agent_workflow", "schema_version": "1.0", "agents": {}, "tasks": {}}`)
	_, err := DetectSchema(data, "workflow.json")
	if err == nil {
		t.Fatal("expected error for invalid schema_version")
	}
}

func TestDetectSchema_UnsupportedSchemaMajor(t *testing.T) {
	data := []byte(`{"kind": "graph", "schema_version": "2.0.0", "nodes": [], "edges": []}`)
	_, err := DetectSchema(data, "workflow.json")
	if err == nil {
		t.Fatal("expected error for unsupported schema_version major")
	}
}

func TestDetectSchema_ValidSchemaVersion_JSON(t *testing.T) {
	data := []byte(`{"kind":"agent_workflow","schema_version":"1.2.3","agents":{},"tasks":{}}`)
	kind, err := DetectSchema(data, "workflow.json")
	if err != nil {
		t.Fatalf("DetectSchema() error = %v", err)
	}
	if kind != SchemaKindAgent {
		t.Errorf("kind = %q, want %q", kind, SchemaKindAgent)
	}
}

func TestDetectSchema_ValidSchemaVersion_YAML(t *testing.T) {
	data := []byte("kind: graph\nschema_version: 1.0.0\nnodes: []\nedges: []\n")
	kind, err := DetectSchema(data, "workflow.yaml")
	if err != nil {
		t.Fatalf("DetectSchema() error = %v", err)
	}
	if kind != SchemaKindGraph {
		t.Errorf("kind = %q, want %q", kind, SchemaKindGraph)
	}
}

func TestIsYAML(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"file.yaml", true},
		{"file.yml", true},
		{"file.YAML", true},
		{"file.json", false},
		{"file.txt", false},
		{"file.agent.yaml", true},
		{"file.graph.json", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isYAML(tt.path)
			if got != tt.want {
				t.Errorf("isYAML(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestYamlToJSON(t *testing.T) {
	yamlData := []byte("name: test\ncount: 42\n")
	jsonData, err := yamlToJSON(yamlData)
	if err != nil {
		t.Fatalf("yamlToJSON() error = %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(jsonData, &m); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if m["name"] != "test" {
		t.Errorf("name = %v, want %q", m["name"], "test")
	}
}
