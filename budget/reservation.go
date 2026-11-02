package budget

import (
	"errors"
	"fmt"
)

// ErrReservationSettled signals that a reservation was already committed
// or released, so Commit/Release is not applicable any more.
var ErrReservationSettled = errors.New("budget reservation already settled")

// Reservation is a granted pre-dispatch budget hold. Estimates count
// towards the ceiling from Reserve until Commit or Release settles the
// reservation, which makes the preflight gate race-safe under parallel
// branches: concurrent reservations cannot each slip their committed
// spend past a ceiling.
//
// A single reservation settles exactly once — attempts to Commit or
// Release twice return ErrReservationSettled.
type Reservation struct {
	ledger   *Ledger
	id       uint64
	scope    string
	estimate Spend

	settled bool
}

// Scope returns the ledger scope the reservation was taken on.
func (r *Reservation) Scope() string {
	if r == nil {
		return ""
	}
	return r.scope
}

// Estimate returns the reserved (estimated) spend held by this
// reservation.
func (r *Reservation) Estimate() Spend {
	if r == nil {
		return Spend{}
	}
	return r.estimate
}

// Committed reports whether the reservation already recorded actual
// spend.
func (r *Reservation) Committed() bool {
	if r == nil {
		return false
	}
	return r.settled
}

// Commit settles the reservation by recording the actual dispatch spend.
// The recorded actual may exceed both the estimate and the ceiling (the
// spend already happened); enforcement happens at Reserve before the
// dispatch. Attempts to commit after any settlement return
// ErrReservationSettled.
func (r *Reservation) Commit(tokens int, costUSD float64) error {
	return r.settle(true, tokens, costUSD)
}

// Release abandons a reservation without recording spend (a failed or
// aborted dispatch). Attempts to release after any settlement return
// ErrReservationSettled.
func (r *Reservation) Release() error {
	return r.settle(false, 0, 0)
}

func (r *Reservation) settle(commit bool, tokens int, costUSD float64) error {
	if r == nil || r.ledger == nil {
		return ErrReservationSettled
	}
	if tokens < 0 || costUSD < 0 {
		return fmt.Errorf("%w: negative spend (tokens=%d, cost_usd=%.4f)", ErrMalformedBudget, tokens, costUSD)
	}

	r.ledger.mu.Lock()
	defer r.ledger.mu.Unlock()
	if r.settled {
		return ErrReservationSettled
	}
	r.settled = true

	if est, held := r.ledger.holds[r.id]; held {
		r.ledger.reserved.TotalTokens -= est.TotalTokens
		r.ledger.reserved.CostUSD -= est.CostUSD
		delete(r.ledger.holds, r.id)
	}
	if commit {
		r.ledger.spent.TotalTokens += tokens
		r.ledger.spent.CostUSD += costUSD
	}
	return nil
}
