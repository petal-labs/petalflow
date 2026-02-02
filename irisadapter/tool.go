package irisadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/petal-labs/iris/tools"
	"github.com/petal-labs/petalflow"
)

// ToolAdapter adapts an iris tools.Tool to the petalflow.PetalTool interface.
type ToolAdapter struct {
	tool tools.Tool
}

// NewToolAdapter creates a new adapter for the given tool.
func NewToolAdapter(tool tools.Tool) *ToolAdapter {
	return &ToolAdapter{tool: tool}
}

// Name returns the tool's name.
func (a *ToolAdapter) Name() string {
	return a.tool.Name()
}

// Invoke executes the tool with the given arguments.
func (a *ToolAdapter) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	// Convert args to JSON
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal args: %w", err)
	}

	// Call the underlying tool
	result, err := a.tool.Call(ctx, argsJSON)
	if err != nil {
		return nil, fmt.Errorf("tool call failed: %w", err)
	}

	// Convert result to map
	return toResultMap(result)
}

// toResultMap converts various result types to map[string]any.
func toResultMap(result any) (map[string]any, error) {
	if result == nil {
		return map[string]any{}, nil
	}

	// If already a map, return it
	if m, ok := result.(map[string]any); ok {
		return m, nil
	}

	// Try to convert via JSON
	data, err := json.Marshal(result)
	if err != nil {
		// If we can't marshal, wrap in a result key
		return map[string]any{"result": result}, nil
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		// If we can't unmarshal to map, wrap in a result key
		return map[string]any{"result": result}, nil
	}

	return m, nil
}

// Description returns the tool's description (for compatibility).
func (a *ToolAdapter) Description() string {
	return a.tool.Description()
}

// Schema returns the tool's schema (for compatibility).
func (a *ToolAdapter) Schema() tools.ToolSchema {
	return a.tool.Schema()
}

// Ensure interface compliance at compile time.
var _ petalflow.PetalTool = (*ToolAdapter)(nil)
