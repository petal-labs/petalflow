// Package budget provides the cumulative spend accounting and hard cost
// ceilings for PetalFlow runs: a concurrency-safe ledger that per-node and
// per-run budgets draw from, so retries and parallel branches cannot
// bypass a ceiling.
//
// Accounting model: every provider dispatch first checks the ledger
// against a predicted spend (the preflight estimate), rejecting the call
// before it happens if the prediction breaches a ceiling, then records the
// actual spend after the call. Recording happens at the dispatch point;
// clients that dispatch internally (such as a fallback chain) declare it
// via SelfRecorder so outer layers do not double-count.
package budget

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrBudgetExceeded signals that a call was rejected because its projected
// spend breaches a budget ceiling. Diagnosable via errors.Is; the
// BudgetExceededError carries the gate, ceiling, and projected values.
var ErrBudgetExceeded = errors.New("budget exceeded")

// ErrMalformedBudget signals invalid budget configuration: negative
// ceilings cannot be satisfied and are rejected up front rather than
// silently ignored.
var ErrMalformedBudget = errors.New("malformed budget")

// Ceiling bounds cumulative spend. Zero values mean unbounded; negative
// values are malformed.
type Ceiling struct {
	// MaxTotalTokens caps the cumulative token consumption.
	MaxTotalTokens int
	// MaxCostUSD caps the cumulative cost in USD.
	MaxCostUSD float64
}

// Validate reports malformed ceilings (negative values).
func (c Ceiling) Validate() error {
	if c.MaxTotalTokens < 0 {
		return fmt.Errorf("%w: max_total_tokens %d is negative", ErrMalformedBudget, c.MaxTotalTokens)
	}
	if c.MaxCostUSD < 0 {
		return fmt.Errorf("%w: max_cost_usd %.4f is negative", ErrMalformedBudget, c.MaxCostUSD)
	}
	return nil
}

// unbounded reports whether a ceiling imposes no limits.
func (c Ceiling) unbounded() bool {
	return c.MaxTotalTokens == 0 && c.MaxCostUSD == 0
}

// Spend is the cumulative recorded spend of a ledger.
type Spend struct {
	TotalTokens int     `json:"total_tokens"`
	CostUSD     float64 `json:"cost_usd"`
}

// BudgetExceededError describes a rejected call: which gate tripped, the
// ceiling, the spend so far, and the projected total.
type BudgetExceededError struct {
	// Scope names the ledger scope ("run" or "node:<id>").
	Scope string
	// Gate is the metric that breached ("total_tokens" or "cost_usd").
	Gate string
	// Ceiling is the configured limit for the gate.
	Ceiling float64
	// Projected is the projected total (spent + estimate).
	Projected float64
	// Spent is the recorded spend before this call.
	Spent Spend
	// Estimate is the preflight estimate for the rejected call.
	Estimate Spend
}

// Error implements error, attributing the failure to the gate that tripped
// and the metric that moved.
func (e *BudgetExceededError) Error() string {
	if e.Gate == "cost_usd" {
		return fmt.Sprintf("budget exceeded (%s): cost_usd projected %.4f exceeds ceiling %.4f (spent %.4f)",
			e.Scope, e.Projected, e.Ceiling, e.Spent.CostUSD)
	}
	return fmt.Sprintf("budget exceeded (%s): total_tokens projected %d exceeds ceiling %d (spent %d)",
		e.Scope, int(e.Projected), int(e.Ceiling), e.Spent.TotalTokens)
}

// Unwrap exposes the sentinel.
func (e *BudgetExceededError) Unwrap() error {
	return ErrBudgetExceeded
}

// Ledger accounts cumulative spend against a ceiling. Safe for concurrent
// use: parallel branches and retries share one ledger, so concurrency
// cannot multiply spend past a ceiling.
type Ledger struct {
	mu      sync.Mutex
	ceiling Ceiling
	spent   Spend
}

// NewLedger creates a ledger bounded by the ceiling. Malformed ceilings
// (negative) are rejected up front.
func NewLedger(ceiling Ceiling) (*Ledger, error) {
	if err := ceiling.Validate(); err != nil {
		return nil, err
	}
	return &Ledger{ceiling: ceiling}, nil
}

// Spent returns the recorded spend.
func (l *Ledger) Spent() Spend {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.spent
}

// Check validates a predicted spend (estimate) against the remaining
// budget without recording anything. costUSD may be 0 when the cost is
// unknown (#206): the token gate still applies, the cost gate cannot.
func (l *Ledger) Check(scope string, estimateTokens int, estimateCostUSD float64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.ceiling.unbounded() {
		return nil
	}

	projectedTokens := l.spent.TotalTokens + estimateTokens
	if l.ceiling.MaxTotalTokens > 0 && projectedTokens > l.ceiling.MaxTotalTokens {
		return &BudgetExceededError{
			Scope:     scope,
			Gate:      "total_tokens",
			Ceiling:   float64(l.ceiling.MaxTotalTokens),
			Projected: float64(projectedTokens),
			Spent:     l.spent,
			Estimate:  Spend{TotalTokens: estimateTokens, CostUSD: estimateCostUSD},
		}
	}

	if estimateCostUSD > 0 && l.ceiling.MaxCostUSD > 0 {
		projectedCost := l.spent.CostUSD + estimateCostUSD
		if projectedCost > l.ceiling.MaxCostUSD {
			return &BudgetExceededError{
				Scope:     scope,
				Gate:      "cost_usd",
				Ceiling:   l.ceiling.MaxCostUSD,
				Projected: projectedCost,
				Spent:     l.spent,
				Estimate:  Spend{TotalTokens: estimateTokens, CostUSD: estimateCostUSD},
			}
		}
	}
	return nil
}

// Record commits actual spend after a dispatch. Actuals may exceed the
// ceiling (the spend already happened); enforcement happens in Check
// before dispatch. Negative values are malformed and rejected.
func (l *Ledger) Record(tokens int, costUSD float64) error {
	if tokens < 0 || costUSD < 0 {
		return fmt.Errorf("%w: negative spend (tokens=%d, cost_usd=%.4f)", ErrMalformedBudget, tokens, costUSD)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.spent.TotalTokens += tokens
	l.spent.CostUSD += costUSD
	return nil
}

// SelfRecorder is implemented by clients that account for their own spend
// at the dispatch point (e.g. a fallback chain that checks and records per
// target attempt). Outer layers skip recording for such clients to avoid
// double-counting; pre-dispatch checks remain the outer layer's
// responsibility.
type SelfRecorder interface {
	RecordsOwnSpend() bool
}

// Context propagation: the run ledger travels on the node execution
// context so every dispatch point shares one accounting source.
type ledgerKey struct{}

// ContextWithLedger attaches a run-scoped budget ledger to the context.
func ContextWithLedger(ctx context.Context, ledger *Ledger) context.Context {
	return context.WithValue(ctx, ledgerKey{}, ledger)
}

// LedgerFromContext retrieves the run-scoped budget ledger, or nil when no
// ledger is configured.
func LedgerFromContext(ctx context.Context) *Ledger {
	ledger, _ := ctx.Value(ledgerKey{}).(*Ledger)
	return ledger
}
