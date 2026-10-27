package capability

import (
	"errors"
	"strings"
	"testing"
)

func TestParseCapability(t *testing.T) {
	tests := []struct {
		in   string
		want Capability
		ok   bool
	}{
		{"tools", CapabilityTools, true},
		{"tool-calling", CapabilityTools, true},
		{"function_calling", CapabilityTools, true},
		{"structured-output", CapabilityStructuredOutput, true},
		{"json_mode", CapabilityStructuredOutput, true},
		{"reasoning", CapabilityReasoning, true},
		{"multimodal", CapabilityMultimodalInput, true},
		{"embeddings", CapabilityEmbeddings, true},
		{"stream", CapabilityStreaming, true},
		{"  Tools  ", CapabilityTools, true},
		{"bogus", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseCapability(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("ParseCapability(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSetMissing(t *testing.T) {
	have := NewSet(CapabilityTools, CapabilityStreaming)
	required := NewSet(CapabilityTools, CapabilityReasoning, CapabilityEmbeddings)

	missing := have.Missing(required)
	if len(missing) != 2 {
		t.Fatalf("missing = %v, want 2 entries", missing)
	}
	// Sorted for stable messages.
	if missing[0] != CapabilityEmbeddings || missing[1] != CapabilityReasoning {
		t.Errorf("missing = %v, want sorted [embeddings reasoning]", missing)
	}
}

func TestUnsupportedErrorMessages(t *testing.T) {
	err := &UnsupportedError{
		Provider:   "openai",
		Model:      "text-embedding-3-small",
		Node:       "answer",
		Missing:    []Capability{CapabilityTools, CapabilityStructuredOutput},
		KnownModel: true,
	}
	msg := err.Error()
	for _, want := range []string{`node "answer"`, "openai/text-embedding-3-small", "structured_output, tools"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
	if !errors.Is(err, ErrUnsupportedCapability) {
		t.Error("error should wrap the sentinel")
	}

	unknown := &UnsupportedError{
		Provider:   "acme",
		Model:      "private-model",
		Missing:    []Capability{CapabilityTools},
		KnownModel: false,
	}
	umsg := unknown.Error()
	for _, want := range []string{"unknown model", "failing closed", "providers.<name>.capabilities.<model>"} {
		if !strings.Contains(umsg, want) {
			t.Errorf("unknown-model message %q missing %q", umsg, want)
		}
	}
}

func TestSetStringAndNames(t *testing.T) {
	set := NewSet(CapabilityStreaming, CapabilityTools)
	names := set.Names()
	if len(names) != 2 || names[0] != "streaming" || names[1] != "tools" {
		t.Errorf("Names = %v, want sorted", names)
	}
	if !strings.Contains(set.String(), "tools") {
		t.Errorf("String = %q", set.String())
	}
}
