package llmprovider

import (
	"strings"

	"github.com/petal-labs/petalflow/capability"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/memory"
)

// CostEstimator produces preflight token and cost estimates for LLM
// requests, using per-model prices from a capability registry and a
// pluggable token counter (default: the ~4 chars/token heuristic from the
// memory package; supply a model-specific tokenizer for precision).
type CostEstimator struct {
	// Registry resolves per-model prices. nil means costs are unknown.
	Registry *capability.Registry
	// Counter estimates tokens per string. nil uses memory.EstimateTokens.
	Counter memory.TokenCounter
}

// RequestEstimate is the preflight estimate for one request. The estimate
// covers the input (prompt) side: output size is unknowable before the
// call. Known is false when the model's price is not registered — cost is
// then unknown, not zero.
type RequestEstimate struct {
	// InputTokens is the estimated prompt token count.
	InputTokens int
	// CostUSD is the estimated input-side cost (zero when Known is false).
	CostUSD float64
	// Known reports whether a price was resolved for the model.
	Known bool
}

// Estimate computes the preflight estimate for a request on the given
// provider.
func (e CostEstimator) Estimate(provider string, req core.LLMRequest) RequestEstimate {
	tokens := e.EstimateRequestTokens(req)

	estimate := RequestEstimate{InputTokens: tokens}
	if e.Registry == nil {
		return estimate
	}
	price := e.Registry.Price(provider, req.Model)
	if !price.Known {
		return estimate
	}
	estimate.Known = true
	estimate.CostUSD = price.Cost(tokens, 0)
	return estimate
}

// EstimateRequestTokens sums the estimated tokens of every prompt-side
// string: system prompt, instructions, input text, and message
// role/name/content. Streaming and non-streaming requests estimate
// identically.
func (e CostEstimator) EstimateRequestTokens(req core.LLMRequest) int {
	counter := e.Counter
	if counter == nil {
		counter = memory.EstimateTokens
	}

	total := 0
	total += counter(req.System)
	total += counter(req.Instructions)
	total += counter(req.InputText)
	for _, message := range req.Messages {
		total += counter(message.Role)
		if message.Name != "" {
			total += counter(message.Name)
		}
		total += counter(message.Content)
		for _, part := range message.Parts {
			// Textual parts count; binary parts (images/files) are
			// represented by their media type at minimum.
			if part.Text != "" {
				total += counter(part.Text)
				continue
			}
			if part.Type != "" && part.Type != "text" && part.Type != "input_text" {
				total += counter(part.Type)
			}
		}
	}
	return total
}

// ActualCost resolves the actual cost of a completed call: a
// provider-reported cost wins; otherwise the price registry computes it
// from actual usage. Known is false when neither is available — cost is
// unknown, never silently zero.
func (e CostEstimator) ActualCost(provider, model string, usage core.LLMTokenUsage) (float64, bool) {
	if usage.CostUSD > 0 {
		return usage.CostUSD, true
	}
	if e.Registry == nil {
		return 0, false
	}
	price := e.Registry.Price(provider, model)
	if !price.Known {
		return 0, false
	}
	return price.Cost(usage.InputTokens, usage.OutputTokens), true
}

// estimateEventFields returns the llm.call payload fields for a request
// estimate. Returns nil when the request has no text to estimate (nothing
// meaningful to report).
func (e CostEstimator) estimateEventFields(provider string, req core.LLMRequest) map[string]any {
	if strings.TrimSpace(req.InputText) == "" && req.System == "" &&
		req.Instructions == "" && len(req.Messages) == 0 {
		return nil
	}

	estimate := e.Estimate(provider, req)
	fields := map[string]any{
		"estimated_input_tokens": estimate.InputTokens,
	}
	if estimate.Known {
		fields["estimated_cost_usd"] = estimate.CostUSD
	} else {
		fields["cost_unknown"] = true
	}
	return fields
}

// actualEventFields returns the llm.response payload fields for actual
// usage: consistent names with the estimate fields, so tooling can diff
// estimate vs. actual per call.
func (e CostEstimator) actualEventFields(provider, model string, usage core.LLMTokenUsage) map[string]any {
	fields := map[string]any{
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
		"total_tokens":  usage.TotalTokens,
	}
	cost, known := e.ActualCost(provider, model, usage)
	if known {
		fields["cost_usd"] = cost
	} else {
		fields["cost_unknown"] = true
	}
	return fields
}
