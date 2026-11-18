package tool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// ClassifiedPolicyEvaluator is the optional capability of evaluators
// whose decisions also consider a tool's effective safety
// classification (dangerous tools gated to approval even when the
// allowlist includes them). Implementing this interface means the
// caller should prefer CheckPolicyClassified when classification is
// known, and classification-only gates still apply throughEvaluatePolicy.
type ClassifiedPolicyEvaluator interface {
	EvaluatePolicyClassified(toolName string, class SafetyClassification) PolicyDecision
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
	base, ref := ReferenceNames(toolName)
	return EvaluatePolicyReferences(e.Effective, ref, base)
}

// EvaluatePolicyClassified applies the classification-aware evaluation to
// both references.
func (e EffectivePolicyEvaluator) EvaluatePolicyClassified(toolName string, class SafetyClassification) PolicyDecision {
	base, ref := ReferenceNames(toolName)
	if len(e.Effective.RequireApprovalClasses) == 0 {
		return e.EvaluatePolicy(toolName)
	}
	return EvaluateClassifiedReferences(e.Effective, class, ref, base)
}

// ReferenceNames derives the reference pair for a tool invocation: the
// full name as invoked ("tool.action") and the base registered tool
// name ("tool").
func ReferenceNames(toolName string) (base, ref string) {
	base, ref = toolName, toolName
	if idx := strings.LastIndexByte(toolName, '.'); idx > 0 && strings.IndexByte(toolName[:idx], '.') < 0 {
		base = toolName[:idx]
	}
	return base, ref
}

// MostRestrictiveDecision folds decisions: deny beats needs-approval
// beats allow; the default verdict is deny.
func MostRestrictiveDecision(decisions []PolicyDecision) PolicyDecision {
	var decision PolicyDecision
	for _, candidate := range decisions {
		switch candidate {
		case PolicyDeny:
			return PolicyDeny
		case PolicyNeedsApproval:
			decision = PolicyNeedsApproval
		case PolicyAllow:
			if decision == "" {
				decision = PolicyAllow
			}
		}
	}
	if decision == "" {
		return PolicyDeny
	}
	return decision
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

// CheckPolicyClassified is the classification-aware gate for callers that
// already hold the tool's effective safety classification (the daemon
// service has its registration). Evaluators that do not implement
// ClassifiedPolicyEvaluator fall back to name-only evaluation, which
// cannot see classification gates.
func CheckPolicyClassified(ctx context.Context, toolName string, class SafetyClassification) error {
	evaluator, ok := PolicyEvaluatorFromContext(ctx)
	if !ok {
		return nil
	}
	if classified, implementsClassified := evaluator.(ClassifiedPolicyEvaluator); implementsClassified {
		return checkPolicyDecision(classified.EvaluatePolicyClassified(toolName, class), toolName)
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

// IsApprovalRequired reports whether err is specifically an approval
// requirement (as opposed to an outright denial).
func IsApprovalRequired(err error) bool {
	return errors.Is(err, ErrToolApprovalRequired)
}

// ApprovalGrant is the invocation-scoped grant produced by a durable
// approval decision. Nodes that replay an approval install it so the
// service-level gate lets this one invocation through.
type ApprovalGrant struct {
	// ToolName is the base registered tool name ("tool").
	ToolName   string
	ApprovalID string
	// DecidedBy records who approved for the invocation trail.
	DecidedBy string
	// Reference is the approved name as invoked ("tool.action").
	Reference string
	// ArgumentsDigest is ArgumentsDigest(Reference, args) for the exact
	// arguments the operator approved. Empty only on legacy grants built
	// without invocation binding.
	ArgumentsDigest string
}

// Bound reports whether the grant is bound to one exact invocation
// (reference and arguments digest), as opposed to a legacy tool-name
// grant.
func (g ApprovalGrant) Bound() bool {
	return g.Reference != "" && g.ArgumentsDigest != ""
}

type approvalGrantKey struct{}

// ContextWithApprovalGrant attaches a validated approval grant for the
// named tool to the invocation context.
func ContextWithApprovalGrant(ctx context.Context, grant ApprovalGrant) context.Context {
	return context.WithValue(ctx, approvalGrantKey{}, grant)
}

// ApprovalGrantFromContext returns the attached grant, if any, so a tool
// can inspect the approved reference and arguments digest and compare
// them against its own canonical ArgumentsDigest.
func ApprovalGrantFromContext(ctx context.Context) (ApprovalGrant, bool) {
	grant, ok := ctx.Value(approvalGrantKey{}).(ApprovalGrant)
	return grant, ok
}

// ApprovalGrantForTool returns the attached grant when it matches the
// given base tool name.
//
// This is the weaker, compatibility lookup: it ignores the action and
// arguments the grant was approved for. Gates that know the invocation
// should use ApprovalGrantForInvocation.
func ApprovalGrantForTool(ctx context.Context, toolName string) (ApprovalGrant, bool) {
	grant, ok := ApprovalGrantFromContext(ctx)
	if !ok || grant.ToolName != toolName {
		return ApprovalGrant{}, false
	}
	return grant, true
}

// ApprovalGrantForInvocation returns the attached grant only when it is
// bound to exactly this invocation: the same reference ("tool.action")
// and the same arguments digest (see ArgumentsDigest). Legacy grants
// without a digest never match.
func ApprovalGrantForInvocation(ctx context.Context, reference, digest string) (ApprovalGrant, bool) {
	grant, ok := ApprovalGrantFromContext(ctx)
	if !ok || !grant.Bound() || reference == "" || digest == "" {
		return ApprovalGrant{}, false
	}
	if grant.Reference != reference || grant.ArgumentsDigest != digest {
		return ApprovalGrant{}, false
	}
	return grant, true
}

// approvalGrantForAction is the service-level grant lookup for one action
// invocation. A bound grant authorizes only the exact reference
// ("tool.action", as registered action tools are named) and inputs; a
// legacy grant without a digest falls back to the tool-name match.
func approvalGrantForAction(ctx context.Context, toolName, action string, inputs map[string]any) (ApprovalGrant, bool) {
	grant, ok := ApprovalGrantFromContext(ctx)
	if !ok {
		return ApprovalGrant{}, false
	}
	if !grant.Bound() {
		return ApprovalGrantForTool(ctx, toolName)
	}
	reference := toolName
	if action != "" {
		reference = toolName + "." + action
	}
	return ApprovalGrantForInvocation(ctx, reference, ArgumentsDigest(reference, inputs))
}

// argumentsDigestPrefix names the digest algorithm so the format can
// evolve without ambiguity.
const argumentsDigestPrefix = "sha256:"

// ArgumentsDigest returns the canonical digest that binds an approval to
// one invocation: "sha256:" followed by the lowercase hex SHA-256 of the
// canonical JSON encoding of
//
//	{"arguments": <args>, "reference": <reference>}
//
// Canonical JSON here is encoding/json output with object keys sorted,
// no insignificant whitespace, and no HTML escaping. A nil args map
// digests the same as an empty one. Numbers encode by value, so an
// argument digests identically before and after a JSON round trip
// (int 5 and float64 5 both encode as 5).
//
// The digest is not sensitive: it identifies the invocation without
// revealing its arguments. It returns "" when args cannot be encoded
// (for example NaN or a channel); an empty digest never matches a grant,
// so such invocations cannot be approved.
func ArgumentsDigest(reference string, args map[string]any) string {
	if args == nil {
		args = map[string]any{}
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(struct {
		Arguments map[string]any `json:"arguments"`
		Reference string         `json:"reference"`
	}{Arguments: args, Reference: reference})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return argumentsDigestPrefix + hex.EncodeToString(sum[:])
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
