package llmprovider

import (
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/capability"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
)

func testEstimator() CostEstimator {
	return CostEstimator{Registry: capability.NewRegistry()}
}

func TestEstimateRequestTokens(t *testing.T) {
	estimator := testEstimator()

	// ~4 chars/token heuristic: 400 chars ≈ 100 tokens.
	req := core.LLMRequest{
		Model:     "gpt-4o",
		System:    strings.Repeat("a", 100),
		InputText: strings.Repeat("b", 200),
		Messages: []core.LLMMessage{
			{Role: strings.Repeat("c", 20), Content: strings.Repeat("d", 80)},
		},
	}
	// (100+200+20+80)/4 = 100
	if got := estimator.EstimateRequestTokens(req); got != 100 {
		t.Errorf("EstimateRequestTokens = %d, want 100", got)
	}

	if got := estimator.EstimateRequestTokens(core.LLMRequest{}); got != 0 {
		t.Errorf("empty request = %d tokens, want 0", got)
	}
}

func TestEstimateKnownModel(t *testing.T) {
	estimator := testEstimator()
	req := core.LLMRequest{Model: "gpt-4o", InputText: strings.Repeat("a", 4000)}

	estimate := estimator.Estimate("openai", req)
	if !estimate.Known {
		t.Fatal("gpt-4o estimate should be known")
	}
	if estimate.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", estimate.InputTokens)
	}
	// Input-side only: 1000 tokens × $2.50/1M = $0.0025.
	if estimate.CostUSD != 0.0025 {
		t.Errorf("CostUSD = %v, want 0.0025", estimate.CostUSD)
	}
}

func TestEstimateUnknownModelIsExplicitlyUnknown(t *testing.T) {
	estimator := testEstimator()
	req := core.LLMRequest{Model: "private-model", InputText: strings.Repeat("a", 4000)}

	estimate := estimator.Estimate("acme", req)
	if estimate.Known {
		t.Fatal("unknown model cost should not be known")
	}
	if estimate.CostUSD != 0 {
		t.Errorf("unknown cost must not be silently priced: %v", estimate.CostUSD)
	}
}

func TestEstimateRequestTokensCustomCounter(t *testing.T) {
	estimator := CostEstimator{Counter: func(s string) int { return len([]rune(s)) }}
	req := core.LLMRequest{Model: "m", InputText: "four"}
	if got := estimator.EstimateRequestTokens(req); got != 4 {
		t.Errorf("custom counter = %d, want 4", got)
	}
}

func TestActualCostProviderReportedWins(t *testing.T) {
	estimator := testEstimator()
	cost, known := estimator.ActualCost("openai", "gpt-4o", core.LLMTokenUsage{
		InputTokens: 1000, OutputTokens: 1000, CostUSD: 99.0,
	})
	if !known || cost != 99.0 {
		t.Errorf("provider-reported cost should win: %v known=%v", cost, known)
	}
}

func TestActualCostComputedFromPrice(t *testing.T) {
	estimator := testEstimator()
	cost, known := estimator.ActualCost("openai", "gpt-4o", core.LLMTokenUsage{
		InputTokens: 1_000_000, OutputTokens: 1_000_000,
	})
	if !known || cost != 12.50 {
		t.Errorf("computed cost = %v known=%v, want 12.50", cost, known)
	}
}

func TestActualCostUnknown(t *testing.T) {
	estimator := testEstimator()
	_, known := estimator.ActualCost("acme", "private-model", core.LLMTokenUsage{TotalTokens: 100})
	if known {
		t.Error("unknown price should make cost explicitly unknown")
	}
}

func TestEstimateAndActualEventFields(t *testing.T) {
	estimator := testEstimator()

	// llm.call fields: estimate only.
	callFields := estimator.estimateEventFields("openai", core.LLMRequest{
		Model:     "gpt-4o",
		InputText: strings.Repeat("a", 4000),
	})
	if callFields["estimated_input_tokens"] != 1000 {
		t.Errorf("estimated_input_tokens = %v", callFields["estimated_input_tokens"])
	}
	if callFields["estimated_cost_usd"] != 0.0025 {
		t.Errorf("estimated_cost_usd = %v", callFields["estimated_cost_usd"])
	}
	if _, has := callFields["input_tokens"]; has {
		t.Error("call event must not carry actual usage")
	}

	// llm.response fields: actual usage, computed cost, consistent names.
	responseFields := estimator.actualEventFields("openai", "gpt-4o", core.LLMTokenUsage{
		InputTokens: 2000, OutputTokens: 500, TotalTokens: 2500,
	})
	if responseFields["input_tokens"] != 2000 || responseFields["total_tokens"] != 2500 {
		t.Errorf("response fields = %v", responseFields)
	}
	if _, has := responseFields["cost_usd"]; !has {
		t.Error("known price should produce cost_usd")
	}
	if _, has := responseFields["estimated_input_tokens"]; has {
		t.Error("response event must not carry estimates")
	}
}

func TestEventFieldsCostUnknown(t *testing.T) {
	estimator := testEstimator()

	callFields := estimator.estimateEventFields("acme", core.LLMRequest{
		Model: "private-model", InputText: "hi",
	})
	if callFields["cost_unknown"] != true {
		t.Errorf("unknown price should mark cost_unknown on call: %v", callFields)
	}
	if _, has := callFields["estimated_cost_usd"]; has {
		t.Error("unknown price must not emit estimated_cost_usd")
	}

	responseFields := estimator.actualEventFields("acme", "private-model", core.LLMTokenUsage{TotalTokens: 10})
	if responseFields["cost_unknown"] != true {
		t.Errorf("unknown price should mark cost_unknown on response: %v", responseFields)
	}
	if _, has := responseFields["cost_usd"]; has {
		t.Error("unknown price must not emit cost_usd")
	}
}

func TestEstimateEventFieldsSkipsEmptyRequests(t *testing.T) {
	estimator := testEstimator()
	if fields := estimator.estimateEventFields("openai", core.LLMRequest{Model: "gpt-4o"}); fields != nil {
		t.Errorf("empty request should produce no estimate fields, got %v", fields)
	}
}

func TestNewCostEstimatorFromConfig(t *testing.T) {
	estimator := NewCostEstimator("acme", hydrate.ProviderConfig{
		Costs: map[string]hydrate.ModelCost{
			"private-model": {InputPerMillion: 5.0, OutputPerMillion: 20.0},
		},
	})

	estimate := estimator.Estimate("acme", core.LLMRequest{
		Model: "private-model", InputText: strings.Repeat("a", 2_000_000),
	})
	// 500k tokens × $5/1M = $2.50 (input side only).
	if !estimate.Known || estimate.CostUSD != 2.5 {
		t.Errorf("config-driven estimate = %+v, want known, 2.5", estimate)
	}

	// Capability overrides flow through the same registry.
	estimator = NewCostEstimator("acme", hydrate.ProviderConfig{
		Costs: map[string]hydrate.ModelCost{"m": {}},
		Capabilities: map[string][]string{
			"m": {"tools", "structured_output", "streaming", "reasoning", "multimodal_input", "embeddings"},
		},
	})
	if caps, known := estimator.Registry.Model("acme", "m"); !known || len(caps) != 6 {
		t.Errorf("capabilities through cost-config registry: %v known=%v", caps.Names(), known)
	}
}

func TestNewCostEstimatorWithCapabilityWarnings(t *testing.T) {
	registry, warnings := NewCapabilityRegistry("acme", hydrate.ProviderConfig{
		Costs:        map[string]hydrate.ModelCost{"m": {InputPerMillion: -1}},
		Capabilities: map[string][]string{"m": {"bogus"}},
	})
	if len(warnings) != 2 {
		t.Errorf("warnings = %v, want malformed capability + negative price", warnings)
	}
	if registry == nil {
		t.Fatal("registry should still build")
	}
}
