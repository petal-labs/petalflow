package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// GateViolation reports one tripped gate with the metric that moved. A
// violation with Severity "error" fails CI; "warning" is allowed with
// review (within the tolerance margin).
type GateViolation struct {
	// Gate is the gate that tripped (e.g. "min_pass_rate", "max_cost_usd").
	Gate string `json:"gate"`
	// Metric is the measured metric that moved.
	Metric string `json:"metric"`
	// Severity is "error" (beyond tolerance/budget) or "warning" (within
	// the tolerance margin).
	Severity string `json:"severity"`
	// Message attributes the failure to the gate and the values involved.
	Message string `json:"message"`
}

// Gate severities.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Gates configures CI regression gates for a dataset run. Absolute budgets
// cap the metrics of the current run; baseline-relative gates fail when the
// current run regresses against the last accepted baseline artifact.
//
// Tolerance is a relative flakiness margin (e.g. 0.05 allows 5% drift): a
// metric beyond its budget or baseline within the tolerance is a warning;
// beyond that, an error. Absolute budgets are evaluated first, then
// baseline regressions.
//
// The zero value has no gates: nothing fails, which keeps informational
// runs possible.
type Gates struct {
	// MinPassRate floors the overall pass rate (0..1).
	MinPassRate *float64 `json:"min_pass_rate,omitempty"`
	// MaxCostUSD caps the total run cost in USD.
	MaxCostUSD *float64 `json:"max_cost_usd,omitempty"`
	// MaxTotalTokens caps the total token consumption.
	MaxTotalTokens *int `json:"max_total_tokens,omitempty"`
	// MaxLatencyAvgMS caps the average per-case latency in milliseconds.
	MaxLatencyAvgMS *float64 `json:"max_latency_avg_ms,omitempty"`
	// MaxLatencyP95MS caps the p95 per-case latency in milliseconds.
	MaxLatencyP95MS *float64 `json:"max_latency_p95_ms,omitempty"`

	// MaxPassRateDrop fails when the pass rate drops by more than this
	// relative fraction against the baseline (e.g. 0.1 = 10% drop).
	MaxPassRateDrop *float64 `json:"max_pass_rate_drop,omitempty"`
	// MaxCostIncrease fails when cost rises by more than this relative
	// fraction against the baseline.
	MaxCostIncrease *float64 `json:"max_cost_increase,omitempty"`
	// MaxLatencyP95Increase fails when p95 latency rises by more than this
	// relative fraction against the baseline.
	MaxLatencyP95Increase *float64 `json:"max_latency_p95_increase,omitempty"`

	// Tolerance is the relative flakiness margin applied to budgets and
	// baseline comparisons (e.g. 0.05 = 5%). Within tolerance, a violation
	// is a warning; beyond it, an error. Defaults to 0.
	Tolerance float64 `json:"tolerance,omitempty"`
}

// ErrGateFailed is returned by CheckGates when any error-severity violation
// exists. CLI commands translate it to a non-zero exit code.
var ErrGateFailed = errors.New("eval: quality gate failed")

// LoadGates reads a gates configuration from a JSON file.
func LoadGates(path string) (*Gates, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path from caller
	if err != nil {
		return nil, fmt.Errorf("eval: read gates: %w", err)
	}
	var gates Gates
	if err := json.Unmarshal(data, &gates); err != nil {
		return nil, fmt.Errorf("eval: decode gates: %w", err)
	}
	return &gates, nil
}

// Evaluate applies the gates to the current result, comparing against the
// baseline result artifact when one is provided. Violations carrying
// Severity "error" mean the run must fail CI.
func (g Gates) Evaluate(current, baseline *DatasetResult) []GateViolation {
	var violations []GateViolation

	// --- absolute budgets ---
	if g.MinPassRate != nil && current != nil {
		limit := *g.MinPassRate
		actual := current.Aggregate.PassRate
		if actual < limit {
			severity := SeverityError
			if withinTolerance(actual, limit, g.Tolerance) {
				severity = SeverityWarning
			}
			violations = append(violations, GateViolation{
				Gate: "min_pass_rate", Metric: "pass_rate", Severity: severity,
				Message: fmt.Sprintf("pass_rate %.3f below floor %.3f", actual, limit),
			})
		}
	}

	if g.MaxCostUSD != nil && current != nil {
		limit := *g.MaxCostUSD * (1 + g.Tolerance)
		actual := current.Aggregate.CostUSD
		if actual > limit {
			violations = append(violations, GateViolation{
				Gate: "max_cost_usd", Metric: "cost_usd", Severity: SeverityError,
				Message: fmt.Sprintf("cost_usd %.4f exceeds budget %.4f", actual, limit),
			})
		}
	}

	if g.MaxTotalTokens != nil && current != nil {
		limit := float64(*g.MaxTotalTokens) * (1 + g.Tolerance)
		if float64(current.Aggregate.TotalTokens) > limit {
			violations = append(violations, GateViolation{
				Gate: "max_total_tokens", Metric: "total_tokens", Severity: SeverityError,
				Message: fmt.Sprintf("total_tokens %d exceeds budget %.0f", current.Aggregate.TotalTokens, limit),
			})
		}
	}

	if g.MaxLatencyAvgMS != nil && current != nil {
		limit := *g.MaxLatencyAvgMS * (1 + g.Tolerance)
		if current.Aggregate.LatencyAvgMS > limit {
			violations = append(violations, GateViolation{
				Gate: "max_latency_avg_ms", Metric: "latency_avg_ms", Severity: SeverityError,
				Message: fmt.Sprintf("latency_avg_ms %.1f exceeds budget %.1f", current.Aggregate.LatencyAvgMS, limit),
			})
		}
	}

	if g.MaxLatencyP95MS != nil && current != nil {
		limit := *g.MaxLatencyP95MS * (1 + g.Tolerance)
		if current.Aggregate.LatencyP95MS > limit {
			violations = append(violations, GateViolation{
				Gate: "max_latency_p95_ms", Metric: "latency_p95_ms", Severity: SeverityError,
				Message: fmt.Sprintf("latency_p95_ms %.1f exceeds budget %.1f", current.Aggregate.LatencyP95MS, limit),
			})
		}
	}

	// --- baseline-relative regressions ---
	if baseline != nil {
		regressions := g.evaluateBaseline(current, baseline)
		violations = append(violations, regressions...)
	}

	return violations
}

// evaluateBaseline compares the current run against the baseline artifact.
func (g Gates) evaluateBaseline(current, baseline *DatasetResult) []GateViolation {
	var violations []GateViolation
	if current == nil || baseline == nil {
		return violations
	}

	if g.MaxPassRateDrop != nil && baseline.Aggregate.PassRate > 0 {
		limit := *g.MaxPassRateDrop * (1 + g.Tolerance)
		drop := (baseline.Aggregate.PassRate - current.Aggregate.PassRate) / baseline.Aggregate.PassRate
		if drop > 0 {
			severity := SeverityError
			if drop <= limit {
				severity = SeverityWarning
			}
			violations = append(violations, GateViolation{
				Gate: "max_pass_rate_drop", Metric: "pass_rate", Severity: severity,
				Message: fmt.Sprintf("pass_rate %.3f dropped %.1f%% from baseline %.3f (cap %.1f%%)",
					current.Aggregate.PassRate, drop*100, baseline.Aggregate.PassRate, limit*100),
			})
		}
	}

	if g.MaxCostIncrease != nil && baseline.Aggregate.CostUSD > 0 {
		limit := *g.MaxCostIncrease * (1 + g.Tolerance)
		increase := (current.Aggregate.CostUSD - baseline.Aggregate.CostUSD) / baseline.Aggregate.CostUSD
		if increase > 0 {
			severity := SeverityError
			if increase <= limit {
				severity = SeverityWarning
			}
			violations = append(violations, GateViolation{
				Gate: "max_cost_increase", Metric: "cost_usd", Severity: severity,
				Message: fmt.Sprintf("cost_usd %.4f increased %.1f%% from baseline %.4f (cap %.1f%%)",
					current.Aggregate.CostUSD, increase*100, baseline.Aggregate.CostUSD, limit*100),
			})
		}
	}

	if g.MaxLatencyP95Increase != nil && baseline.Aggregate.LatencyP95MS > 0 {
		limit := *g.MaxLatencyP95Increase * (1 + g.Tolerance)
		increase := (current.Aggregate.LatencyP95MS - baseline.Aggregate.LatencyP95MS) / baseline.Aggregate.LatencyP95MS
		if increase > 0 {
			severity := SeverityError
			if increase <= limit {
				severity = SeverityWarning
			}
			violations = append(violations, GateViolation{
				Gate: "max_latency_p95_increase", Metric: "latency_p95_ms", Severity: severity,
				Message: fmt.Sprintf("latency_p95_ms %.1f increased %.1f%% from baseline %.1f (cap %.1f%%)",
					current.Aggregate.LatencyP95MS, increase*100, baseline.Aggregate.LatencyP95MS, limit*100),
			})
		}
	}

	return violations
}

// withinTolerance reports whether value misses limit by at most the
// relative tolerance (works for floors and ceilings alike).
func withinTolerance(value, limit, tolerance float64) bool {
	if limit == 0 {
		return false
	}
	drift := (value - limit) / limit
	if drift < 0 {
		drift = -drift
	}
	return drift <= tolerance
}

// CheckGates evaluates the gates and returns ErrGateFailed if any
// error-severity violation exists. Warnings are returned but do not fail.
func (g Gates) CheckGates(current, baseline *DatasetResult) ([]GateViolation, error) {
	violations := g.Evaluate(current, baseline)
	for _, v := range violations {
		if v.Severity == SeverityError {
			return violations, ErrGateFailed
		}
	}
	return violations, nil
}
