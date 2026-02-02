package petalflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RouterNode is a node that can select which edges to activate.
type RouterNode interface {
	Node
	Route(ctx context.Context, env *Envelope) (RouteDecision, error)
}

// ConditionOp is an operator for rule conditions.
type ConditionOp string

const (
	OpEquals      ConditionOp = "eq"
	OpNotEquals   ConditionOp = "neq"
	OpContains    ConditionOp = "contains"
	OpGreaterThan ConditionOp = "gt"
	OpLessThan    ConditionOp = "lt"
	OpExists      ConditionOp = "exists"
	OpNotExists   ConditionOp = "not_exists"
	OpIn          ConditionOp = "in"
)

// RouteCondition defines a condition for rule-based routing.
type RouteCondition struct {
	// VarPath is the envelope variable path to evaluate (supports dot notation).
	VarPath string

	// Op is the comparison operator.
	Op ConditionOp

	// Value is the value to compare against.
	Value any

	// Values is used with OpIn for multiple value matching.
	Values []any
}

// RouteRule defines a routing rule.
type RouteRule struct {
	// Conditions that must all be true for this rule to match.
	Conditions []RouteCondition

	// Target is the node ID to route to if conditions match.
	Target string

	// Reason is a human-readable explanation for debugging.
	Reason string
}

// RuleRouterConfig configures a rule-based router.
type RuleRouterConfig struct {
	// Rules are evaluated in order; first match wins.
	Rules []RouteRule

	// DefaultTarget is used when no rules match.
	DefaultTarget string

	// DecisionKey stores the routing decision in the envelope.
	DecisionKey string

	// AllowMultiple allows multiple rules to match (fan-out).
	AllowMultiple bool
}

// RuleRouter routes based on envelope variable values.
type RuleRouter struct {
	BaseNode
	config RuleRouterConfig
}

// NewRuleRouter creates a new rule-based router.
func NewRuleRouter(id string, config RuleRouterConfig) *RuleRouter {
	if config.DecisionKey == "" {
		config.DecisionKey = id + "_decision"
	}

	return &RuleRouter{
		BaseNode: NewBaseNode(id, NodeKindRouter),
		config:   config,
	}
}

// Run executes the router and stores the decision.
func (r *RuleRouter) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	decision, err := r.Route(ctx, env)
	if err != nil {
		return nil, err
	}

	// Store decision in envelope
	env.SetVar(r.config.DecisionKey, decision)

	return env, nil
}

// Route evaluates rules and returns the routing decision.
func (r *RuleRouter) Route(ctx context.Context, env *Envelope) (RouteDecision, error) {
	var targets []string
	var reasons []string

	for _, rule := range r.config.Rules {
		if r.evaluateRule(env, rule) {
			targets = append(targets, rule.Target)
			reasons = append(reasons, rule.Reason)

			if !r.config.AllowMultiple {
				break
			}
		}
	}

	// Use default if no matches
	if len(targets) == 0 && r.config.DefaultTarget != "" {
		targets = []string{r.config.DefaultTarget}
		reasons = []string{"default route"}
	}

	return RouteDecision{
		Targets: targets,
		Reason:  strings.Join(reasons, "; "),
	}, nil
}

// evaluateRule checks if all conditions in a rule are satisfied.
func (r *RuleRouter) evaluateRule(env *Envelope, rule RouteRule) bool {
	for _, cond := range rule.Conditions {
		if !r.evaluateCondition(env, cond) {
			return false
		}
	}
	return true
}

// evaluateCondition checks if a single condition is satisfied.
func (r *RuleRouter) evaluateCondition(env *Envelope, cond RouteCondition) bool {
	// Get the value from envelope
	val, exists := env.GetVarNested(cond.VarPath)
	if !exists {
		val, exists = env.GetVar(cond.VarPath)
	}

	switch cond.Op {
	case OpExists:
		return exists
	case OpNotExists:
		return !exists
	case OpEquals:
		return exists && compare(val, cond.Value) == 0
	case OpNotEquals:
		return !exists || compare(val, cond.Value) != 0
	case OpGreaterThan:
		return exists && compare(val, cond.Value) > 0
	case OpLessThan:
		return exists && compare(val, cond.Value) < 0
	case OpContains:
		return exists && containsValue(val, cond.Value)
	case OpIn:
		return exists && inValues(val, cond.Values)
	default:
		return false
	}
}

// LLMRouterConfig configures an LLM-based router.
type LLMRouterConfig struct {
	// Model is the LLM model to use for routing decisions.
	Model string

	// System is the system prompt for the router LLM.
	System string

	// InputVars specifies which envelope variables to include in the prompt.
	InputVars []string

	// AllowedTargets maps labels to node IDs.
	// The LLM must output one of these labels.
	AllowedTargets map[string]string

	// DecisionKey stores the routing decision in the envelope.
	DecisionKey string

	// Temperature for the LLM call (lower = more deterministic).
	Temperature *float64

	// Timeout for the LLM call.
	Timeout time.Duration

	// RetryPolicy for the LLM call.
	RetryPolicy RetryPolicy
}

// LLMRouter routes based on LLM classification.
type LLMRouter struct {
	BaseNode
	config LLMRouterConfig
	client LLMClient
}

// NewLLMRouter creates a new LLM-based router.
func NewLLMRouter(id string, client LLMClient, config LLMRouterConfig) *LLMRouter {
	if config.DecisionKey == "" {
		config.DecisionKey = id + "_decision"
	}
	if config.RetryPolicy.MaxAttempts == 0 {
		config.RetryPolicy = DefaultRetryPolicy()
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	// Use low temperature by default for deterministic routing
	if config.Temperature == nil {
		temp := 0.1
		config.Temperature = &temp
	}

	return &LLMRouter{
		BaseNode: NewBaseNode(id, NodeKindRouter),
		config:   config,
		client:   client,
	}
}

// Run executes the router and stores the decision.
func (r *LLMRouter) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	decision, err := r.Route(ctx, env)
	if err != nil {
		return nil, err
	}

	// Store decision in envelope
	env.SetVar(r.config.DecisionKey, decision)

	return env, nil
}

// Route uses an LLM to make routing decisions.
func (r *LLMRouter) Route(ctx context.Context, env *Envelope) (RouteDecision, error) {
	// Apply timeout
	if r.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.config.Timeout)
		defer cancel()
	}

	// Build the prompt
	prompt := r.buildPrompt(env)

	// Build JSON schema for constrained output
	labels := make([]string, 0, len(r.config.AllowedTargets))
	for label := range r.config.AllowedTargets {
		labels = append(labels, label)
	}

	// Request the LLM to choose from allowed labels
	req := LLMRequest{
		Model:       r.config.Model,
		System:      r.buildSystemPrompt(labels),
		InputText:   prompt,
		Temperature: r.config.Temperature,
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"choice": map[string]any{
					"type": "string",
					"enum": labels,
				},
				"reason": map[string]any{
					"type": "string",
				},
				"confidence": map[string]any{
					"type":    "number",
					"minimum": 0,
					"maximum": 1,
				},
			},
			"required": []string{"choice"},
		},
	}

	// Execute with retries
	var resp LLMResponse
	var lastErr error

	for attempt := 1; attempt <= r.config.RetryPolicy.MaxAttempts; attempt++ {
		resp, lastErr = r.client.Complete(ctx, req)
		if lastErr == nil {
			break
		}

		if ctx.Err() != nil {
			return RouteDecision{}, ctx.Err()
		}

		if attempt < r.config.RetryPolicy.MaxAttempts {
			select {
			case <-ctx.Done():
				return RouteDecision{}, ctx.Err()
			case <-time.After(r.config.RetryPolicy.Backoff * time.Duration(attempt)):
			}
		}
	}

	if lastErr != nil {
		return RouteDecision{}, fmt.Errorf("LLM router failed: %w", lastErr)
	}

	// Parse the response
	return r.parseResponse(resp)
}

// buildPrompt constructs the classification prompt.
func (r *LLMRouter) buildPrompt(env *Envelope) string {
	var parts []string

	for _, varName := range r.config.InputVars {
		if val, ok := env.GetVar(varName); ok {
			parts = append(parts, toString(val))
		}
	}

	return strings.Join(parts, "\n\n")
}

// buildSystemPrompt creates the system prompt for classification.
func (r *LLMRouter) buildSystemPrompt(labels []string) string {
	base := r.config.System
	if base == "" {
		base = "You are a classifier. Analyze the input and choose the most appropriate category."
	}

	return fmt.Sprintf("%s\n\nYou must choose exactly one of these options: %s\n\nRespond with JSON containing your choice and brief reason.",
		base, strings.Join(labels, ", "))
}

// parseResponse extracts routing decision from LLM response.
func (r *LLMRouter) parseResponse(resp LLMResponse) (RouteDecision, error) {
	decision := RouteDecision{
		Meta: make(map[string]any),
	}

	// Try to parse JSON response
	var parsed struct {
		Choice     string   `json:"choice"`
		Reason     string   `json:"reason"`
		Confidence *float64 `json:"confidence"`
	}

	// First try the JSON field
	if resp.JSON != nil {
		data, _ := json.Marshal(resp.JSON)
		if err := json.Unmarshal(data, &parsed); err == nil && parsed.Choice != "" {
			goto found
		}
	}

	// Try parsing the text output
	if err := json.Unmarshal([]byte(resp.Text), &parsed); err != nil {
		// Try to find a label in the raw text
		for label := range r.config.AllowedTargets {
			if strings.Contains(strings.ToLower(resp.Text), strings.ToLower(label)) {
				parsed.Choice = label
				parsed.Reason = "extracted from text"
				goto found
			}
		}
		return RouteDecision{}, fmt.Errorf("could not parse routing decision from: %s", resp.Text)
	}

found:
	// Map label to target
	target, ok := r.config.AllowedTargets[parsed.Choice]
	if !ok {
		return RouteDecision{}, fmt.Errorf("invalid choice %q, allowed: %v", parsed.Choice, r.config.AllowedTargets)
	}

	decision.Targets = []string{target}
	decision.Reason = parsed.Reason
	decision.Confidence = parsed.Confidence
	decision.Meta["choice"] = parsed.Choice
	decision.Meta["model"] = resp.Model

	return decision, nil
}

// Config returns the router's configuration.
func (r *LLMRouter) Config() LLMRouterConfig {
	return r.config
}

// Config returns the rule router's configuration.
func (r *RuleRouter) Config() RuleRouterConfig {
	return r.config
}

// Helper functions for condition evaluation

// compare compares two values and returns -1, 0, or 1.
func compare(a, b any) int {
	// Handle numeric comparisons
	aNum, aOk := toFloat64(a)
	bNum, bOk := toFloat64(b)
	if aOk && bOk {
		if aNum < bNum {
			return -1
		}
		if aNum > bNum {
			return 1
		}
		return 0
	}

	// String comparison
	aStr := fmt.Sprintf("%v", a)
	bStr := fmt.Sprintf("%v", b)
	if aStr < bStr {
		return -1
	}
	if aStr > bStr {
		return 1
	}
	return 0
}

// toFloat64 attempts to convert a value to float64.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// containsValue checks if a value contains another value.
func containsValue(container, value any) bool {
	containerStr := fmt.Sprintf("%v", container)
	valueStr := fmt.Sprintf("%v", value)
	return strings.Contains(containerStr, valueStr)
}

// inValues checks if a value is in a list of values.
func inValues(val any, values []any) bool {
	for _, v := range values {
		if compare(val, v) == 0 {
			return true
		}
	}
	return false
}

// Ensure interface compliance at compile time.
var (
	_ Node       = (*RuleRouter)(nil)
	_ Node       = (*LLMRouter)(nil)
	_ RouterNode = (*RuleRouter)(nil)
	_ RouterNode = (*LLMRouter)(nil)
)
