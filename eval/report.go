package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// WriteReport writes the dataset result to path as formatted JSON, suitable
// for diffing across runs (baselines) and for downstream tooling.
func WriteReport(result *DatasetResult, path string) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("eval: encode report: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("eval: write report: %w", err)
	}
	return nil
}

// LoadReport reads a report written by WriteReport. Reports are ordinary
// JSON; this helper adds no validation beyond decoding.
func LoadReport(path string) (*DatasetResult, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path from caller
	if err != nil {
		return nil, fmt.Errorf("eval: read report: %w", err)
	}
	var result DatasetResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("eval: decode report: %w", err)
	}
	return &result, nil
}

// Summarize returns a compact, content-free summary of a dataset result for
// logs and CI output: pass rate, token usage, and latency bounds.
func Summarize(result *DatasetResult) string {
	if result == nil {
		return ""
	}
	agg := result.Aggregate
	var b strings.Builder
	fmt.Fprintf(&b, "dataset %s: %d/%d passed (%.0f%%)",
		result.Dataset, agg.Passed, agg.TotalCases, agg.PassRate*100)
	if agg.TotalTokens > 0 {
		fmt.Fprintf(&b, ", tokens=%d", agg.TotalTokens)
	}
	if agg.CostUSD > 0 {
		fmt.Fprintf(&b, ", cost_usd=%.4f", agg.CostUSD)
	}
	fmt.Fprintf(&b, ", latency avg=%.0fms p95=%.0fms", agg.LatencyAvgMS, agg.LatencyP95MS)

	for _, r := range result.Results {
		if !r.Passed {
			fmt.Fprintf(&b, "\n  FAIL %s", r.CaseID)
			for _, g := range r.Grades {
				if !g.Passed {
					fmt.Fprintf(&b, " [%s: %s]", g.Grader, g.Reason)
				}
			}
			if r.Error != "" {
				fmt.Fprintf(&b, " [error: %s]", r.Error)
			}
		}
	}
	return b.String()
}
