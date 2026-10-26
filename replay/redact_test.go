package replay

import (
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/core"
)

func sampleState() *EnvelopeState {
	return &EnvelopeState{
		Input: "user prompt text",
		Vars: map[string]any{
			"summary": "generated summary text",
			"count":   float64(3),
			"nested":  map[string]any{"note": "nested secret note"},
		},
		Artifacts: []ArtifactState{{
			ID:   "a1",
			Type: "document",
			Text: "artifact body text",
		}},
		Messages: []MessageState{{Role: "user", Content: "chat message content"}},
	}
}

func TestDefaultPolicyRedactsContent(t *testing.T) {
	policy := DefaultPolicy()
	state := sampleState()
	policy.applyEnvelope(state)

	if state.Input != defaultPlaceholder {
		t.Errorf("Input = %q, want redacted", state.Input)
	}
	if state.Vars["summary"] != defaultPlaceholder {
		t.Errorf("vars.summary = %v, want redacted", state.Vars["summary"])
	}
	// Non-string scalars keep structure.
	if state.Vars["count"] != float64(3) {
		t.Errorf("vars.count = %v, want kept", state.Vars["count"])
	}
	if nested, ok := state.Vars["nested"].(map[string]any); !ok || nested["note"] != defaultPlaceholder {
		t.Errorf("vars.nested = %v, want deep redaction", state.Vars["nested"])
	}
	if state.Artifacts[0].Text != defaultPlaceholder {
		t.Errorf("artifact text = %q, want redacted", state.Artifacts[0].Text)
	}
	if state.Messages[0].Content != defaultPlaceholder {
		t.Errorf("message content = %q, want redacted", state.Messages[0].Content)
	}
	// Metadata survives.
	if state.Artifacts[0].ID != "a1" || state.Messages[0].Role != "user" {
		t.Errorf("metadata should survive redaction: %+v", state)
	}
}

func TestAllowAllPolicyKeepsContent(t *testing.T) {
	policy := AllowAllPolicy()
	state := sampleState()
	policy.applyEnvelope(state)

	if state.Input != "user prompt text" {
		t.Errorf("Input = %q, want kept", state.Input)
	}
	if state.Vars["summary"] != "generated summary text" {
		t.Errorf("vars.summary = %v, want kept", state.Vars["summary"])
	}
	if state.Artifacts[0].Text != "artifact body text" || state.Messages[0].Content != "chat message content" {
		t.Errorf("artifacts/messages should be kept: %+v", state)
	}
}

func TestFieldPolicyOverridesIncludes(t *testing.T) {
	policy := AllowAllPolicy()
	policy.Fields = []FieldPolicy{{Field: "prompts", Action: DropAction}}

	call := LLMCallRecord{
		System:     "system prompt",
		Completion: "the completion",
		Messages:   []LLMMessageRef{{Role: "user", Content: "hi"}},
	}
	policy.applyLLM(&call)

	if call.System != "" {
		t.Errorf("System = %q, want dropped", call.System)
	}
	if len(call.Messages) != 0 || (len(call.Messages) == 0 && call.Messages != nil) {
		// Messages content dropped leaves empty strings in place.
	}
	for _, m := range call.Messages {
		if m.Content != "" {
			t.Errorf("message content = %q, want dropped", m.Content)
		}
	}
	if call.Completion != "the completion" {
		t.Errorf("Completion = %q, want kept (different category)", call.Completion)
	}
}

func TestLLMRedaction(t *testing.T) {
	call := LLMCallRecord{
		NodeID:     "llm",
		Model:      "test-model",
		System:     "you are a helpful assistant",
		InputText:  "what is the launch code",
		Completion: "the launch code is alpha-7",
		Messages:   []LLMMessageRef{{Role: "user", Content: "what is the launch code"}},
		Usage:      UsageRecord{TotalTokens: 10},
	}

	// Default: prompts and outputs redacted, usage and model kept.
	DefaultPolicy().applyLLM(&call)
	if call.System != defaultPlaceholder || call.InputText != defaultPlaceholder || call.Completion != defaultPlaceholder {
		t.Errorf("llm content not redacted: %+v", call)
	}
	if call.Messages[0].Content != defaultPlaceholder {
		t.Errorf("message not redacted: %+v", call.Messages[0])
	}
	if call.Model != "test-model" || call.Usage.TotalTokens != 10 {
		t.Errorf("metadata should survive: %+v", call)
	}
}

func TestToolRedaction(t *testing.T) {
	call := ToolCallRecord{
		NodeID:   "tool",
		ToolName: "http_fetch",
		Args:     map[string]any{"url": "https://example.com", "api_key": "sk-abcdef123456"},
		Result:   map[string]any{"body": "response body"},
	}

	DefaultPolicy().applyTool(&call)
	if call.Args != nil || call.Result != nil {
		t.Errorf("tool data should be dropped by default: %+v", call)
	}
	if call.ToolName != "http_fetch" {
		t.Errorf("tool name should survive: %+v", call)
	}

	allow := AllowAllPolicy()
	kept := ToolCallRecord{
		NodeID:   "tool",
		ToolName: "http_fetch",
		Args:     map[string]any{"url": "https://example.com", "api_key": "sk-abcdef123456"},
		Result:   map[string]any{"body": "response body"},
	}
	allow.applyTool(&kept)
	if kept.Args["url"] != "https://example.com" {
		t.Errorf("args should be kept under AllowAll: %+v", kept.Args)
	}
	if !strings.Contains(kept.Args["api_key"].(string), defaultPlaceholder) {
		t.Errorf("credential-looking arg should be scrubbed: %v", kept.Args["api_key"])
	}
}

func TestCredentialScrubbing(t *testing.T) {
	policy := DefaultPolicy()
	policy.IncludeOutputs = true // keep content, still scrub credentials

	state := &EnvelopeState{
		Vars: map[string]any{
			"config": "endpoint=https://api.example.com api_key=sk-abcdef123456 done",
		},
	}
	policy.applyEnvelope(state)

	got := state.Vars["config"].(string)
	if strings.Contains(got, "sk-abcdef123456") {
		t.Errorf("credential leaked: %q", got)
	}
	if !strings.Contains(got, "endpoint=https://api.example.com") {
		t.Errorf("non-credential content should survive: %q", got)
	}
}

func TestScrubBearerToken(t *testing.T) {
	policy := DefaultPolicy()
	got := policy.scrubString("Authorization: Bearer abcdefgh123456 rest")
	if strings.Contains(got, "abcdefgh123456") {
		t.Errorf("bearer token leaked: %q", got)
	}
}

func TestScrubDisabled(t *testing.T) {
	policy := DefaultPolicy()
	policy.ScrubCredentials = false
	got := policy.scrubString("key sk-abcdef123456")
	if !strings.Contains(got, "sk-abcdef123456") {
		t.Errorf("scrubbing disabled should keep string: %q", got)
	}
}

func TestCustomPlaceholder(t *testing.T) {
	policy := DefaultPolicy()
	policy.Placeholder = "<<gone>>"
	state := &EnvelopeState{Input: "text"}
	policy.applyEnvelope(state)
	if state.Input != "<<gone>>" {
		t.Errorf("Input = %q, want custom placeholder", state.Input)
	}
}

func TestRedactArtifactBytes(t *testing.T) {
	policy := DefaultPolicy()
	state := &EnvelopeState{
		Artifacts: []ArtifactState{{ID: "a", Bytes: []byte("binary secret")}},
	}
	policy.applyEnvelope(state)
	if state.Artifacts[0].Bytes != nil {
		t.Errorf("artifact bytes should be dropped under redaction: %+v", state.Artifacts[0])
	}
}

func TestNilEnvelopeState(t *testing.T) {
	DefaultPolicy().applyEnvelope(nil) // must not panic
}

func TestEnvelopeCloneRedactionIsolation(t *testing.T) {
	// Redaction applied to a snapshot must not leak into the source envelope.
	env := core.NewEnvelope().WithInput("secret input")
	env.SetVar("k", "secret value")

	capture := NewCapture(DefaultPolicy())
	snapshot := capture.snapshotEnvelope(env)

	if snapshot.Input != defaultPlaceholder {
		t.Errorf("snapshot input = %v, want redacted", snapshot.Input)
	}
	if env.Input != "secret input" {
		t.Errorf("source envelope mutated: %v", env.Input)
	}
	if v, _ := env.GetVar("k"); v != "secret value" {
		t.Errorf("source vars mutated: %v", v)
	}
}
