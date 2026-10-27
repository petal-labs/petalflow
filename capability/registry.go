package capability

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry resolves capability metadata for provider/model pairs.
//
// Resolution order: exact model entry → matching model pattern (entries
// ending in "*") → provider default → unknown model (empty set, fail
// closed). Entries come from the bundled static snapshot and configuration
// overrides; configuration wins over the snapshot.
//
// The registry also resolves per-model prices with the same resolution
// semantics (see pricing.go).
type Registry struct {
	mu sync.RWMutex

	// models maps "provider/model" (lowercased) to capabilities.
	models map[string]modelEntry
	// patterns holds model entries whose names end in "*", longest pattern
	// first, per provider.
	patterns map[string][]modelEntry
	// providers maps provider names to default capabilities.
	providers map[string]Set
	// prices and pricePatterns hold per-model prices.
	prices        map[string]priceEntry
	pricePatterns map[string][]priceEntry
}

type modelEntry struct {
	pattern      string
	capabilities Set
}

// NewRegistry returns a registry preloaded with the bundled static
// capability snapshot for known providers and models.
func NewRegistry() *Registry {
	r := &Registry{
		models:        make(map[string]modelEntry),
		patterns:      make(map[string][]modelEntry),
		providers:     make(map[string]Set),
		prices:        make(map[string]priceEntry),
		pricePatterns: make(map[string][]priceEntry),
	}
	r.applyStaticDefaults()
	r.applyStaticPrices()
	return r
}

// modelKey normalizes a provider/model pair into a registry key.
func modelKey(provider, model string) string {
	return strings.ToLower(provider) + "/" + strings.ToLower(strings.TrimSpace(model))
}

// ApplyConfig applies configuration overrides for one provider:
// model name (or "model*"-style prefix pattern) → capability strings.
// Malformed capability strings are skipped and reported as warnings;
// they never corrupt the registry. Overrides win over the static snapshot.
func (r *Registry) ApplyConfig(provider string, models map[string][]string) []string {
	if provider == "" || len(models) == 0 {
		return nil
	}
	var warnings []string

	// Parse without holding the lock, then apply mutations atomically.
	parsed := make([]struct {
		model string
		set   Set
	}, 0, len(models))
	for model, caps := range models {
		set := Set{}
		for _, raw := range caps {
			capability, ok := ParseCapability(raw)
			if !ok {
				warnings = append(warnings, fmt.Sprintf(
					"provider %q model %q: unknown capability %q ignored", provider, model, raw))
				continue
			}
			set.Add(capability)
		}
		parsed = append(parsed, struct {
			model string
			set   Set
		}{model, set})
	}

	r.mu.Lock()
	for _, p := range parsed {
		r.registerModelEntry(provider, p.model, p.set)
	}
	r.mu.Unlock()
	return warnings
}

// registerModelEntry stores a model entry, routing suffix patterns to the
// pattern list (longest first).
func (r *Registry) registerModelEntry(provider, model string, set Set) {
	key := modelKey(provider, model)
	entry := modelEntry{pattern: strings.ToLower(strings.TrimSpace(model)), capabilities: set}
	if strings.HasSuffix(entry.pattern, "*") {
		r.patterns[strings.ToLower(provider)] = append(r.patterns[strings.ToLower(provider)], entry)
		// Longest pattern first for most-specific matching.
		list := r.patterns[strings.ToLower(provider)]
		sort.Slice(list, func(i, j int) bool { return len(list[i].pattern) > len(list[j].pattern) })
		return
	}
	r.models[key] = entry
}

// Model resolves the capability set for a provider/model pair.
// known is false for models absent from the registry (fail closed).
func (r *Registry) Model(provider, model string) (capabilities Set, known bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if entry, ok := r.models[modelKey(provider, model)]; ok {
		return entry.capabilities, true
	}

	// Pattern match: longest pattern wins.
	providerKey := strings.ToLower(provider)
	lowerModel := strings.ToLower(strings.TrimSpace(model))
	for _, entry := range r.patterns[providerKey] {
		if strings.HasPrefix(lowerModel, strings.TrimSuffix(entry.pattern, "*")) {
			return entry.capabilities, true
		}
	}

	if defaults, ok := r.providers[strings.ToLower(provider)]; ok {
		return defaults, true
	}
	return Set{}, false
}

// Validate checks that a model supports all required capabilities.
// nodeID is optional context for the error message. Returns nil when the
// model satisfies the requirements (or when nothing is required).
func (r *Registry) Validate(provider, model, nodeID string, required Set) error {
	if len(required) == 0 {
		return nil
	}
	capabilities, known := r.Model(provider, model)
	missing := capabilities.Missing(required)
	if len(missing) == 0 {
		return nil
	}
	return &UnsupportedError{
		Provider:   provider,
		Model:      model,
		Node:       nodeID,
		Missing:    missing,
		KnownModel: known,
	}
}

// applyStaticDefaults loads the bundled conservative capability snapshot.
//
// This table is a best-effort snapshot, not ground truth: configuration
// overrides (ApplyConfig) always win, and unknown models fail closed.
func (r *Registry) applyStaticDefaults() {
	// Provider-level conservative defaults.
	r.providers["openai"] = NewSet(
		CapabilityTools, CapabilityStructuredOutput, CapabilityStreaming, CapabilityMultimodalInput)
	r.providers["anthropic"] = NewSet(
		CapabilityTools, CapabilityStreaming, CapabilityMultimodalInput)
	// Ollama models vary wildly: claim only streaming by default and
	// require explicit configuration for anything else.
	r.providers["ollama"] = NewSet(CapabilityStreaming)

	// OpenAI reasoning models.
	for _, model := range []string{"o1", "o1-pro", "o3", "o3-mini", "o4-mini"} {
		r.registerModelEntry("openai", model, NewSet(
			CapabilityReasoning, CapabilityStreaming, CapabilityStructuredOutput))
	}
	// OpenAI embeddings: embeddings only, no chat capabilities.
	for _, model := range []string{"text-embedding-3-small", "text-embedding-3-large", "text-embedding-ada-002"} {
		r.registerModelEntry("openai", model, NewSet(CapabilityEmbeddings))
	}

	// Anthropic reasoning (extended thinking) models; patterns cover
	// dated snapshots (e.g. claude-sonnet-4-20250514).
	for _, model := range []string{"claude-3-7-sonnet*", "claude-sonnet-4*", "claude-opus-4*"} {
		r.registerModelEntry("anthropic", model, NewSet(
			CapabilityTools, CapabilityStreaming, CapabilityMultimodalInput, CapabilityReasoning))
	}

	// Ollama models with tool support.
	for _, model := range []string{"llama3.1*", "llama3.2*", "llama3.3*", "qwen2.5*", "mistral-nemo*"} {
		r.registerModelEntry("ollama", model, NewSet(
			CapabilityTools, CapabilityStreaming))
	}
}
