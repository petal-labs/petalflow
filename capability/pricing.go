package capability

import (
	"fmt"
	"sort"
	"strings"
)

// Price is USD pricing per one million tokens for a model.
type Price struct {
	// InputPerMillion is the USD cost per 1M input (prompt) tokens.
	InputPerMillion float64
	// OutputPerMillion is the USD cost per 1M output (completion) tokens.
	OutputPerMillion float64
}

// Cost computes the USD cost of a token split.
func (p Price) Cost(inputTokens, outputTokens int) float64 {
	return (float64(inputTokens)/1_000_000)*p.InputPerMillion +
		(float64(outputTokens)/1_000_000)*p.OutputPerMillion
}

// ModelPrice is a resolved price with provenance. Known is false when the
// model's price is not in the registry: callers must treat cost as unknown
// rather than silently zero.
type ModelPrice struct {
	Price
	Known bool
}

// priceEntry is one registered price (exact or pattern).
type priceEntry struct {
	pattern string
	price   Price
}

// SetPrice registers the price for a provider/model (exact name or `*`
// prefix pattern). Later calls win. Negative prices are rejected as
// malformed (reported as warnings by the Apply helpers); zero is legal
// (free or locally-hosted models).
func (r *Registry) SetPrice(provider, model string, price Price) error {
	if price.InputPerMillion < 0 || price.OutputPerMillion < 0 {
		return fmt.Errorf("negative price for %s/%s", provider, model)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entry := priceEntry{pattern: strings.ToLower(strings.TrimSpace(model)), price: price}
	if strings.HasSuffix(entry.pattern, "*") {
		providerKey := strings.ToLower(provider)
		r.pricePatterns[providerKey] = append(r.pricePatterns[providerKey], entry)
		sort.Slice(r.pricePatterns[providerKey], func(i, j int) bool {
			return len(r.pricePatterns[providerKey][i].pattern) > len(r.pricePatterns[providerKey][j].pattern)
		})
		return nil
	}
	r.prices[modelKey(provider, model)] = entry
	return nil
}

// Price resolves the price for a provider/model pair, mirroring capability
// resolution: exact entry → longest matching pattern → unknown.
// Provider defaults intentionally carry no price: guessing a price for a
// chat model is silent mispricing. Known is false when unresolved.
func (r *Registry) Price(provider, model string) ModelPrice {
	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.prices[modelKey(provider, model)]; ok {
		return ModelPrice{Price: entry.price, Known: true}
	}

	providerKey := strings.ToLower(provider)
	lowerModel := strings.ToLower(strings.TrimSpace(model))
	for _, entry := range r.pricePatterns[providerKey] {
		if strings.HasPrefix(lowerModel, strings.TrimSuffix(entry.pattern, "*")) {
			return ModelPrice{Price: entry.price, Known: true}
		}
	}
	return ModelPrice{Known: false}
}

// ApplyCostConfig applies cost configuration overrides for one provider:
// model name (or `model*` prefix pattern) → price. Negative prices are
// malformed: they are skipped and reported as warnings, never registered —
// a silently wrong price is worse than an unknown one.
func (r *Registry) ApplyCostConfig(provider string, costs map[string]Price) []string {
	if provider == "" || len(costs) == 0 {
		return nil
	}
	var warnings []string

	models := make([]string, 0, len(costs))
	for model := range costs {
		models = append(models, model)
	}
	sort.Strings(models)

	for _, model := range models {
		price := costs[model]
		if price.InputPerMillion < 0 || price.OutputPerMillion < 0 {
			warnings = append(warnings, fmt.Sprintf(
				"provider %q model %q: negative price ignored (input=%.4f output=%.4f)",
				provider, model, price.InputPerMillion, price.OutputPerMillion))
			continue
		}
		if err := r.SetPrice(provider, model, price); err != nil {
			warnings = append(warnings, err.Error())
		}
	}
	return warnings
}

// applyStaticPrices loads the bundled price snapshot for known models.
// USD per 1M tokens; a best-effort snapshot, overridable via configuration.
func (r *Registry) applyStaticPrices() {
	static := map[string]Price{
		// OpenAI chat models.
		"openai/gpt-4o":      {InputPerMillion: 2.50, OutputPerMillion: 10.00},
		"openai/gpt-4o-mini": {InputPerMillion: 0.15, OutputPerMillion: 0.60},
		"openai/gpt-4.1":     {InputPerMillion: 2.00, OutputPerMillion: 8.00},
		"openai/o1":          {InputPerMillion: 15.00, OutputPerMillion: 60.00},
		"openai/o1-mini":     {InputPerMillion: 1.10, OutputPerMillion: 4.40},
		"openai/o3":          {InputPerMillion: 2.00, OutputPerMillion: 8.00},
		"openai/o3-mini":     {InputPerMillion: 1.10, OutputPerMillion: 4.40},
		"openai/o4-mini":     {InputPerMillion: 1.10, OutputPerMillion: 4.40},

		// OpenAI embeddings.
		"openai/text-embedding-3-small": {InputPerMillion: 0.02},
		"openai/text-embedding-3-large": {InputPerMillion: 0.13},
		"openai/text-embedding-ada-002": {InputPerMillion: 0.10},
	}

	for key, price := range static {
		parts := strings.SplitN(key, "/", 2)
		if err := r.SetPrice(parts[0], parts[1], price); err != nil {
			// Static table is trusted; a programming error would surface here.
			panic(fmt.Sprintf("capability: static price table: %v", err))
		}
	}

	// Anthropic models via patterns covering dated snapshots.
	for _, pattern := range []string{"claude-3-5-sonnet*", "claude-sonnet-4*", "claude-opus-4*"} {
		_ = r.SetPrice("anthropic", pattern, Price{InputPerMillion: 3.00, OutputPerMillion: 15.00})
	}
	for _, pattern := range []string{"claude-3-5-haiku*", "claude-haiku-4*"} {
		_ = r.SetPrice("anthropic", pattern, Price{InputPerMillion: 0.80, OutputPerMillion: 4.00})
	}
	for _, pattern := range []string{"claude-opus-3*"} {
		_ = r.SetPrice("anthropic", pattern, Price{InputPerMillion: 15.00, OutputPerMillion: 75.00})
	}
}
