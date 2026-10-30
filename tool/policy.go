package tool

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// PolicyDecision is the result of evaluating a tool policy for a tool name.
type PolicyDecision string

const (
	// PolicyAllow permits direct invocation.
	PolicyAllow PolicyDecision = "allow"
	// PolicyDeny invocation is forbidden. Deny always wins over every
	// other decision, including requires-approval.
	PolicyDeny PolicyDecision = "deny"
	// PolicyNeedsApproval permits invocation only after a durable human
	// approval is recorded (wired to Guardian rules in P1-05.3/P1-05.4).
	PolicyNeedsApproval PolicyDecision = "needs_approval"
)

// PolicySchemaVersion is the current version emitted for new policy objects.
const PolicySchemaVersion = 1

// ToolPolicy is a policy object anchoring tool allowlists, denylists, and
// approval requirements at either a tenant or a workflow. Both levels share
// one shape so resolution stays symmetric; see ResolveEffectivePolicy.
//
// List semantics:
//   - Each list entry is an exact tool name, "*" for every tool, or a
//     namespace pattern "prefix/*" matching any tool whose name begins
//     with "prefix/".
//   - nil means "not set" (the policy level does not speak to this rule
//     and does not participate in resolution). An explicitly empty list
//     differs from nil and is meaningful: it removes access across the
//     resolution merge for that rule.
//   - Deny always wins over Allow and RequireApproval, whatever the
//     listing level.
//
// Instance semantics:
//   - Version identifies the policy revision for storage versioning and
//     later audit correlation. Zero means unset/unknown.
//   - UpdatedAt records the last policy change; zero for values that have
//     never been persisted.
type ToolPolicy struct {
	Version int `json:"version,omitempty"`
	// Allow lists tools that may be invoked (subject to deny and
	// requirement rules above). Secure by default: a tool not matched by
	// an effective allow rule is denied. The JSON tag has no omitempty so
	// an explicit empty list ("deny everything") survives persistence
	// round trips; absent or null means "not set".
	Allow []string `json:"allow"`
	// Deny lists tools that are forbidden outright.
	Deny []string `json:"deny,omitempty"`
	// RequireApproval lists tools whose invocation is allowed but gated
	// behind durable human approval.
	RequireApproval []string `json:"require_approval,omitempty"`
	// UpdatedAt records the last policy change.
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// EffectiveToolPolicy is the deterministic outcome of merging the tenant
// default policy with the per-workflow policy.
type EffectiveToolPolicy struct {
	Allow           []string
	Deny            []string
	RequireApproval []string
}

// Evaluate returns the decision for the named tool under this policy
// alone, without merging with another policy level.
func (p ToolPolicy) Evaluate(toolName string) PolicyDecision {
	return EffectiveToolPolicy{
		Allow:           p.Allow,
		Deny:            p.Deny,
		RequireApproval: p.RequireApproval,
	}.Evaluate(toolName)
}

// ResolveEffectivePolicy merges the tenant-level policy with the
// per-workflow policy into an effective policy. Workflow policies narrow
// tenant policy but can never loosen it:
//
// Precedence (deterministic, conflict-free):
//  1. Deny: tenant and workflow deny rules union. A workflow can add denials
//     but cannot remove tenant denials.
//  2. Allow: the workflow allowlist replaces the tenant allowlist when the
//     workflow policy explicitly sets one (including an empty list to deny
//     everything). Replacing can only narrow, never grant tools that the
//     tenant default did not list.
//  3. RequireApproval: tenant and workflow approval requirements union. A
//     workflow can add approval gates but cannot remove tenant gates.
//
// A nil effective Allow list (no policy speaks to allow at either level)
// denies every tool; see Evaluate.
func ResolveEffectivePolicy(tenant, workflow *ToolPolicy) EffectiveToolPolicy {
	var effective EffectiveToolPolicy

	if tenant != nil {
		effective.Deny = append(effective.Deny, tenant.Deny...)
		effective.Allow = appendAllowIfSet(effective.Allow, tenant.Allow)
		effective.RequireApproval = append(effective.RequireApproval, tenant.RequireApproval...)
	}
	if workflow != nil {
		effective.Deny = append(effective.Deny, workflow.Deny...)
		effective.RequireApproval = append(effective.RequireApproval, workflow.RequireApproval...)
		// An explicitly set workflow allowlist replaces the tenant
		// allowlist (including an empty list, which removes access).
		if workflow.Allow != nil {
			effective.Allow = append(effective.Allow[:0:0], workflow.Allow...)
		}
	}

	effective.Allow = normalizeRuleList(effective.Allow)
	effective.Deny = normalizeRuleList(effective.Deny)
	effective.RequireApproval = normalizeRuleList(effective.RequireApproval)
	return effective
}

// Evaluate returns the decision for invoking the named tool under the
// effective policy.
//
// Decision precedence:
//  1. deny — the tool matches an effective deny rule.
//  2. needs_approval — allowed by an effective allow rule and matched by an
//     effective require-approval rule.
//  3. allow — allowed by an effective allow rule.
//  4. deny — secure-by-default fallback: without an allow rule, no tool is
//     invocable.
func (e EffectiveToolPolicy) Evaluate(toolName string) PolicyDecision {
	if toolName == "" {
		return PolicyDeny
	}
	if anyRuleMatches(e.Deny, toolName) {
		return PolicyDeny
	}
	if !anyRuleMatches(e.Allow, toolName) {
		return PolicyDeny
	}
	if anyRuleMatches(e.RequireApproval, toolName) {
		return PolicyNeedsApproval
	}
	return PolicyAllow
}

// Validate checks structural policy integrity: list entries must be valid
// patterns and the version non-negative.
func (p ToolPolicy) Validate() error {
	if p.Version < 0 {
		return fmt.Errorf("tool policy version must be >= 0")
	}
	for _, list := range []struct {
		name    string
		entries []string
	}{
		{"allow", p.Allow},
		{"deny", p.Deny},
		{"require_approval", p.RequireApproval},
	} {
		for _, entry := range list.entries {
			if err := validatePolicyRule(entry); err != nil {
				return fmt.Errorf("%s: %w", list.name, err)
			}
		}
	}
	return nil
}

// Normalized returns a copy with each rule list deduplicated and sorted so
// equal policies compare deterministically.
func (p ToolPolicy) Normalized() ToolPolicy {
	return ToolPolicy{
		Version:         p.Version,
		Allow:           normalizeRuleList(p.Allow),
		Deny:            normalizeRuleList(p.Deny),
		RequireApproval: normalizeRuleList(p.RequireApproval),
		UpdatedAt:       p.UpdatedAt,
	}
}

func validatePolicyRule(entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return fmt.Errorf("rule is empty")
	}
	if strings.ContainsAny(entry, " \t\r\n") {
		return fmt.Errorf("rule %q contains whitespace", entry)
	}
	if strings.Contains(entry, "**") {
		return fmt.Errorf("rule %q contains repeated '*'", entry)
	}
	if suffix := strings.TrimSuffix(entry, "/*"); suffix != entry {
		if strings.HasPrefix(suffix, "/") || strings.Contains(suffix, "/*") {
			return fmt.Errorf("rule %q has invalid namespace syntax", entry)
		}
	}
	return nil
}

// ruleMatches reports whether a single pattern matches the tool name.
func ruleMatches(pattern, toolName string) bool {
	switch {
	case pattern == "*":
		return true
	case strings.HasSuffix(pattern, "/*"):
		return strings.HasPrefix(toolName, pattern[:len(pattern)-1])
	default:
		return pattern == toolName
	}
}

func anyRuleMatches(patterns []string, toolName string) bool {
	for _, pattern := range patterns {
		if ruleMatches(pattern, toolName) {
			return true
		}
	}
	return false
}

// appendAllowIfSet copies explicit (set, possibly empty) allow rules from a
// policy. nil means "not set", so the lower-precedence value survives.
func appendAllowIfSet(current, incoming []string) []string {
	if incoming == nil {
		return current
	}
	if current == nil {
		out := make([]string, len(incoming))
		copy(out, incoming)
		return out
	}
	return append(current, incoming...)
}

func normalizeRuleList(entries []string) []string {
	if entries == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(entries))
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if _, exists := seen[entry]; exists {
			continue
		}
		seen[entry] = struct{}{}
		out = append(out, entry)
	}
	sort.Strings(out)
	return out
}
