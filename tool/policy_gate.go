package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ToolErrorCodePolicyDenied is returned when the effective tool policy
// forbids invoking a tool.
const ToolErrorCodePolicyDenied = "POLICY_DENIED"

// ToolErrorCodePolicyApprovalRequired is returned when the effective tool
// policy allows a tool only after durable human approval.
const ToolErrorCodePolicyApprovalRequired = "POLICY_APPROVAL_REQUIRED"

// Errors returned by the policy gate. Callers should check these with
// errors.Is; IsToolPolicyError distinguishes gate rejections generally.
var (
	// ErrToolPolicyDenied marks invocations forbidden by policy. It is
	// never retryable and must never be converted into a network call.
	ErrToolPolicyDenied = errors.New("tool: denied by tool policy")
	// ErrToolApprovalRequired marks invocations whose policy allows the
	// tool only after durable human approval is recorded.
	ErrToolApprovalRequired = errors.New("tool: approval required by tool policy")
)

// PolicyEvaluator answers tool-policy questions for the caller's scope
// (a workflow run, a tenant request, or a daemon). Implementations
// resolve tenant and workflow policies before returning a decision.
type PolicyEvaluator interface {
	EvaluatePolicy(toolName string) PolicyDecision
}

// PolicyEvaluatorFunc adapts a function to PolicyEvaluator.
type PolicyEvaluatorFunc func(toolName string) PolicyDecision

// EvaluatePolicy implements PolicyEvaluator.
func (f PolicyEvaluatorFunc) EvaluatePolicy(toolName string) PolicyDecision {
	return f(toolName)
}

// EffectivePolicyEvaluator evaluates an EffectiveToolPolicy against both
// the full reference ("tool.action") and the base tool name ("tool") so
// namespace rules match regardless of how the caller references the tool.
type EffectivePolicyEvaluator struct {
	Effective EffectiveToolPolicy
}

// EvaluatePolicy returns the most restrictive decision across the
// tool-name references derived from toolName.
func (e EffectivePolicyEvaluator) EvaluatePolicy(toolName string) PolicyDecision {
	base := toolName
	if idx := strings.LastIndexByte(toolName, '.'); idx > 0 && strings.IndexByte(toolName[:idx], '.') < 0 {
		base = toolName[:idx]
	}
	return EvaluatePolicyReferences(e.Effective, toolName, base)
}

// FromEffectivePolicy returns the evaluator for an effective policy.
func FromEffectivePolicy(effective EffectiveToolPolicy) PolicyEvaluator {
	return EffectivePolicyEvaluator{Effective: effective}
}

// EvaluatePolicyReferences returns the decision for a tool given all its
// reference names (for example the full "tool.action" reference and the
// bare tool name).
//
// The references all denote one invocation, so:
//   - a deny match on any reference denies the invocation,
//   - a require-approval match on any reference gates it,
//   - an allow match on any reference permits it,
//   - no allow match on any reference is a default-deny.
func EvaluatePolicyReferences(effective EffectiveToolPolicy, names ...string) PolicyDecision {
	permitted := false
	gated := false
	for _, name := range names {
		if name == "" {
			continue
		}
		if anyRuleMatches(effective.Deny, name) {
			return PolicyDeny
		}
		if !anyRuleMatches(effective.Allow, name) {
			continue
		}
		permitted = true
		if anyRuleMatches(effective.RequireApproval, name) {
			gated = true
		}
	}
	switch {
	case gated:
		return PolicyNeedsApproval
	case permitted:
		return PolicyAllow
	default:
		return PolicyDeny
	}
}

// ContextWithPolicyEvaluator returns a context whose descendants consult
// the evaluator in CheckPolicy and other policy-aware paths.
func ContextWithPolicyEvaluator(ctx context.Context, evaluator PolicyEvaluator) context.Context {
	return context.WithValue(ctx, policyEvaluatorKey{}, evaluator)
}

// PolicyEvaluatorFromContext returns the installed evaluator. A missing
// evaluator means tool-policy enforcement was never opted into (embedded
// library compatibility); callers treat that as allow.
func PolicyEvaluatorFromContext(ctx context.Context) (PolicyEvaluator, bool) {
	evaluator, ok := ctx.Value(policyEvaluatorKey{}).(PolicyEvaluator)
	return evaluator, ok && evaluator != nil
}

type policyEvaluatorKey struct{}

// CheckPolicy is the invocation gate: it resolves the caller's effective
// policy for the named tool and returns
//
//   - nil when the invocation may proceed,
//   - ErrToolPolicyDenied when the effective policy denies the tool, or
//   - ErrToolApprovalRequired when the effective policy allows the tool
//     but requires durable approval first.
//
// Gate rejections precede every side effect. Without a policy evaluator
// in the context, enforcement was not opted into and the invocation
// proceeds (parallel to how server authentication is opt-in for embedded
// library compatibility).
func CheckPolicy(ctx context.Context, toolName string) error {
	evaluator, ok := PolicyEvaluatorFromContext(ctx)
	if !ok {
		return nil
	}
	return checkPolicyDecision(evaluator.EvaluatePolicy(toolName), toolName)
}

// CheckPolicyEvaluator is a CheckPolicy variant with explicit resolution
// for paths that already hold an effective policy outside the context.
func CheckPolicyEvaluator(evaluator PolicyEvaluator, toolName string) error {
	return checkPolicyDecision(evaluator.EvaluatePolicy(toolName), toolName)
}

func checkPolicyDecision(decision PolicyDecision, toolName string) error {
	switch decision {
	case PolicyAllow:
		return nil
	case PolicyNeedsApproval:
		return fmt.Errorf("%w: %q", ErrToolApprovalRequired, toolName)
	default:
		return fmt.Errorf("%w: %q", ErrToolPolicyDenied, toolName)
	}
}

// IsToolPolicyError reports whether err is a policy-gate rejection.
func IsToolPolicyError(err error) bool {
	return errors.Is(err, ErrToolPolicyDenied) || errors.Is(err, ErrToolApprovalRequired)
}

// PolicyErrorCode maps a gate rejection to its stable error code; empty
// for non-policy errors.
func PolicyErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrToolApprovalRequired):
		return ToolErrorCodePolicyApprovalRequired
	case errors.Is(err, ErrToolPolicyDenied):
		return ToolErrorCodePolicyDenied
	default:
		return ""
	}
}
