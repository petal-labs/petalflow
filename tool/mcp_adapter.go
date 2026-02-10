package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mcpclient "github.com/petal-labs/petalflow/tool/mcp"
)

// MCPAdapter is the runtime adapter for MCP-backed tools.
type MCPAdapter struct {
	reg     Registration
	client  *mcpclient.Client
	closeFn func()
}

// NewMCPAdapter creates an MCP adapter from a registration.
func NewMCPAdapter(ctx context.Context, reg Registration) (*MCPAdapter, error) {
	overlay, _ := loadOverlayForRegistration(reg)
	transport, ok := reg.Manifest.Transport.AsMCP()
	if !ok {
		return nil, fmt.Errorf("tool: registration %q is not an mcp transport", reg.Name)
	}

	client, cleanup, err := newMCPClientFromConfig(ctx, reg.Name, transport, reg.Config, overlay)
	if err != nil {
		return nil, err
	}
	if _, err := client.Initialize(ctx); err != nil {
		cleanup()
		return nil, fmt.Errorf("tool: mcp adapter initialize failed: %w", err)
	}

	return &MCPAdapter{
		reg:     reg,
		client:  client,
		closeFn: cleanup,
	}, nil
}

// Invoke executes an action through MCP tools/call.
func (a *MCPAdapter) Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error) {
	if a == nil || a.client == nil {
		return InvokeResponse{}, errors.New("tool: mcp adapter has no client")
	}
	actionName := strings.TrimSpace(req.Action)
	if actionName == "" {
		return InvokeResponse{}, fmt.Errorf("%w: empty action", ErrActionNotFound)
	}

	action, ok := a.reg.Manifest.Actions[actionName]
	if !ok {
		return InvokeResponse{}, fmt.Errorf("%w: %s", ErrActionNotFound, actionName)
	}
	targetTool := strings.TrimSpace(action.MCPToolName)
	if targetTool == "" {
		targetTool = actionName
	}

	start := time.Now()
	result, err := a.client.CallTool(ctx, mcpclient.ToolsCallParams{
		Name:      targetTool,
		Arguments: req.Inputs,
	})
	if err != nil {
		return InvokeResponse{}, err
	}

	outputs, err := ParseMCPCallResult(result, action)
	if err != nil {
		return InvokeResponse{}, err
	}
	if result.IsError {
		return InvokeResponse{}, fmt.Errorf("tool: mcp call reported error for action %q", actionName)
	}

	return InvokeResponse{
		Outputs:    outputs,
		DurationMS: elapsedMS(start),
		Metadata: map[string]any{
			"mcp_tool_name": targetTool,
		},
	}, nil
}

// Close closes the MCP connection.
func (a *MCPAdapter) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if a.closeFn != nil {
		a.closeFn()
	}
	return nil
}

func loadOverlayForRegistration(reg Registration) (*MCPOverlay, error) {
	if reg.Overlay == nil || strings.TrimSpace(reg.Overlay.Path) == "" {
		return nil, nil
	}
	overlay, _, err := ParseMCPOverlayFile(reg.Overlay.Path)
	if err != nil {
		return nil, err
	}
	return &overlay, nil
}
