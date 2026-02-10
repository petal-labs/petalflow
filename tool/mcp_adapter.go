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
	reg  Registration
	pool *mcpClientPool
}

// NewMCPAdapter creates an MCP adapter from a registration.
func NewMCPAdapter(ctx context.Context, reg Registration) (*MCPAdapter, error) {
	overlay, _ := loadOverlayForRegistration(reg)
	transport, ok := reg.Manifest.Transport.AsMCP()
	if !ok {
		return nil, fmt.Errorf("tool: registration %q is not an mcp transport", reg.Name)
	}

	overlayPath := ""
	if reg.Overlay != nil {
		overlayPath = reg.Overlay.Path
	}
	poolKey, err := mcpPoolKey(reg.Name, transport, reg.Config, overlayPath)
	if err != nil {
		return nil, err
	}
	pool, err := sharedMCPClientPools.getOrCreate(ctx, poolKey, func(createCtx context.Context) (*mcpClientPool, error) {
		return newMCPClientPool(createCtx, configuredMCPPoolSize(), func(clientCtx context.Context) (*mcpclient.Client, func(), error) {
			return newMCPClientFromConfig(clientCtx, reg.Name, transport, reg.Config, overlay)
		})
	})
	if err != nil {
		return nil, err
	}

	return &MCPAdapter{
		reg:  reg,
		pool: pool,
	}, nil
}

// Invoke executes an action through MCP tools/call.
func (a *MCPAdapter) Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error) {
	if a == nil || a.pool == nil {
		return InvokeResponse{}, newToolError(ToolErrorCodeInvalidRequest, "tool: mcp adapter has no client pool", false, errors.New("mcp pool is nil"))
	}
	actionName := strings.TrimSpace(req.Action)
	if actionName == "" {
		return InvokeResponse{}, newToolError(
			ToolErrorCodeActionNotFound,
			"tool: action is required",
			false,
			fmt.Errorf("%w: empty action", ErrActionNotFound),
		)
	}

	action, ok := a.reg.Manifest.Actions[actionName]
	if !ok {
		return InvokeResponse{}, newToolError(
			ToolErrorCodeActionNotFound,
			fmt.Sprintf("tool: action %q not found", actionName),
			false,
			fmt.Errorf("%w: %s", ErrActionNotFound, actionName),
		)
	}
	targetTool := strings.TrimSpace(action.MCPToolName)
	if targetTool == "" {
		targetTool = actionName
	}

	totalStart := time.Now()
	response, attempts, err := invokeWithRetry(ctx, a.reg.Manifest.Transport.Retry, retryObservationMeta{
		toolName:  req.ToolName,
		action:    actionName,
		transport: a.reg.Manifest.Transport.Type,
	}, func(attemptCtx context.Context, attempt int) (InvokeResponse, error) {
		start := time.Now()
		result, err := a.pool.CallTool(attemptCtx, mcpclient.ToolsCallParams{
			Name:      targetTool,
			Arguments: req.Inputs,
		})
		if err != nil {
			return InvokeResponse{}, classifyMCPInvokeError(err)
		}

		outputs, err := ParseMCPCallResult(result, action)
		if err != nil {
			return InvokeResponse{}, newToolError(ToolErrorCodeDecodeFailure, "tool: mcp decode call result", false, err)
		}
		if result.IsError {
			return InvokeResponse{}, withToolErrorDetails(
				newToolError(
					ToolErrorCodeMCPFailure,
					fmt.Sprintf("tool: mcp call reported error for action %q", actionName),
					false,
					nil,
				),
				map[string]any{
					"mcp_tool_name": targetTool,
					"outputs":       outputs,
				},
			)
		}

		return InvokeResponse{
			Outputs:    outputs,
			DurationMS: elapsedMS(start),
			Metadata: map[string]any{
				"mcp_tool_name": targetTool,
			},
		}, nil
	})
	if err != nil {
		emitInvokeObservation(ToolInvokeObservation{
			ToolName:   req.ToolName,
			Action:     actionName,
			Transport:  a.reg.Manifest.Transport.Type,
			Attempts:   attempts,
			DurationMS: elapsedMS(totalStart),
			Success:    false,
			ErrorCode:  toolErrorCode(err),
		})
		return InvokeResponse{}, withToolErrorDetails(
			newToolError(
				toolErrorCodeOrDefault(err, ToolErrorCodeInvocationFailed),
				"tool: mcp invoke failed",
				isRetryableError(err),
				err,
			),
			map[string]any{
				"action":        actionName,
				"mcp_tool_name": targetTool,
				"attempts":      attempts,
				"duration_ms":   elapsedMS(totalStart),
			},
		)
	}

	if response.Metadata == nil {
		response.Metadata = map[string]any{}
	}
	response.Metadata["attempts"] = attempts
	response.Metadata["retry_count"] = attempts - 1
	response.Metadata["total_duration_ms"] = elapsedMS(totalStart)
	emitInvokeObservation(ToolInvokeObservation{
		ToolName:   req.ToolName,
		Action:     actionName,
		Transport:  a.reg.Manifest.Transport.Type,
		Attempts:   attempts,
		DurationMS: elapsedMS(totalStart),
		Success:    true,
	})
	return response, nil
}

// Close closes the MCP connection.
func (a *MCPAdapter) Close(ctx context.Context) error {
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

func classifyMCPInvokeError(err error) error {
	if err == nil {
		return nil
	}

	var rpcErr *mcpclient.RPCError
	if errors.As(err, &rpcErr) {
		return withToolErrorDetails(
			newToolError(
				ToolErrorCodeMCPFailure,
				rpcErr.Error(),
				false,
				err,
			),
			map[string]any{
				"mcp_code": rpcErr.Code,
			},
		)
	}

	var reqErr *mcpclient.RequestError
	if errors.As(err, &reqErr) {
		retryable := true
		if errors.Is(err, context.Canceled) {
			retryable = false
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return newToolError(ToolErrorCodeTimeout, "tool: mcp request timed out", true, err)
		}
		return newToolError(ToolErrorCodeTransportFailure, reqErr.Error(), retryable, err)
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return newToolError(ToolErrorCodeTimeout, "tool: mcp request timed out", true, err)
	}

	return newToolError(ToolErrorCodeMCPFailure, "tool: mcp invoke failed", false, err)
}
