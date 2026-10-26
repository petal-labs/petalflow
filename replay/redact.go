package replay

import (
	"regexp"
	"strings"
)

// RedactionAction determines how a matched field is treated.
type RedactionAction string

const (
	// RedactAction replaces string content with the policy placeholder.
	RedactAction RedactionAction = "redact"

	// DropAction removes the field entirely.
	DropAction RedactionAction = "drop"

	// KeepAction preserves the field as-is (explicit opt-in).
	KeepAction RedactionAction = "keep"
)

// FieldPolicy is a field-level rule applied on top of the policy's include
// flags. Field matches are case-insensitive against the recording field
// name; the last matching rule wins.
type FieldPolicy struct {
	Field  string
	Action RedactionAction
}

// RedactionPolicy controls what content survives into a recording. The zero
// value is the default-deny policy: prompts, outputs, tool data, and
// artifact contents are redacted, and credentials are scrubbed from
// everything that is kept. Use AllowAllPolicy for full-fidelity capture.
type RedactionPolicy struct {
	// IncludePrompts keeps LLM system prompts, messages, input text, and
	// envelope chat message content.
	IncludePrompts bool

	// IncludeOutputs keeps LLM completions, envelope inputs, and variable
	// values produced by nodes.
	IncludeOutputs bool

	// IncludeToolData keeps tool arguments and results.
	IncludeToolData bool

	// IncludeArtifacts keeps artifact text and bytes.
	IncludeArtifacts bool

	// ScrubCredentials scans kept strings for common credential formats
	// (API keys, bearer tokens, key/value secrets) and redacts matches.
	// Enabled by default; set false only for trusted local workflows.
	ScrubCredentials bool

	// Placeholder replaces redacted string content.
	// Defaults to "[redacted]".
	Placeholder string

	// Fields are extra field-level rules applied on top of the include
	// flags (highest precedence, last match wins).
	Fields []FieldPolicy
}

// DefaultPolicy returns the default-deny policy: metadata (timing, usage,
// structure) is kept; content is redacted.
func DefaultPolicy() RedactionPolicy {
	return RedactionPolicy{
		ScrubCredentials: true,
		Placeholder:      defaultPlaceholder,
	}
}

// AllowAllPolicy returns a full-fidelity policy that keeps all content.
// Use it only when the recording destination is trusted (e.g. local
// fixture storage for offline replay); the resulting artifact must be
// handled as sensitive data.
func AllowAllPolicy() RedactionPolicy {
	policy := DefaultPolicy()
	policy.IncludePrompts = true
	policy.IncludeOutputs = true
	policy.IncludeToolData = true
	policy.IncludeArtifacts = true
	return policy
}

const defaultPlaceholder = "[redacted]"

// placeholder returns the configured placeholder, or the default.
func (p RedactionPolicy) placeholder() string {
	if p.Placeholder != "" {
		return p.Placeholder
	}
	return defaultPlaceholder
}

// actionFor resolves the effective action for a named content category and
// field. Field rules take precedence over the include flags.
func (p RedactionPolicy) actionFor(category string, included bool) RedactionAction {
	action := RedactAction
	if included {
		action = KeepAction
	}
	for _, rule := range p.Fields {
		if !strings.EqualFold(rule.Field, category) {
			continue
		}
		switch rule.Action {
		case KeepAction, RedactAction, DropAction:
			action = rule.Action
		}
	}
	return action
}

// credentialPatterns match common secret formats embedded in kept strings.
var credentialPatterns = []*regexp.Regexp{
	// API-key style tokens: sk-..., ghp_..., AKIA..., petal-...
	regexp.MustCompile(`\b(?:sk|pk|ghp|gho|github|xoxb|xoxp|AKIA|petal)[-_][A-Za-z0-9]{8,}\b`),
	// Bearer / authorization headers.
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	// key/value secrets in strings: "api_key": "...", password=...
	regexp.MustCompile(`(?i)\b((?:api[_-]?key|secret|token|password|passwd|authorization)\s*["']?\s*[:=]\s*["']?)[A-Za-z0-9._~+/=-]{8,}`),
}

// scrubString redacts credential-shaped substrings.
func (p RedactionPolicy) scrubString(s string) string {
	if !p.ScrubCredentials || s == "" {
		return s
	}
	for _, pattern := range credentialPatterns {
		s = pattern.ReplaceAllString(s, p.placeholder())
	}
	return s
}

// redactString returns the redacted or scrubbed form of a kept string.
func (p RedactionPolicy) redactString(s, category string, included bool) string {
	if s == "" {
		return ""
	}
	if p.actionFor(category, included) != KeepAction {
		return p.placeholder()
	}
	return p.scrubString(s)
}

// redactStringDrop returns the string, or "" when the category is dropped.
func (p RedactionPolicy) redactStringDrop(s, category string, included bool) string {
	if p.actionFor(category, included) == DropAction {
		return ""
	}
	return p.redactString(s, category, included)
}

// redactValue redacts string leaves of an arbitrary value in place where
// possible. Maps and slices are walked; other values pass through unless
// the category is dropped (non-string scalars are kept — they carry
// structure, not content).
func (p RedactionPolicy) redactValue(v any, category string, included bool) any {
	switch p.actionFor(category, included) {
	case DropAction:
		return nil
	case RedactAction:
		return redactStringsDeep(v, p.placeholder())
	default:
		return scrubStringsDeep(v, p)
	}
}

// redactStringsDeep replaces every string leaf with the placeholder.
func redactStringsDeep(v any, placeholder string) any {
	switch typed := v.(type) {
	case string:
		if typed == "" {
			return typed
		}
		return placeholder
	case map[string]any:
		for k, item := range typed {
			typed[k] = redactStringsDeep(item, placeholder)
		}
		return typed
	case []any:
		for i, item := range typed {
			typed[i] = redactStringsDeep(item, placeholder)
		}
		return typed
	default:
		return v
	}
}

// scrubStringsDeep walks a value and scrubs credential patterns from string
// leaves.
func scrubStringsDeep(v any, policy RedactionPolicy) any {
	switch typed := v.(type) {
	case string:
		return policy.scrubString(typed)
	case map[string]any:
		for k, item := range typed {
			typed[k] = scrubStringsDeep(item, policy)
		}
		return typed
	case []any:
		for i, item := range typed {
			typed[i] = scrubStringsDeep(item, policy)
		}
		return typed
	default:
		return v
	}
}

// applyEnvelope redacts an envelope snapshot per policy. It mutates state.
func (p RedactionPolicy) applyEnvelope(state *EnvelopeState) {
	if state == nil {
		return
	}
	state.Input = p.redactValue(state.Input, "output", p.IncludeOutputs)

	for key, value := range state.Vars {
		state.Vars[key] = p.redactValue(value, "output", p.IncludeOutputs)
	}

	for i := range state.Artifacts {
		artifact := &state.Artifacts[i]
		artifact.Text = p.redactStringDrop(artifact.Text, "artifacts", p.IncludeArtifacts)
		if p.actionFor("artifacts", p.IncludeArtifacts) == RedactAction {
			artifact.Bytes = nil
		}
	}

	for i := range state.Messages {
		state.Messages[i].Content = p.redactStringDrop(state.Messages[i].Content, "prompts", p.IncludePrompts)
	}
}

// applyLLM redacts an LLM call record per policy.
func (p RedactionPolicy) applyLLM(call *LLMCallRecord) {
	call.System = p.redactStringDrop(call.System, "prompts", p.IncludePrompts)
	call.InputText = p.redactStringDrop(call.InputText, "prompts", p.IncludePrompts)
	call.Completion = p.redactStringDrop(call.Completion, "outputs", p.IncludeOutputs)

	for i := range call.Messages {
		call.Messages[i].Content = p.redactStringDrop(call.Messages[i].Content, "prompts", p.IncludePrompts)
	}
}

// applyTool redacts a tool call record per policy.
func (p RedactionPolicy) applyTool(call *ToolCallRecord) {
	if p.actionFor("tool", p.IncludeToolData) != KeepAction {
		call.Args = nil
		call.Result = nil
		return
	}
	call.Args, _ = scrubStringsDeep(call.Args, p).(map[string]any)
	call.Result, _ = scrubStringsDeep(call.Result, p).(map[string]any)
}
