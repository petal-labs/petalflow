package nodes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	toolpolicy "github.com/petal-labs/petalflow/tool"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

// ToolNodeConfig configures a ToolNode.
type ToolNodeConfig struct {
	// ToolName is the name of the tool to execute.
	ToolName string

	// ArgsTemplate maps argument names to envelope variable paths.
	// Example: {"location": "user_location", "units": "preferences.units"}
	ArgsTemplate map[string]string

	// RequiredArgs lists argument names that must resolve to a value. If any is
	// missing after building args, the node returns an error instead of invoking
	// the tool with incomplete arguments.
	RequiredArgs []string

	// StaticArgs are arguments that don't come from the envelope.
	StaticArgs map[string]any

	// OutputKey is the envelope variable name to store the tool output.
	OutputKey string

	// Timeout is the maximum time to wait for tool execution.
	Timeout time.Duration

	// RetryPolicy configures retry behavior for transient failures.
	RetryPolicy core.RetryPolicy
}

// ToolNode executes a tool as a workflow step.
type ToolNode struct {
	core.BaseNode
	config   ToolNodeConfig
	tool     core.PetalTool
	registry *core.ToolRegistry
}

// NewToolNode creates a new tool node with a specific tool.
func NewToolNode(id string, tool core.PetalTool, config ToolNodeConfig) *ToolNode {
	// Apply defaults
	if config.OutputKey == "" {
		config.OutputKey = id + "_output"
	}
	if config.RetryPolicy.MaxAttempts == 0 {
		config.RetryPolicy = core.DefaultRetryPolicy()
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.ToolName == "" && tool != nil {
		config.ToolName = tool.Name()
	}

	return &ToolNode{
		BaseNode: core.NewBaseNode(id, core.NodeKindTool),
		config:   config,
		tool:     tool,
	}
}

// NewToolNodeWithRegistry creates a tool node that looks up tools by name.
func NewToolNodeWithRegistry(id string, registry *core.ToolRegistry, config ToolNodeConfig) *ToolNode {
	// Apply defaults
	if config.OutputKey == "" {
		config.OutputKey = id + "_output"
	}
	if config.RetryPolicy.MaxAttempts == 0 {
		config.RetryPolicy = core.DefaultRetryPolicy()
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	return &ToolNode{
		BaseNode: core.NewBaseNode(id, core.NodeKindTool),
		config:   config,
		registry: registry,
	}
}

// Run executes the tool and stores the result in the envelope.
func (n *ToolNode) Run(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	// Apply timeout
	if n.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, n.config.Timeout)
		defer cancel()
	}

	// Get the tool
	tool, err := n.getTool()
	if err != nil {
		return nil, err
	}

	// Get event emitter from context
	emit := runtime.EmitterFromContext(ctx)

	// Build arguments from envelope
	args, err := n.buildArgs(env)
	if err != nil {
		return nil, fmt.Errorf("failed to build args: %w", err)
	}

	// Emit tool.call event
	emit(runtime.NewEvent(runtime.EventToolCall, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("tool_name", tool.Name()).
		WithPayload("arguments", args))

	// Policy gate: fails with a typed error before any side effect when
	// the caller's effective policy denies the tool. Rejections are
	// never retried. Without an installed evaluator, enforcement was
	// not opted into and invocation proceeds.
	gateName := tool.Name()
	baseName, _ := toolpolicy.ReferenceNames(gateName)
	if gateErr := toolpolicy.CheckPolicy(ctx, gateName); gateErr != nil {
		// Durable approval flow: an approval requirement pauses the run
		// for an operator decision when the runtime provides a bridge;
		// without one the requirement stays a hard block.
		if toolpolicy.IsApprovalRequired(gateErr) {
			approved, approvalID, decidedBy, approvalErr := n.requestToolApproval(ctx, emit, env, gateName, gateErr)
			if approvalErr != nil {
				var pendingErr *ToolApprovalPendingError
				if errors.As(approvalErr, &pendingErr) {
					return nil, approvalErr
				}
				return nil, approvalErr
			}
			if approved {
				// Replayed approval authorizes exactly this invocation.
				ctx = toolpolicy.ContextWithApprovalGrant(ctx, toolpolicy.ApprovalGrant{
					ToolName:   baseName,
					ApprovalID: approvalID,
					DecidedBy:  decidedBy,
				})
			}
		} else {
			emit(runtime.NewEvent(runtime.EventToolResult, env.Trace.RunID).
				WithNode(n.ID(), n.Kind()).
				WithPayload("tool_name", gateName).
				WithPayload("is_error", true).
				WithPayload("policy_rejected", true).
				WithPayload("error_code", toolpolicy.PolicyErrorCode(gateErr)))
			return nil, fmt.Errorf("tool %q rejected before invocation: %w", gateName, gateErr)
		}
	}

	// Execute with retries
	var result map[string]any
	var lastErr error

	for attempt := 1; attempt <= n.config.RetryPolicy.MaxAttempts; attempt++ {
		result, lastErr = tool.Invoke(ctx, args)
		if lastErr == nil {
			break
		}

		// Check if context is done
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Wait before retry (except on last attempt)
		if attempt < n.config.RetryPolicy.MaxAttempts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(n.config.RetryPolicy.Backoff * time.Duration(attempt)):
			}
		}
	}

	// Emit tool.result event (always, even on failure)
	emit(runtime.NewEvent(runtime.EventToolResult, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("tool_name", tool.Name()).
		WithPayload("is_error", lastErr != nil))

	if lastErr != nil {
		return nil, fmt.Errorf("tool %q failed after %d attempts: %w",
			n.config.ToolName, n.config.RetryPolicy.MaxAttempts, lastErr)
	}

	// Store output in envelope
	env.SetVar(n.config.OutputKey, result)

	return env, nil
}

// requestToolApproval runs the durable approval flow for an
// approval-gated tool. Behavior by bridge outcome:
//   - no bridge → hard block with the typed policy error,
//   - pending → approval.requested event and a run pause,
//   - approved → proceed to invocation,
//   - rejected/expired → deterministic typed failure.
func (n *ToolNode) requestToolApproval(
	ctx context.Context,
	emit runtime.EventEmitter,
	env *core.Envelope,
	toolName string,
	gateErr error,
) (bool, string, string, error) {
	handler := runtime.ToolApprovalHandlerFromContext(ctx)
	if handler == nil {
		emit(runtime.NewEvent(runtime.EventToolResult, env.Trace.RunID).
			WithNode(n.ID(), n.Kind()).
			WithPayload("tool_name", toolName).
			WithPayload("is_error", true).
			WithPayload("policy_rejected", true).
			WithPayload("error_code", toolpolicy.ToolErrorCodePolicyApprovalRequired))
		return false, "", "", fmt.Errorf("tool %q approval required but no approval flow is configured: %w", toolName, gateErr)
	}

	base, action := toolpolicy.ReferenceNames(toolName)
	if action != "" {
		action = strings.TrimPrefix(action, base+".")
	}
	req := &ToolApprovalRequest{
		ID:           uuid.New().String(),
		NodeID:       n.ID(),
		RunID:        env.Trace.RunID,
		ToolName:     base,
		Action:       action,
		Reference:    toolName,
		PolicySource: ToolApprovalSourcePolicy,
		CreatedAt:    time.Now(),
	}

	emit(runtime.NewEvent(runtime.EventApprovalRequested, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("approval_id", req.ID).
		WithPayload("tool_name", req.ToolName).
		WithPayload("action", req.Action).
		WithPayload("policy_source", req.PolicySource).
		WithPayload("expires_at", req.ExpiresAt))

	value, handlerErr := handler(ctx, req)
	if handlerErr != nil {
		// Pending requests pause the run instead of failing it.
		var pendingErr *ToolApprovalPendingError
		if errors.As(handlerErr, &pendingErr) {
			return false, "", "", handlerErr
		}
		return false, "", "", fmt.Errorf("tool %q approval flow failed: %w", toolName, handlerErr)
	}

	var response *ToolApprovalResponse
	switch typed := value.(type) {
	case *ToolApprovalResponse:
		response = typed
	case ToolApprovalResponse:
		response = &typed
	default:
		return false, "", "", fmt.Errorf("tool %q approval bridge returned %T, want *ToolApprovalResponse", toolName, value)
	}
	if response == nil {
		return false, "", "", fmt.Errorf("tool %q approval bridge returned nil decision", toolName)
	}

	emit(runtime.NewEvent(runtime.EventApprovalDecided, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("approval_id", req.ID).
		WithPayload("tool_name", req.ToolName).
		WithPayload("decision", response.Decision).
		WithPayload("decided_by", response.DecisionBy).
		WithPayload("note", response.Note))

	if !response.Approved() {
		decisionErr := ErrToolApprovalRejected
		if response.Decision != ToolApprovalDecisionRejected && response.Decision != "" {
			// Unrecognized explicit decisions fail closed as well.
			decisionErr = ErrToolApprovalExpired
		}
		emit(runtime.NewEvent(runtime.EventToolResult, env.Trace.RunID).
			WithNode(n.ID(), n.Kind()).
			WithPayload("tool_name", toolName).
			WithPayload("is_error", true).
			WithPayload("policy_rejected", true).
			WithPayload("error_code", toolpolicy.ToolErrorCodePolicyApprovalRequired).
			WithPayload("approval_decision", response.Decision))
		return false, "", "", fmt.Errorf("tool %q invocation blocked: approval %w", toolName, decisionErr)
	}
	return true, req.ID, response.DecisionBy, nil
}

// getTool retrieves the tool to execute.
func (n *ToolNode) getTool() (core.PetalTool, error) {
	// Direct tool takes precedence
	if n.tool != nil {
		return n.tool, nil
	}

	// Look up in registry
	if n.registry != nil {
		tool, ok := n.registry.Get(n.config.ToolName)
		if !ok {
			return nil, fmt.Errorf("tool %q not found in registry", n.config.ToolName)
		}
		return tool, nil
	}

	return nil, fmt.Errorf("no tool or registry configured")
}

// buildArgs constructs tool arguments from the envelope.
func (n *ToolNode) buildArgs(env *core.Envelope) (map[string]any, error) {
	args := make(map[string]any)

	// Add static args first
	for k, v := range n.config.StaticArgs {
		args[k] = v
	}

	// Add args from envelope variables
	for argName, varPath := range n.config.ArgsTemplate {
		// Support nested paths with dot notation
		val, ok := env.GetVarNested(varPath)
		if !ok {
			// Try simple var lookup
			val, ok = env.GetVar(varPath)
		}
		if ok {
			args[argName] = val
		}
	}

	// Fail rather than invoke the tool with missing required arguments.
	var missing []string
	for _, name := range n.config.RequiredArgs {
		if _, ok := args[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("missing required tool args: %s", strings.Join(missing, ", "))
	}

	return args, nil
}

// Config returns the node's configuration.
func (n *ToolNode) Config() ToolNodeConfig {
	return n.config
}

// Ensure interface compliance at compile time.
var _ core.Node = (*ToolNode)(nil)
