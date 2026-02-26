// Package loader provides schema detection and loading for PetalFlow workflow files.
// It supports both Agent/Task and Graph IR schemas in JSON and YAML formats.
package loader

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/petal-labs/petalflow/schemafmt"
	"gopkg.in/yaml.v3"
)

// SchemaKind identifies the type of workflow schema.
type SchemaKind string

const (
	SchemaKindAgent SchemaKind = "agent_workflow"
	SchemaKindGraph SchemaKind = "graph"
)

// DetectSchema auto-detects the schema kind from file content and path.
// Detection order:
//  1. Determine parse format from extension (.yaml/.yml -> YAML, else JSON)
//  2. If parsed.kind is present, normalize/validate it and use it as source of truth
//  3. If parsed.schema_version is present, validate semver and supported major
//  4. Otherwise use legacy shape fallback (agents/tasks vs nodes/edges)
//  5. Else return an unrecognized schema error
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

	// Step 2: Prefer explicit kind/schema_version routing when present.
	kind, hasKind, err := readStringField(raw, "kind")
	if err != nil {
		return "", err
	}
	schemaVersion, hasSchemaVersion, err := readStringField(raw, "schema_version")
	if err != nil {
		return "", err
	}

	if hasKind {
		normalized, _, err := schemafmt.NormalizeKind(kind)
		if err != nil {
			return "", fmt.Errorf("invalid kind: %w", err)
		}

		schemaKind := toSchemaKind(normalized)
		if hasSchemaVersion {
			if err := validateSchemaVersionForKind(schemaVersion, schemaKind); err != nil {
				return "", err
			}
		}

		return schemaKind, nil
	}

	// Step 3: Legacy shape-based fallback.
	hasNodes := hasKey(raw, "nodes")
	hasEdges := hasKey(raw, "edges")
	hasAgents := hasKey(raw, "agents")
	hasTasks := hasKey(raw, "tasks")

	if hasNodes && hasEdges && !hasAgents {
		if hasSchemaVersion {
			if err := validateSchemaVersionForKind(schemaVersion, SchemaKindGraph); err != nil {
				return "", err
			}
		}
		return SchemaKindGraph, nil
	}

	if hasAgents && hasTasks {
		if hasSchemaVersion {
			if err := validateSchemaVersionForKind(schemaVersion, SchemaKindAgent); err != nil {
				return "", err
			}
		}
		return SchemaKindAgent, nil
	}

	return "", fmt.Errorf("unable to detect schema format: file does not match agent_workflow or graph schema")
}

func readStringField(raw map[string]any, field string) (string, bool, error) {
	value, ok := raw[field]
	if !ok {
		return "", false, nil
	}

	str, ok := value.(string)
	if !ok {
		return "", true, fmt.Errorf("%s must be a string", field)
	}
	return str, true, nil
}

func toSchemaKind(kind schemafmt.WorkflowKind) SchemaKind {
	switch kind {
	case schemafmt.KindAgent:
		return SchemaKindAgent
	case schemafmt.KindGraph:
		return SchemaKindGraph
	default:
		return ""
	}
}

func validateSchemaVersionForKind(schemaVersion string, kind SchemaKind) error {
	switch kind {
	case SchemaKindAgent:
		if err := schemafmt.ValidateSchemaVersion(schemaVersion, schemafmt.SupportedAgentSchemaMajor); err != nil {
			return fmt.Errorf("invalid schema_version: %w", err)
		}
	case SchemaKindGraph:
		if err := schemafmt.ValidateSchemaVersion(schemaVersion, schemafmt.SupportedGraphSchemaMajor); err != nil {
			return fmt.Errorf("invalid schema_version: %w", err)
		}
	}

	return nil
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
