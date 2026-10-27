package capability

import (
	"testing"
)

func TestPriceSnapshot(t *testing.T) {
	r := NewRegistry()

	// Known chat model.
	price := r.Price("openai", "gpt-4o")
	if !price.Known {
		t.Fatal("gpt-4o price should be known")
	}
	if price.InputPerMillion != 2.50 || price.OutputPerMillion != 10.00 {
		t.Errorf("gpt-4o price = %+v", price.Price)
	}

	// Anthropic pattern covers dated snapshots.
	price = r.Price("anthropic", "claude-sonnet-4-20250514")
	if !price.Known || price.InputPerMillion != 3.00 {
		t.Errorf("claude-sonnet-4 dated snapshot = %+v known=%v", price.Price, price.Known)
	}
}

func TestPriceUnknownModel(t *testing.T) {
	r := NewRegistry()
	price := r.Price("acme", "private-model")
	if price.Known {
		t.Fatal("unknown model price should not be known")
	}
	// Unknown cost is explicitly unknown, never silently zero-priced.
	if price.Cost(1000, 1000) != 0 {
		t.Error("unknown price cost is zero but callers must gate on Known")
	}
}

func TestPriceConfigOverrideWins(t *testing.T) {
	r := NewRegistry()
	warnings := r.ApplyCostConfig("openai", map[string]Price{
		"gpt-4o": {InputPerMillion: 1.00, OutputPerMillion: 2.00},
	})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}

	price := r.Price("openai", "gpt-4o")
	if !price.Known || price.InputPerMillion != 1.00 || price.OutputPerMillion != 2.00 {
		t.Errorf("override should win: %+v known=%v", price.Price, price.Known)
	}
}

func TestPriceNegativeRejectedWithWarning(t *testing.T) {
	r := NewRegistry()
	warnings := r.ApplyCostConfig("openai", map[string]Price{
		"gpt-4o":     {InputPerMillion: -1},
		"o3":         {OutputPerMillion: -0.5},
		"free-model": {InputPerMillion: 0, OutputPerMillion: 0},
	})
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want 2 (negative prices)", warnings)
	}

	// Negative entries are not registered: the snapshot price remains.
	if price := r.Price("openai", "gpt-4o"); !price.Known ||
		price.InputPerMillion != 2.50 || price.OutputPerMillion != 10.00 {
		t.Errorf("rejected override should leave snapshot price: %+v", price)
	}

	// Zero price is legal (free/local models).
	price := r.Price("openai", "free-model")
	if !price.Known || price.InputPerMillion != 0 || price.OutputPerMillion != 0 {
		t.Errorf("zero price should be known and free: %+v known=%v", price.Price, price.Known)
	}
}

func TestPricePatternMatching(t *testing.T) {
	r := NewRegistry()
	price := r.Price("anthropic", "claude-sonnet-4-6")
	if !price.Known {
		t.Fatal("pattern price should resolve")
	}
	if cost := price.Cost(1_000_000, 1_000_000); cost != 18.00 {
		t.Errorf("cost = %v, want 18.00 (3 + 15)", cost)
	}
}

func TestPriceExactBeatsPattern(t *testing.T) {
	r := NewRegistry()
	if err := r.SetPrice("anthropic", "claude-sonnet-4-6", Price{InputPerMillion: 1, OutputPerMillion: 2}); err != nil {
		t.Fatalf("SetPrice: %v", err)
	}

	price := r.Price("anthropic", "claude-sonnet-4-6")
	if price.InputPerMillion != 1.00 {
		t.Errorf("exact price should beat pattern: %+v", price.Price)
	}
}

func TestPriceCost(t *testing.T) {
	price := Price{InputPerMillion: 3.00, OutputPerMillion: 15.00}
	if got := price.Cost(1_000_000, 2_000_000); got != 33.00 {
		t.Errorf("Cost = %v, want 33.00", got)
	}
	if got := price.Cost(0, 0); got != 0 {
		t.Errorf("Cost of zero tokens = %v", got)
	}
}
