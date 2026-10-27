package capability

import (
	"errors"
	"strings"
	"testing"
)

func TestRegistryStaticDefaults(t *testing.T) {
	r := NewRegistry()

	// Provider defaults apply when no model entry matches.
	caps, known := r.Model("openai", "gpt-4o")
	if !known || !caps.Has(CapabilityTools) || !caps.Has(CapabilityStructuredOutput) {
		t.Errorf("openai gpt-4o = %v known=%v", caps.Names(), known)
	}

	// Model entries override provider defaults.
	caps, known = r.Model("openai", "text-embedding-3-small")
	if !known || !caps.Has(CapabilityEmbeddings) {
		t.Errorf("embeddings model = %v known=%v", caps.Names(), known)
	}
	if caps.Has(CapabilityTools) {
		t.Error("embedding model must not claim chat capabilities")
	}

	// Reasoning models via exact entry.
	caps, _ = r.Model("openai", "o3")
	if !caps.Has(CapabilityReasoning) {
		t.Errorf("o3 = %v, want reasoning", caps.Names())
	}

	// Pattern entries cover dated snapshots.
	caps, _ = r.Model("anthropic", "claude-sonnet-4-20250514")
	if !caps.Has(CapabilityReasoning) || !caps.Has(CapabilityTools) {
		t.Errorf("claude-sonnet-4 dated snapshot = %v", caps.Names())
	}

	// Conservative provider default for ollama: streaming only.
	caps, _ = r.Model("ollama", "some-local-model")
	if !caps.Has(CapabilityStreaming) || caps.Has(CapabilityTools) {
		t.Errorf("ollama unknown model = %v, want streaming only", caps.Names())
	}
}

func TestRegistryUnknownModelFailsClosed(t *testing.T) {
	r := NewRegistry()

	caps, known := r.Model("acme", "private-model")
	if known {
		t.Fatal("unknown model should not be known")
	}
	if len(caps) != 0 {
		t.Errorf("unknown model capabilities = %v, want empty", caps.Names())
	}

	// Any requirement fails closed with a configurable message.
	err := r.Validate("acme", "private-model", "node-1", NewSet(CapabilityTools))
	if err == nil {
		t.Fatal("expected fail-closed error")
	}
	if !errors.Is(err, ErrUnsupportedCapability) {
		t.Errorf("error should wrap sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "unknown model") || !strings.Contains(err.Error(), "capabilities.<model>") {
		t.Errorf("fail-closed error should guide configuration: %v", err)
	}
	if !strings.Contains(err.Error(), `node "node-1"`) {
		t.Errorf("error should name the node: %v", err)
	}
}

func TestRegistryConfigOverrides(t *testing.T) {
	r := NewRegistry()
	warnings := r.ApplyConfig("acme", map[string][]string{
		"private-model": {"tools", "streaming", "bogus-capability"},
	})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "bogus-capability") {
		t.Errorf("malformed capability should produce a warning: %v", warnings)
	}

	caps, known := r.Model("acme", "private-model")
	if !known || !caps.Has(CapabilityTools) || !caps.Has(CapabilityStreaming) {
		t.Errorf("configured model = %v known=%v", caps.Names(), known)
	}
	if caps.Has(CapabilityStructuredOutput) {
		t.Errorf("unconfigured capabilities must not appear: %v", caps.Names())
	}

	// Configured model passes validation.
	if err := r.Validate("acme", "private-model", "", NewSet(CapabilityTools)); err != nil {
		t.Errorf("configured capability should validate: %v", err)
	}
	// ...and still fail closed for unconfigured ones.
	if err := r.Validate("acme", "private-model", "", NewSet(CapabilityReasoning)); err == nil {
		t.Error("unconfigured capability should fail closed")
	}
}

func TestRegistryConfigOverridesSnapshot(t *testing.T) {
	r := NewRegistry()
	// Override wins over the static snapshot: ollama gains tools.
	r.ApplyConfig("ollama", map[string][]string{"llama3.1:70b": {"tools", "structured_output"}})

	caps, known := r.Model("ollama", "llama3.1:70b")
	if !known || !caps.Has(CapabilityStructuredOutput) {
		t.Errorf("override should replace snapshot: %v known=%v", caps.Names(), known)
	}
}

func TestRegistryLongestPatternWins(t *testing.T) {
	r := NewRegistry()
	r.ApplyConfig("acme", map[string][]string{
		"model*":     {"tools"},
		"model-pro*": {"tools", "reasoning"},
	})

	if caps, _ := r.Model("acme", "model-basic"); !caps.Has(CapabilityTools) || caps.Has(CapabilityReasoning) {
		t.Errorf("model-basic = %v", caps.Names())
	}
	if caps, _ := r.Model("acme", "model-pro"); !caps.Has(CapabilityReasoning) {
		t.Errorf("model-pro should match the longest pattern: %v", caps.Names())
	}
}

func TestRegistryValidateSatisfied(t *testing.T) {
	r := NewRegistry()
	if err := r.Validate("openai", "gpt-4o", "", NewSet(CapabilityTools, CapabilityStructuredOutput)); err != nil {
		t.Errorf("satisfied requirements should pass: %v", err)
	}
	// No requirements: always passes, even for unknown models.
	if err := r.Validate("acme", "unknown", "", nil); err != nil {
		t.Errorf("empty requirements should pass: %v", err)
	}
}

func TestRegistryCaseInsensitive(t *testing.T) {
	r := NewRegistry()
	if caps, known := r.Model("OpenAI", "GPT-4O"); !known || !caps.Has(CapabilityTools) {
		t.Errorf("case-insensitive lookup failed: known=%v", known)
	}
}
