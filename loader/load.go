package loader

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/petal-labs/petalflow/agent"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/registry"
)

// LoadWorkflow is the unified entry point that loads a workflow file,
// auto-detects its schema kind, and returns the compiled GraphDefinition.
func LoadWorkflow(path string) (*graph.GraphDefinition, SchemaKind, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path from caller
	if err != nil {
		return nil, "", fmt.Errorf("reading file %s: %w", path, err)
	}

	kind, err := DetectSchema(data, path)
	if err != nil {
		return nil, "", err
	}

	switch kind {
	case SchemaKindAgent:
		gd, err := loadAgentWorkflow(data, path)
		return gd, SchemaKindAgent, err
	case SchemaKindGraph:
		gd, err := loadGraphDefinition(data, path)
		return gd, SchemaKindGraph, err
	default:
		return nil, "", fmt.Errorf("unknown schema kind %q", kind)
	}
}

// LoadAgentWorkflow loads an Agent/Task workflow file, validates it,
// compiles it to a GraphDefinition, and returns the result.
func LoadAgentWorkflow(path string) (*graph.GraphDefinition, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path from caller
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", path, err)
	}
	return loadAgentWorkflow(data, path)
}

func loadAgentWorkflow(data []byte, path string) (*graph.GraphDefinition, error) {
	// Convert YAML to JSON if needed
	jsonData, err := toJSON(data, path)
	if err != nil {
		return nil, err
	}

	wf, err := agent.LoadFromBytes(jsonData)
	if err != nil {
		return nil, err
	}

	// Validate
	diags := agent.Validate(wf)
	if graph.HasErrors(diags) {
		return nil, &DiagnosticError{Diagnostics: diags}
	}

	// Compile
	gd, err := agent.Compile(wf)
	if err != nil {
		return nil, fmt.Errorf("compiling agent workflow: %w", err)
	}

	return gd, nil
}

// LoadGraphDefinition loads a Graph IR file, validates it, and returns
// the GraphDefinition.
func LoadGraphDefinition(path string) (*graph.GraphDefinition, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path from caller
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", path, err)
	}
	return loadGraphDefinition(data, path)
}

func loadGraphDefinition(data []byte, path string) (*graph.GraphDefinition, error) {
	jsonData, err := toJSON(data, path)
	if err != nil {
		return nil, err
	}

	var gd graph.GraphDefinition
	if err := json.Unmarshal(jsonData, &gd); err != nil {
		return nil, fmt.Errorf("parsing graph definition: %w", err)
	}

	// Validate
	diags := gd.ValidateWithRegistry(registry.Global())
	if graph.HasErrors(diags) {
		return nil, &DiagnosticError{Diagnostics: diags}
	}

	return &gd, nil
}

// toJSON converts data to JSON bytes, handling YAML conversion if the path
// indicates a YAML file.
func toJSON(data []byte, path string) ([]byte, error) {
	if isYAML(path) {
		return yamlToJSON(data)
	}
	return data, nil
}

// DiagnosticError wraps validation diagnostics as an error.
type DiagnosticError struct {
	Diagnostics []graph.Diagnostic
}

func (e *DiagnosticError) Error() string {
	errs := graph.Errors(e.Diagnostics)
	if len(errs) == 1 {
		return fmt.Sprintf("validation error: %s", errs[0].Message)
	}
	return fmt.Sprintf("%d validation errors (first: %s)", len(errs), errs[0].Message)
}
