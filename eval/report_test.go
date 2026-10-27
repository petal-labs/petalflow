package eval

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleResult() *DatasetResult {
	return &DatasetResult{
		Dataset:   "sample",
		Graders:   []string{"var_equals:answer_output"},
		StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Finished:  time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC),
		Results: []CaseResult{
			{
				CaseID:   "pass-1",
				Passed:   true,
				Grades:   []GradeResult{{Grader: "var_equals:answer_output", Score: 1, Passed: true}},
				Duration: 100,
				Usage:    UsageTotals{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, CostUSD: 0.001},
			},
			{
				CaseID:   "fail-1",
				Passed:   false,
				Grades:   []GradeResult{{Grader: "var_equals:answer_output", Score: 0, Reason: "answer_output = \"nope\""}},
				Error:    "grader failed",
				Duration: 200,
				Usage:    UsageTotals{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, CostUSD: 0.001},
			},
		},
		Aggregate: Aggregate{
			TotalCases: 2, Passed: 1, Failed: 1, PassRate: 0.5,
			TotalTokens: 30, CostUSD: 0.002,
			LatencyAvgMS: 150, LatencyP50MS: 100, LatencyP95MS: 200, TotalTimeMS: 2000,
		},
	}
}

func TestWriteLoadReportRoundTrip(t *testing.T) {
	result := sampleResult()
	path := filepath.Join(t.TempDir(), "report.json")

	if err := WriteReport(result, path); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	loaded, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if loaded.Dataset != "sample" || loaded.Aggregate.TotalCases != 2 || loaded.Aggregate.PassRate != 0.5 {
		t.Errorf("round-trip mismatch: %+v", loaded)
	}
	if len(loaded.Results) != 2 || loaded.Results[1].Error != "grader failed" {
		t.Errorf("results mismatch: %+v", loaded.Results)
	}
}

func TestSummarize(t *testing.T) {
	result := sampleResult()
	summary := Summarize(result)

	for _, want := range []string{"sample", "1/2 passed (50%)", "tokens=30", "cost_usd=0.0020", "FAIL fail-1", "answer_output"} {
		if !strings.Contains(summary, want) {
			t.Errorf("Summarize() missing %q in %q", want, summary)
		}
	}
}

func TestSummarizeAllPassed(t *testing.T) {
	result := sampleResult()
	result.Results[1].Passed = true
	result.Results[1].Error = ""
	result.Aggregate.Passed = 2
	result.Aggregate.Failed = 0
	result.Aggregate.PassRate = 1.0

	if strings.Contains(Summarize(result), "FAIL") {
		t.Error("all-pass summary should not list failures")
	}
}

func TestSummarizeNil(t *testing.T) {
	if Summarize(nil) != "" {
		t.Error("Summarize(nil) should be empty")
	}
}

func TestSaveDatasetInvalid(t *testing.T) {
	if err := SaveDataset(&Dataset{Name: "no-cases"}, filepath.Join(t.TempDir(), "d.json")); !strings.Contains(err.Error(), "at least one case") {
		t.Errorf("expected validation error, got %v", err)
	}
}
