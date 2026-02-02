package petalflow

import (
	"context"
	"fmt"
)

// GateAction defines what happens when a gate condition fails.
type GateAction string

const (
	// GateActionBlock stops execution with an error.
	GateActionBlock GateAction = "block"

	// GateActionSkip passes through without modification (noop behavior).
	GateActionSkip GateAction = "skip"

	// GateActionRedirect routes to a specific node on failure.
	GateActionRedirect GateAction = "redirect"
)

// GateNodeConfig configures a GateNode.
type GateNodeConfig struct {
	// Condition evaluates whether the gate passes.
	// Receives the envelope and returns (pass, error).
	// If error is non-nil, the gate fails with that error.
	Condition func(ctx context.Context, env *Envelope) (bool, error)

	// ConditionVar is an alternative to Condition.
	// Specifies a variable name in the envelope that should be truthy.
	// If both Condition and ConditionVar are set, Condition takes precedence.
	ConditionVar string

	// OnFail determines the behavior when the gate condition fails.
	// Defaults to GateActionBlock.
	OnFail GateAction

	// FailMessage is the error message when OnFail is GateActionBlock.
	// Defaults to "gate condition failed".
	FailMessage string

	// RedirectNodeID is the node to route to when OnFail is GateActionRedirect.
	// The runtime must handle this by checking the envelope's route hint.
	RedirectNodeID string

	// ResultVar is the variable name to store the gate result (true/false).
	// If empty, no result is stored.
	ResultVar string
}

// GateNode evaluates a condition and either passes execution through or takes action on failure.
// It's useful for guard clauses, validation, feature flags, and permission checks.
type GateNode struct {
	BaseNode
	config GateNodeConfig
}

// NewGateNode creates a new GateNode with the given configuration.
func NewGateNode(id string, config GateNodeConfig) *GateNode {
	// Set defaults
	if config.OnFail == "" {
		config.OnFail = GateActionBlock
	}
	if config.FailMessage == "" {
		config.FailMessage = "gate condition failed"
	}

	return &GateNode{
		BaseNode: NewBaseNode(id, NodeKindGate),
		config:   config,
	}
}

// Config returns the node's configuration.
func (n *GateNode) Config() GateNodeConfig {
	return n.config
}

// GateResult is stored in the envelope when ResultVar is set.
type GateResult struct {
	Passed bool   `json:"passed"`
	Action string `json:"action,omitempty"`
}

// Run evaluates the gate condition and takes appropriate action.
func (n *GateNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	// Evaluate condition
	passed, err := n.evaluateCondition(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("gate node %s: condition error: %w", n.id, err)
	}

	// Clone envelope for result
	result := env.Clone()

	// Store result if configured
	if n.config.ResultVar != "" {
		result.SetVar(n.config.ResultVar, GateResult{
			Passed: passed,
			Action: string(n.config.OnFail),
		})
	}

	// Handle pass case
	if passed {
		return result, nil
	}

	// Handle failure based on configured action
	switch n.config.OnFail {
	case GateActionBlock:
		return nil, fmt.Errorf("gate node %s: %s", n.id, n.config.FailMessage)

	case GateActionSkip:
		// Pass through without modification
		return result, nil

	case GateActionRedirect:
		// Set a route hint for the runtime
		if n.config.RedirectNodeID == "" {
			return nil, fmt.Errorf("gate node %s: redirect action requires RedirectNodeID", n.id)
		}
		result.SetVar("__gate_redirect__", n.config.RedirectNodeID)
		return result, nil

	default:
		return nil, fmt.Errorf("gate node %s: unknown action %q", n.id, n.config.OnFail)
	}
}

// evaluateCondition checks the gate condition.
func (n *GateNode) evaluateCondition(ctx context.Context, env *Envelope) (bool, error) {
	// Use Condition function if provided
	if n.config.Condition != nil {
		return n.config.Condition(ctx, env)
	}

	// Use ConditionVar if provided
	if n.config.ConditionVar != "" {
		val, ok := env.GetVar(n.config.ConditionVar)
		if !ok {
			return false, nil // Variable not found = condition not met
		}
		return isTruthy(val), nil
	}

	// No condition configured - always pass
	return true, nil
}

// isTruthy determines if a value should be considered true.
func isTruthy(v any) bool {
	if v == nil {
		return false
	}

	switch val := v.(type) {
	case bool:
		return val
	case int:
		return val != 0
	case int64:
		return val != 0
	case float64:
		return val != 0
	case string:
		return val != ""
	case []any:
		return len(val) > 0
	case map[string]any:
		return len(val) > 0
	default:
		// Non-nil, non-zero value is truthy
		return true
	}
}

// Ensure interface compliance at compile time.
var _ Node = (*GateNode)(nil)
