package eval

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resultWith(passRate float64, cost float64, tokens int, p95 float64) *DatasetResult {
	return &DatasetResult{
		Aggregate: Aggregate{
			PassRate: passRate, Passed: int(passRate * 10), Failed: 10 - int(passRate*10), TotalCases: 10,
			CostUSD: cost, TotalTokens: tokens, LatencyP95MS: p95, LatencyAvgMS: p95 / 2,
		},
	}
}

func TestEvaluateGatesNoGates(t *testing.T) {
	gates := Gates{}
	if violations := gates.Evaluate(resultWith(0, 0, 0, 0), nil); len(violations) != 0 {
		t.Errorf("no gates should yield no violations, got %+v", violations)
	}
}

func TestMinPassRateGate(t *testing.T) {
	floor := 0.9
	gates := Gates{MinPassRate: &floor}

	// Above the floor: pass.
	if v := gates.Evaluate(resultWith(0.95, 0, 0, 0), nil); len(v) != 0 {
		t.Errorf("pass rate above floor should pass: %+v", v)
	}
	// Below the floor beyond tolerance: error.
	v := gates.Evaluate(resultWith(0.5, 0, 0, 0), nil)
	if len(v) != 1 || v[0].Severity != SeverityError || v[0].Gate != "min_pass_rate" {
		t.Fatalf("expected min_pass_rate error, got %+v", v)
	}
	if !strings.Contains(v[0].Message, "0.500") {
		t.Errorf("message should attribute the metric: %s", v[0].Message)
	}

	// Below the floor within tolerance: warning.
	gates.Tolerance = 0.2
	v = gates.Evaluate(resultWith(0.85, 0, 0, 0), nil)
	if len(v) != 1 || v[0].Severity != SeverityWarning {
		t.Errorf("within-tolerance violation should warn, got %+v", v)
	}
	if _, err := gates.CheckGates(resultWith(0.85, 0, 0, 0), nil); err != nil {
		t.Errorf("warning should not fail CheckGates: %v", err)
	}
	if _, err := gates.CheckGates(resultWith(0.5, 0, 0, 0), nil); !errors.Is(err, ErrGateFailed) {
		t.Errorf("error violation should fail CheckGates: %v", err)
	}
}

func TestBudgetGates(t *testing.T) {
	cost := 1.0
	tokens := 1000
	gates := Gates{MaxCostUSD: &cost, MaxTotalTokens: &tokens}

	if v := gates.Evaluate(resultWith(1, 0.9, 900, 0), nil); len(v) != 0 {
		t.Errorf("within-budget run should pass: %+v", v)
	}

	v := gates.Evaluate(resultWith(1, 2.0, 5000, 0), nil)
	if len(v) != 2 {
		t.Fatalf("expected 2 violations, got %+v", v)
	}
	gatesSeen := map[string]bool{}
	for _, violation := range v {
		gatesSeen[violation.Gate] = true
		if violation.Severity != SeverityError {
			t.Errorf("budget overruns are errors: %+v", violation)
		}
	}
	if !gatesSeen["max_cost_usd"] || !gatesSeen["max_total_tokens"] {
		t.Errorf("missing gates: %+v", gatesSeen)
	}
}

func TestLatencyBudgetGate(t *testing.T) {
	p95 := 100.0
	gates := Gates{MaxLatencyP95MS: &p95}

	if v := gates.Evaluate(resultWith(1, 0, 0, 90), nil); len(v) != 0 {
		t.Errorf("within budget should pass: %+v", v)
	}
	v := gates.Evaluate(resultWith(1, 0, 0, 500), nil)
	if len(v) != 1 || v[0].Gate != "max_latency_p95_ms" {
		t.Fatalf("expected latency gate error, got %+v", v)
	}
}

func TestBaselineRegressionGates(t *testing.T) {
	increase := 0.1 // 10% cap
	drop := 0.1
	gates := Gates{MaxCostIncrease: &increase, MaxPassRateDrop: &drop, MaxLatencyP95Increase: &increase}

	// Identical to baseline: pass.
	baseline := resultWith(1.0, 1.0, 0, 100)
	if v := gates.Evaluate(resultWith(1.0, 1.0, 0, 100), baseline); len(v) != 0 {
		t.Errorf("no regression should pass: %+v", v)
	}

	// Small improvement: pass.
	if v := gates.Evaluate(resultWith(1.0, 0.5, 0, 50), baseline); len(v) != 0 {
		t.Errorf("improvement should pass: %+v", v)
	}

	// Big regression: errors attributed to the right gates.
	current := resultWith(0.5, 2.0, 0, 300)
	v := gates.Evaluate(current, baseline)
	if len(v) != 3 {
		t.Fatalf("expected 3 violations, got %+v", v)
	}
	seen := map[string]string{}
	for _, violation := range v {
		seen[violation.Gate] = violation.Severity
		if !strings.Contains(violation.Message, "baseline") {
			t.Errorf("message should reference baseline: %s", violation.Message)
		}
	}
	if seen["max_pass_rate_drop"] != SeverityError || seen["max_cost_increase"] != SeverityError || seen["max_latency_p95_increase"] != SeverityError {
		t.Errorf("severities: %+v", seen)
	}

	// Small regressions within the tolerance-widened caps: warnings.
	gates.Tolerance = 0.5
	v = gates.Evaluate(resultWith(0.95, 1.12, 0, 112), baseline)
	for _, violation := range v {
		if violation.Severity != SeverityWarning {
			t.Errorf("within-tolerance regression should warn: %+v", violation)
		}
	}
}

func TestCheckGatesWithBaseline(t *testing.T) {
	gates := Gates{}
	baseline := resultWith(1.0, 1.0, 0, 100)

	// No gates: baseline regressions are not enforced.
	if _, err := gates.CheckGates(resultWith(0.1, 99, 99999, 9999), baseline); err != nil {
		t.Errorf("no gates configured should pass: %v", err)
	}
}

func TestLoadGates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gates.json")
	data := `{"min_pass_rate": 0.9, "max_cost_usd": 1.0, "tolerance": 0.05}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	gates, err := LoadGates(path)
	if err != nil {
		t.Fatalf("LoadGates: %v", err)
	}
	if gates.MinPassRate == nil || *gates.MinPassRate != 0.9 || gates.Tolerance != 0.05 {
		t.Errorf("gates = %+v", gates)
	}

	if _, err := LoadGates(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("expected error for missing file")
	}
}

// --- policy tests ---

func TestLoadPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	data := `{
		"graders": [
			{"type": "var_equals", "var": "answer_output"},
			{"type": "contains", "var": "answer_output", "substring": "refund"},
			{"type": "threshold", "var": "score", "min": 0.5}
		],
		"gates": {"min_pass_rate": 0.8}
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	policy, graders, err := LoadPolicy(path)
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	if len(graders) != 3 {
		t.Fatalf("graders = %d, want 3", len(graders))
	}
	if policy.Gates.MinPassRate == nil || *policy.Gates.MinPassRate != 0.8 {
		t.Errorf("gates = %+v", policy.Gates)
	}
}

func TestLoadPolicyErrors(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "no graders", data: `{"gates": {}}`},
		{name: "unknown grader type", data: `{"graders": [{"type": "llm_judge"}]}`},
		{name: "grader missing var", data: `{"graders": [{"type": "var_equals"}]}`},
		{name: "grader missing substring", data: `{"graders": [{"type": "contains", "var": "x"}]}`},
		{name: "malformed json", data: `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "policy.json")
			if err := os.WriteFile(path, []byte(tt.data), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, _, err := LoadPolicy(path); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
