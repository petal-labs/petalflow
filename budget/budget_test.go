package budget

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
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

// TestLedgerConcurrentAccess is a deterministic stress test: parallel
// branches drawing on one ledger can never push committed spend above
// the ceiling because pre-dispatch holds are granted atomically (run
// with -race, repeatedly in CI).
func TestLedgerConcurrentAccess(t *testing.T) {
	ledger, _ := NewLedger(Ceiling{MaxTotalTokens: 1000})

	const (
		goroutines   = 50
		estimate     = 100
		wantGranted  = 10 // checks to spend exactly the ceiling
		wantRejected = goroutines - wantGranted
	)

	var (
		granted  int64
		rejected int64
		wg       sync.WaitGroup
	)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hold, err := ledger.Reserve("run", estimate, 0)
			if err != nil {
				if !errors.Is(err, ErrBudgetExceeded) {
					t.Errorf("unexpected rejection error = %v", err)
				}
				atomic.AddInt64(&rejected, 1)
				return
			}
			if err := hold.Commit(estimate, 0); err != nil {
				t.Errorf("Commit on granted hold: %v", err)
				atomic.AddInt64(&rejected, 1)
				return
			}
			atomic.AddInt64(&granted, 1)
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&granted); int(got) != wantGranted {
		t.Errorf("granted = %d, want %d", got, wantGranted)
	}
	if got := atomic.LoadInt64(&rejected); int(got) != wantRejected {
		t.Errorf("rejected = %d, want %d", got, wantRejected)
	}
	if reserved := ledger.Reserved(); reserved.TotalTokens != 0 || reserved.CostUSD != 0 {
		t.Errorf("reservations must all settle: reserved = %+v", reserved)
	}
	if spent := ledger.Spent().TotalTokens; spent > 1000 {
		t.Errorf("spent = %d, exceeds ceiling 1000", spent)
	}
	if spent := ledger.Spent().TotalTokens; spent != wantGranted*estimate {
		t.Errorf("spent = %d, want exactly %d", spent, wantGranted*estimate)
	}
}

// TestLedgerReservationAtomicity pins the fail-forecast semantics under
// heavy contention: holds serialize commits so the ceiling stays exact.
func TestLedgerReservationAtomicity(t *testing.T) {
	ledger, _ := NewLedger(Ceiling{MaxTotalTokens: 500})

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hold, err := ledger.Reserve("run", 7, 0)
			if err != nil {
				return
			}
			_ = hold.Commit(7, 0)
		}()
	}
	wg.Wait()

	if spent := ledger.Spent().TotalTokens; spent%7 != 0 {
		t.Fatalf("partial hold: spent = %d", spent)
	}
	if spent := ledger.Spent().TotalTokens; spent > 500 {
		t.Errorf("spent = %d exceeds ceiling 500", spent)
	}
}

// TestLedgerReleaseRegrants: abandoned dispatches free their holds, so
// a released reservation does not permanently drain the ledger.
func TestLedgerReleaseRegrants(t *testing.T) {
	ledger, _ := NewLedger(Ceiling{MaxTotalTokens: 100})

	first, err := ledger.Reserve("run", 100, 0)
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if _, err := ledger.Reserve("run", 1, 0); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("contended reserve should be rejected, got %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// The freed hold is available again.
	if _, err := ledger.Reserve("run", 100, 0); err != nil {
		t.Fatalf("release must regrant: %v", err)
	}
	if reserved := ledger.Reserved().TotalTokens; reserved != 100 {
		t.Fatalf("reserved = %d, want 100", reserved)
	}

}

// TestLedgerCostDimensionGate covers the cost gate across reservations.
func TestLedgerCostDimensionGate(t *testing.T) {
	ledger, _ := NewLedger(Ceiling{MaxCostUSD: 10})

	hold, err := ledger.Reserve("run", 0, 4)
	if err != nil {
		t.Fatalf("reserve cost: %v", err)
	}
	if _, err := ledger.Reserve("run", 0, 7); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("cost projection must include holds, got %v", err)
	}

	if err := hold.Commit(0, 3.25); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if actual := ledger.Spent().CostUSD; actual != 3.25 {
		t.Fatalf("spent cost = %.4f, want 3.25", actual)
	}
}

// TestLedgerReservationSettleSemantics: one reservation settles exactly
// once; hold accounting reconciles on both paths.
func TestLedgerReservationSettleSemantics(t *testing.T) {
	ledger, _ := NewLedger(Ceiling{MaxTotalTokens: 100})

	// Double commit.
	commitHold, err := ledger.Reserve("run", 10, 0)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := commitHold.Commit(8, 0); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := commitHold.Commit(8, 0); !errors.Is(err, ErrReservationSettled) {
		t.Fatalf("second commit = %v, want ErrReservationSettled", err)
	}
	if err := commitHold.Release(); !errors.Is(err, ErrReservationSettled) {
		t.Fatalf("commit then release = %v, want ErrReservationSettled", err)
	}
	if committed := commitHold.Committed(); !committed {
		t.Fatal("Committed() must reflect the settled commit")
	}
	if reserved := ledger.Reserved().TotalTokens; reserved != 0 {
		t.Fatalf("reserved after commit = %d, want 0", reserved)
	}

	// Double release.
	releaseHold, err := ledger.Reserve("run", 10, 0)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := releaseHold.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := releaseHold.Release(); !errors.Is(err, ErrReservationSettled) {
		t.Fatalf("second release = %v, want ErrReservationSettled", err)
	}

	// A fresh hold stays open after a malformed commit attempt.
	pending, err := ledger.Reserve("run", 5, 0)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if pending.Committed() {
		t.Fatal("fresh reservation must not be committed")
	}

	// Negative actuals are malformed.
	if err := pending.Commit(-1, 0); !errors.Is(err, ErrMalformedBudget) {
		t.Fatalf("negative commit = %v, want ErrMalformedBudget", err)
	}
	if _, err := ledger.Reserve("run", -5, 0); !errors.Is(err, ErrMalformedBudget) {
		t.Fatalf("negative estimate = %v, want ErrMalformedBudget", err)
	}
	// The malformed commit did not settle the hold.
	if reserved := ledger.Reserved().TotalTokens; reserved != 5 {
		t.Fatalf("reserved after malformed commit = %d, want 5", reserved)
	}
	pending.Release()
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
