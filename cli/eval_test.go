package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/eval"
)

// evalWorkflowYAML produces a deterministic output var for every case.
const evalWorkflowYAML = `kind: graph
id: eval-test-workflow
version: "1.0"
nodes:
  - id: produce
    type: transform
    config:
      transform: template
      template: "fixed answer"
      output_var: answer_output
entry: produce
`

func writeEvalFixtures(t *testing.T, minPassRate float64) (workflow, dataset, policy string) {
	t.Helper()
	dir := t.TempDir()

	workflow = filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(workflow, []byte(evalWorkflowYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	dataset = filepath.Join(dir, "dataset.json")
	ds := `{
  "name": "cli-eval",
  "cases": [
    {"id": "case-1", "input": "one", "expected": {"answer_output": "fixed answer"}},
    {"id": "case-2", "input": "two", "expected": {"answer_output": "fixed answer"}}
  ]
}`
	if err := os.WriteFile(dataset, []byte(ds), 0o600); err != nil {
		t.Fatal(err)
	}

	policyJSON := `{
  "graders": [{"type": "var_equals", "var": "answer_output"}],
  "gates": {"min_pass_rate": ` + fmt.Sprintf("%v", minPassRate) + `}
}`
	policy = filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policy, []byte(policyJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	return workflow, dataset, policy
}

func runEvalCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewEvalCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestEvalCmdPassingRun(t *testing.T) {
	// Floor 0.5: both cases pass.
	workflow, dataset, policy := writeEvalFixtures(t, 0.5)
	report := filepath.Join(t.TempDir(), "report.json")

	out, err := runEvalCmd(t, workflow, "--dataset", dataset, "--policy", policy, "--report", report)
	if err != nil {
		t.Fatalf("eval failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "2/2 passed") {
		t.Errorf("summary missing: %s", out)
	}
	if !strings.Contains(out, "All gates passed") {
		t.Errorf("gate pass message missing: %s", out)
	}

	loaded, err := eval.LoadReport(report)
	if err != nil {
		t.Fatalf("report not written: %v", err)
	}
	if loaded.Dataset != "cli-eval" || len(loaded.Results) != 2 {
		t.Errorf("report mismatch: %+v", loaded)
	}
}

func TestEvalCmdGateFailureExitsNonZero(t *testing.T) {
	// Floor 1.0 with a deliberately broken expectation: pass rate 0.5.
	workflow, dataset, policy := writeEvalFixtures(t, 1.0)

	// Break one expectation so pass rate = 0.5 < 1.0.
	dsData, err := os.ReadFile(dataset)
	if err != nil {
		t.Fatal(err)
	}
	dsData = []byte(strings.Replace(string(dsData), "fixed answer", "expected different", 1))
	if err := os.WriteFile(dataset, dsData, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = runEvalCmd(t, workflow, "--dataset", dataset, "--policy", policy)
	var exitErr *ExitError
	if err == nil {
		t.Fatal("expected gate failure exit error")
	}
	if !errors.As(err, &exitErr) || exitErr.Code != exitValidation {
		t.Errorf("expected exitValidation, got %v", err)
	}
}

func TestEvalCmdBaselineCompareAndUpdate(t *testing.T) {
	workflow, dataset, policy := writeEvalFixtures(t, 0.5)
	baseline := filepath.Join(t.TempDir(), "baseline.json")

	// First run: no baseline exists yet (not an error), then update it.
	out, err := runEvalCmd(t, workflow, "--dataset", dataset, "--policy", policy,
		"--baseline", baseline, "--update-baseline")
	if err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Baseline updated") {
		t.Errorf("baseline update message missing: %s", out)
	}
	if _, err := os.Stat(baseline); err != nil {
		t.Fatalf("baseline artifact not written: %v", err)
	}

	// Second run compares against the baseline: still passes.
	out, err = runEvalCmd(t, workflow, "--dataset", dataset, "--policy", policy, "--baseline", baseline)
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "All gates passed") {
		t.Errorf("gate pass message missing: %s", out)
	}
}

func TestEvalCmdRequiredFlags(t *testing.T) {
	dir := t.TempDir()
	workflow := filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(workflow, []byte(evalWorkflowYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runEvalCmd(t, workflow); err == nil {
		t.Error("missing --dataset should fail")
	}
	if _, err := runEvalCmd(t, workflow, "--dataset", filepath.Join(dir, "d.json")); err == nil {
		t.Error("missing --policy should fail")
	}
}

func TestEvalCmdInvalidPolicy(t *testing.T) {
	workflow, dataset, _ := writeEvalFixtures(t, 0)
	policy := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policy, []byte(`{"graders": [{"type": "bogus"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runEvalCmd(t, workflow, "--dataset", dataset, "--policy", policy); err == nil {
		t.Error("unknown grader type should fail")
	}
}
