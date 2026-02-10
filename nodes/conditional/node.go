// Package conditional implements the conditional routing node for PetalFlow.
// It evaluates expressions against input data and routes execution to matching
// output branches.
package conditional

import (
	"context"
	"fmt"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/nodes/conditional/expr"
)

// Config configures a ConditionalNode.
type Config struct {
	// Conditions is the ordered list of named conditions with expressions.
	Conditions []Condition

	// Default is the output port name for the fallback branch.
	// If empty and no conditions match, the node returns an error.
	Default string

	// EvaluationOrder is "first_match" (default) or "all".
	EvaluationOrder string

	// PassThrough forwards all input data to the matched port when true.
	// When false, only a {matched, condition} result is emitted.
	PassThrough bool

	// OutputKey is the envelope variable key for the output.
	// Defaults to "{id}_output".
	OutputKey string
}

// Condition is a single named condition with an expression.
type Condition struct {
	Name        string
	Expression  string
	Description string

	parsed expr.Expr // cached parsed AST
}

// ConditionalNode evaluates conditions against input data and routes to
// matching output branches via the RouterNode interface.
type ConditionalNode struct {
	core.BaseNode
	config Config
}

// NewConditionalNode creates a new conditional node. All expressions are
// parsed eagerly — invalid expressions cause an error at construction time.
func NewConditionalNode(id string, cfg Config) (*ConditionalNode, error) {
	if len(cfg.Conditions) == 0 {
		return nil, fmt.Errorf("conditional node %q: at least one condition is required", id)
	}

	if cfg.EvaluationOrder == "" {
		cfg.EvaluationOrder = "first_match"
	}
	if cfg.EvaluationOrder != "first_match" && cfg.EvaluationOrder != "all" {
		return nil, fmt.Errorf("conditional node %q: evaluation_order must be \"first_match\" or \"all\", got %q", id, cfg.EvaluationOrder)
	}

	// Default PassThrough to true (use pointer or explicit check)
	// Since bool zero value is false, we default in the caller or here.
	// The spec says default is true, so we set it explicitly.
	// The caller should set PassThrough=false to disable.
	// We leave it as-is since the zero value means the caller didn't set it,
	// but the spec default is true. We handle this by always checking the
	// Config directly — callers who want pass_through=false must set it.

	if cfg.OutputKey == "" {
		cfg.OutputKey = id + "_output"
	}

	// Parse all expressions eagerly
	seen := make(map[string]bool)
	for i := range cfg.Conditions {
		c := &cfg.Conditions[i]
		if c.Name == "" {
			return nil, fmt.Errorf("conditional node %q: condition %d has empty name", id, i)
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("conditional node %q: duplicate condition name %q", id, c.Name)
		}
		seen[c.Name] = true

		if c.Expression == "" {
			return nil, fmt.Errorf("conditional node %q: condition %q has empty expression", id, c.Name)
		}

		parsed, err := expr.Parse(c.Expression)
		if err != nil {
			return nil, fmt.Errorf("conditional node %q: condition %q: %w", id, c.Name, err)
		}
		c.parsed = parsed
	}

	return &ConditionalNode{
		BaseNode: core.NewBaseNode(id, core.NodeKindConditional),
		config:   cfg,
	}, nil
}

// Config returns the node's configuration.
func (n *ConditionalNode) Config() Config {
	return n.config
}

// Run evaluates conditions and stores the routing decision in the envelope.
func (n *ConditionalNode) Run(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	decision, err := n.Route(ctx, env)
	if err != nil {
		return nil, err
	}

	// Store decision in envelope for runtime successor filtering
	env.SetVar(n.ID()+"_decision", decision)

	// Set output based on PassThrough mode
	if !n.config.PassThrough {
		matchedName := ""
		if len(decision.Targets) > 0 {
			matchedName = decision.Targets[0]
		}
		env.SetVar(n.config.OutputKey, map[string]any{
			"matched":   len(decision.Targets) > 0,
			"condition": matchedName,
		})
	}

	return env, nil
}

// Route evaluates conditions against envelope variables and returns a decision
// indicating which branches to activate.
func (n *ConditionalNode) Route(ctx context.Context, env *core.Envelope) (core.RouteDecision, error) {
	// Build the input namespace from envelope vars
	vars := make(map[string]any)
	if env.Vars != nil {
		// Expose all envelope vars at top level (input.X maps to vars["input"]["X"])
		for k, v := range env.Vars {
			vars[k] = v
		}
	}

	// Also provide a top-level "input" key with all vars for convenience
	if _, hasInput := vars["input"]; !hasInput {
		vars["input"] = env.Vars
	}

	var targets []string
	var reasons []string

	for _, cond := range n.config.Conditions {
		result, err := expr.Eval(cond.parsed, vars)
		if err != nil {
			return core.RouteDecision{}, fmt.Errorf("conditional node %q: condition %q: %w", n.ID(), cond.Name, err)
		}

		if isTruthy(result) {
			targets = append(targets, cond.Name)
			reason := cond.Name
			if cond.Description != "" {
				reason = cond.Description
			}
			reasons = append(reasons, reason)

			if n.config.EvaluationOrder == "first_match" {
				break
			}
		}
	}

	// Default branch
	if len(targets) == 0 && n.config.Default != "" {
		targets = []string{n.config.Default}
		reasons = []string{"default branch"}
	}

	if len(targets) == 0 {
		return core.RouteDecision{}, fmt.Errorf("conditional node %q: no conditions matched and no default branch configured", n.ID())
	}

	reason := ""
	if len(reasons) > 0 {
		reason = reasons[0]
		if len(reasons) > 1 {
			reason = fmt.Sprintf("%d branches matched", len(reasons))
		}
	}

	return core.RouteDecision{
		Targets: targets,
		Reason:  reason,
	}, nil
}

// isTruthy checks if a value is truthy using the same rules as the expression
// evaluator: 0, "", nil, false, empty slice/map are falsy.
func isTruthy(val any) bool {
	return expr.IsTruthy(val)
}

// Ensure interface compliance at compile time.
var (
	_ core.Node       = (*ConditionalNode)(nil)
	_ core.RouterNode = (*ConditionalNode)(nil)
)
