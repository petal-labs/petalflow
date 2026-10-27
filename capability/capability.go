// Package capability defines the provider/model capability model for
// PetalFlow: which models support tools, structured output, reasoning,
// multimodal input, embeddings, and streaming. A registry resolves
// capability metadata (static snapshot → configuration overrides → provider
// defaults → fail-closed for unknown models) so workflows can preflight
// model requirements before any provider request is spent.
//
// The bundled metadata is a conservative snapshot, not ground truth: it is
// overridable per provider/model through configuration, and unknown models
// fail closed on required capabilities with guidance on how to configure
// them.
package capability

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Capability identifies a model feature a workflow may require.
type Capability string

const (
	// CapabilityTools is native tool/function calling.
	CapabilityTools Capability = "tools"

	// CapabilityStructuredOutput is native structured output (JSON schema
	// constrained decoding or equivalent).
	CapabilityStructuredOutput Capability = "structured_output"

	// CapabilityReasoning is explicit reasoning/extended thinking.
	CapabilityReasoning Capability = "reasoning"

	// CapabilityMultimodalInput is non-text input (images, files, audio).
	CapabilityMultimodalInput Capability = "multimodal_input"

	// CapabilityEmbeddings is embedding generation.
	CapabilityEmbeddings Capability = "embeddings"

	// CapabilityStreaming is incremental response streaming.
	CapabilityStreaming Capability = "streaming"
)

// ParseCapability converts a configuration string to a Capability,
// accepting the canonical form and common aliases. ok is false for unknown
// strings (malformed metadata): callers must skip and warn, never guess.
func ParseCapability(s string) (Capability, bool) {
	normalized := strings.ToLower(strings.TrimSpace(s))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "tools", "tool_calling", "function_calling":
		return CapabilityTools, true
	case "structured_output", "json_schema", "json_mode":
		return CapabilityStructuredOutput, true
	case "reasoning", "extended_thinking", "thinking":
		return CapabilityReasoning, true
	case "multimodal_input", "multimodal", "vision", "images":
		return CapabilityMultimodalInput, true
	case "embeddings", "embedding":
		return CapabilityEmbeddings, true
	case "streaming", "stream":
		return CapabilityStreaming, true
	default:
		return "", false
	}
}

// Set is a collection of capabilities.
type Set map[Capability]struct{}

// NewSet builds a set from capabilities.
func NewSet(caps ...Capability) Set {
	out := make(Set, len(caps))
	for _, c := range caps {
		out[c] = struct{}{}
	}
	return out
}

// Has reports whether the set contains the capability.
func (s Set) Has(c Capability) bool {
	_, ok := s[c]
	return s != nil && ok
}

// Add inserts a capability.
func (s Set) Add(c Capability) {
	if s == nil {
		s = Set{}
	}
	s[c] = struct{}{}
}

// Missing returns the capabilities in required that the set lacks, sorted
// by name for stable messages.
func (s Set) Missing(required Set) []Capability {
	var missing []Capability
	for c := range required {
		if !s.Has(c) {
			missing = append(missing, c)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	return missing
}

// Names returns the sorted capability names.
func (s Set) Names() []string {
	out := make([]string, 0, len(s))
	for c := range s {
		out = append(out, string(c))
	}
	sort.Strings(out)
	return out
}

// String renders the set for messages.
func (s Set) String() string {
	return strings.Join(s.Names(), ", ")
}

// ErrUnsupportedCapability signals that a model lacks capabilities a
// workflow requires. It is diagnosable before any provider request is made.
var ErrUnsupportedCapability = errors.New("model does not support required capabilities")

// UnsupportedError names the model, the node that required it, and the
// missing capabilities.
type UnsupportedError struct {
	Provider string
	Model    string
	Node     string
	// Missing lists the required capabilities the model lacks.
	Missing []Capability
	// KnownModel is false when the model was not in the registry at all
	// (fail-closed); the message then explains how to configure it.
	KnownModel bool
}

// Error implements error.
func (e *UnsupportedError) Error() string {
	missing := Set{}
	for _, m := range e.Missing {
		missing.Add(m)
	}
	target := e.Provider + "/" + e.Model
	if e.Node != "" {
		target = fmt.Sprintf("node %q (model %s)", e.Node, target)
	}
	if !e.KnownModel {
		return strings.TrimSpace(fmt.Sprintf(
			"%s: %s: unknown model, failing closed on required capabilities [%s]; "+
				"declare its capabilities via provider configuration (providers.<name>.capabilities.<model>)",
			ErrUnsupportedCapability, target, missing.String()))
	}
	return strings.TrimSpace(fmt.Sprintf(
		"%s: %s lacks [%s]", ErrUnsupportedCapability, target, missing.String()))
}

// Unwrap exposes the sentinel.
func (e *UnsupportedError) Unwrap() error {
	return ErrUnsupportedCapability
}
