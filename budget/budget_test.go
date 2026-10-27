package budget

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestLedgerUnbounded(t *testing.T) {
	ledger, err := NewLedger(Ceiling{})
	if err != nil {
		t.Fatalf("NewLedger: %v", err)
	}
	// Unbounded: nothing is ever exceeded, everything records.
	if err := ledger.Check("run", 1_000_000, 100); err != nil {
		t.Errorf("Check with unbounded ceiling: %v", err)
	}
	if err := ledger.Record(1_000_000, 100); err != nil {
		t.Errorf("Record: %v", err)
	}
	if spent := ledger.Spent(); spent.TotalTokens != 1_000_000 || spent.CostUSD != 100 {
		t.Errorf("spent = %+v", spent)
	}
}

func TestLedgerMalformedCeiling(t *testing.T) {
	tests := []struct {
		name    string
		ceiling Ceiling
	}{
		{"negative tokens", Ceiling{MaxTotalTokens: -1}},
		{"negative cost", Ceiling{MaxCostUSD: -0.5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewLedger(tt.ceiling)
			if !errors.Is(err, ErrMalformedBudget) {
				t.Errorf("error should wrap ErrMalformedBudget, got %v", err)
			}
		})
	}
}

func TestLedgerCheckTokenBreach(t *testing.T) {
	ledger, _ := NewLedger(Ceiling{MaxTotalTokens: 1000})

	// Within budget: passes.
	if err := ledger.Check("run", 600, 0); err != nil {
		t.Fatalf("Check within budget: %v", err)
	}
	ledger.Record(600, 0)

	// 400 spent, 700 estimate: projected 1100 > 1000 → breach.
	err := ledger.Check("run", 700, 0)
	if err == nil {
		t.Fatal("expected breach")
	}
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Errorf("error should wrap ErrBudgetExceeded: %v", err)
	}

	var exceeded *BudgetExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("error should be *BudgetExceededError: %T", err)
	}
	if exceeded.Gate != "total_tokens" || exceeded.Scope != "run" {
		t.Errorf("attribution = %+v", exceeded)
	}
	if exceeded.Projected != 1300 || exceeded.Ceiling != 1000 {
		t.Errorf("projected/ceiling = %v/%v", exceeded.Projected, exceeded.Ceiling)
	}
	if !strings.Contains(err.Error(), "total_tokens") {
		t.Errorf("message should name the gate: %v", err)
	}
}

func TestLedgerCheckCostBreach(t *testing.T) {
	ledger, _ := NewLedger(Ceiling{MaxCostUSD: 1.0})

	if err := ledger.Check("run", 0, 0.9); err != nil {
		t.Fatalf("Check within budget: %v", err)
	}
	ledger.Record(0, 0.9)

	// 0.9 spent, 0.2 estimated: projected 1.1 > 1.0.
	err := ledger.Check("run", 0, 0.2)
	if err == nil || !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected cost breach, got %v", err)
	}
	var exceeded *BudgetExceededError
	errors.As(err, &exceeded)
	if exceeded.Gate != "cost_usd" {
		t.Errorf("gate = %q, want cost_usd", exceeded.Gate)
	}
	if !strings.Contains(err.Error(), "cost_usd projected 1.1000 exceeds ceiling 1.0000") {
		t.Errorf("message should attribute: %v", err)
	}
}

func TestLedgerCostUnknownSkipsCostGate(t *testing.T) {
	// Cost-unknown models (#206) estimate zero cost: the token gate still
	// applies, the cost gate cannot fire on unknowns.
	ledger, _ := NewLedger(Ceiling{MaxCostUSD: 0.5, MaxTotalTokens: 10000})

	if err := ledger.Check("run", 500, 0); err != nil {
		t.Errorf("unknown cost should not trip the cost gate: %v", err)
	}
	ledger.Record(500, 0) // actual cost reported as 0/unknown

	if err := ledger.Check("run", 500, 0); err != nil {
		t.Errorf("token gate satisfied: %v", err)
	}

	// Token breach still fires with unknown cost.
	if err := ledger.Check("run", 20000, 0); err == nil {
		t.Error("token breach should fire")
	}
}

func TestLedgerRecordNegativeRejected(t *testing.T) {
	ledger, _ := NewLedger(Ceiling{})
	if err := ledger.Record(-1, 0); !errors.Is(err, ErrMalformedBudget) {
		t.Errorf("negative tokens: %v", err)
	}
	if err := ledger.Record(0, -0.1); !errors.Is(err, ErrMalformedBudget) {
		t.Errorf("negative cost: %v", err)
	}
}

func TestLedgerActualCanExceedCeilingAfterDispatch(t *testing.T) {
	// Enforcement happens pre-dispatch; actuals recorded after can exceed
	// the ceiling (the spend already happened).
	ledger, _ := NewLedger(Ceiling{MaxTotalTokens: 100})

	if err := ledger.Check("run", 50, 0); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if err := ledger.Record(500, 0); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if spent := ledger.Spent(); spent.TotalTokens != 500 {
		t.Errorf("spent = %d, want 500", spent.TotalTokens)
	}
	// Everything after is rejected.
	if err := ledger.Check("run", 1, 0); err == nil {
		t.Error("post-breach checks should fail")
	}
}

func TestLedgerConcurrentAccess(t *testing.T) {
	// Parallel branches drawing on one ledger: the ceiling cannot be
	// bypassed (run with -race).
	ledger, _ := NewLedger(Ceiling{MaxTotalTokens: 1000})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ledger.Check("run", 100, 0); err != nil {
				return
			}
			_ = ledger.Record(100, 0)
		}()
	}
	wg.Wait()

	if spent := ledger.Spent().TotalTokens; spent > 1000 {
		t.Errorf("spent = %d, exceeds ceiling 1000", spent)
	}
}

func TestLedgerContextPropagation(t *testing.T) {
	ledger, _ := NewLedger(Ceiling{MaxTotalTokens: 10})
	ctx := ContextWithLedger(context.Background(), ledger)

	got := LedgerFromContext(ctx)
	if got != ledger {
		t.Fatal("ledger not propagated through context")
	}
	if LedgerFromContext(context.Background()) != nil {
		t.Error("no ledger should resolve to nil")
	}

	// A *nil ledger stored explicitly still reads as nil.
	ctxNil := ContextWithLedger(context.Background(), nil)
	if LedgerFromContext(ctxNil) != nil {
		t.Error("explicit nil ledger should read as nil")
	}
}
