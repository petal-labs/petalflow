// Package loader provides schema detection and loading for PetalFlow workflow files.
// It supports both Agent/Task and Graph IR schemas in JSON and YAML formats.
package loader

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaKind identifies the type of workflow schema.
type SchemaKind string

const (
	SchemaKindAgent SchemaKind = "agent_workflow"
	SchemaKindGraph SchemaKind = "graph"
)

// DetectSchema auto-detects the schema kind from file content and path.
// It follows the detection algorithm from FRD §2.3:
//  1. Determine parse format from extension (.yaml/.yml -> YAML, else JSON)
//  2. If parsed.kind == "agent_workflow" -> AGENT_WORKFLOW
//  3. If has "nodes" AND "edges" AND NOT "agents" -> GRAPH_IR
//  4. If has "agents" AND "tasks" -> AGENT_WORKFLOW (fallback)
//  5. Else error
func DetectSchema(data []byte, filePath string) (SchemaKind, error) {
	// Step 1: Parse based on file extension
	var raw map[string]any
	if isYAML(filePath) {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return "", fmt.Errorf("parsing YAML: %w", err)
		}
	} else {
		if err := json.Unmarshal(data, &raw); err != nil {
			return "", fmt.Errorf("parsing JSON: %w", err)
		}
	}

	// Step 2: Detect schema kind from content
	if kind, ok := raw["kind"].(string); ok && kind == "agent_workflow" {
		return SchemaKindAgent, nil
	}

	hasNodes := hasKey(raw, "nodes")
	hasEdges := hasKey(raw, "edges")
	hasAgents := hasKey(raw, "agents")
	hasTasks := hasKey(raw, "tasks")

	if hasNodes && hasEdges && !hasAgents {
		return SchemaKindGraph, nil
	}

	if hasAgents && hasTasks {
		return SchemaKindAgent, nil
	}

	return "", fmt.Errorf("unable to detect schema format: file does not match agent_workflow or graph schema")
}

// isYAML returns true if the file path has a YAML extension.
func isYAML(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

// hasKey checks if a key exists in a map.
func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

// yamlToJSON converts raw bytes from YAML format to JSON bytes.
// This is the canonical YAML parsing strategy from the spec:
// YAML -> map[string]any -> JSON bytes -> typed struct.
func yamlToJSON(data []byte) ([]byte, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}
	// yaml.v3 uses map[string]any by default, which is JSON-compatible
	return json.Marshal(raw)
}
