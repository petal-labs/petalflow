package petalflow

import (
	"context"
	"fmt"
	"time"
)

// ToolNodeConfig configures a ToolNode.
type ToolNodeConfig struct {
	// ToolName is the name of the tool to execute.
	ToolName string

	// ArgsTemplate maps argument names to envelope variable paths.
	// Example: {"location": "user_location", "units": "preferences.units"}
	ArgsTemplate map[string]string

	// StaticArgs are arguments that don't come from the envelope.
	StaticArgs map[string]any

	// OutputKey is the envelope variable name to store the tool output.
	OutputKey string

	// Timeout is the maximum time to wait for tool execution.
	Timeout time.Duration

	// RetryPolicy configures retry behavior for transient failures.
	RetryPolicy RetryPolicy

	// OnError defines how errors are handled.
	OnError ErrorPolicy
}

// ToolNode executes a tool as a workflow step.
type ToolNode struct {
	BaseNode
	config   ToolNodeConfig
	tool     PetalTool
	registry *ToolRegistry
}

// NewToolNode creates a new tool node with a specific tool.
func NewToolNode(id string, tool PetalTool, config ToolNodeConfig) *ToolNode {
	// Apply defaults
	if config.OutputKey == "" {
		config.OutputKey = id + "_output"
	}
	if config.RetryPolicy.MaxAttempts == 0 {
		config.RetryPolicy = DefaultRetryPolicy()
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.OnError == "" {
		config.OnError = ErrorPolicyFail
	}
	if config.ToolName == "" && tool != nil {
		config.ToolName = tool.Name()
	}

	return &ToolNode{
		BaseNode: NewBaseNode(id, NodeKindTool),
		config:   config,
		tool:     tool,
	}
}

// NewToolNodeWithRegistry creates a tool node that looks up tools by name.
func NewToolNodeWithRegistry(id string, registry *ToolRegistry, config ToolNodeConfig) *ToolNode {
	// Apply defaults
	if config.OutputKey == "" {
		config.OutputKey = id + "_output"
	}
	if config.RetryPolicy.MaxAttempts == 0 {
		config.RetryPolicy = DefaultRetryPolicy()
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.OnError == "" {
		config.OnError = ErrorPolicyFail
	}

	return &ToolNode{
		BaseNode: NewBaseNode(id, NodeKindTool),
		config:   config,
		registry: registry,
	}
}

// Run executes the tool and stores the result in the envelope.
func (n *ToolNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	// Apply timeout
	if n.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, n.config.Timeout)
		defer cancel()
	}

	// Get the tool
	tool, err := n.getTool()
	if err != nil {
		return n.handleError(env, err)
	}

	// Build arguments from envelope
	args, err := n.buildArgs(env)
	if err != nil {
		return n.handleError(env, fmt.Errorf("failed to build args: %w", err))
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
			return n.handleError(env, ctx.Err())
		}

		// Wait before retry (except on last attempt)
		if attempt < n.config.RetryPolicy.MaxAttempts {
			select {
			case <-ctx.Done():
				return n.handleError(env, ctx.Err())
			case <-time.After(n.config.RetryPolicy.Backoff * time.Duration(attempt)):
			}
		}
	}

	if lastErr != nil {
		return n.handleError(env, fmt.Errorf("tool %q failed after %d attempts: %w",
			n.config.ToolName, n.config.RetryPolicy.MaxAttempts, lastErr))
	}

	// Store output in envelope
	env.SetVar(n.config.OutputKey, result)

	return env, nil
}

// getTool retrieves the tool to execute.
func (n *ToolNode) getTool() (PetalTool, error) {
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
func (n *ToolNode) buildArgs(env *Envelope) (map[string]any, error) {
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

	return args, nil
}

// handleError processes errors according to the OnError policy.
func (n *ToolNode) handleError(env *Envelope, err error) (*Envelope, error) {
	switch n.config.OnError {
	case ErrorPolicyContinue, ErrorPolicyRecord:
		// Record error and continue
		env.AppendError(NodeError{
			NodeID:  n.id,
			Kind:    NodeKindTool,
			Message: err.Error(),
			At:      time.Now(),
			Cause:   err,
			Details: map[string]any{
				"tool": n.config.ToolName,
			},
		})
		// Set output to nil to indicate failure
		env.SetVar(n.config.OutputKey, nil)
		env.SetVar(n.config.OutputKey+"_error", err.Error())
		return env, nil

	default: // ErrorPolicyFail
		return nil, err
	}
}

// Config returns the node's configuration.
func (n *ToolNode) Config() ToolNodeConfig {
	return n.config
}

// Ensure interface compliance at compile time.
var _ Node = (*ToolNode)(nil)
