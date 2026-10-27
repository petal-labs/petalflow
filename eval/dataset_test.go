package eval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/petal-labs/petalflow/core"
)

func sampleDataset() *Dataset {
	return &Dataset{
		Name: "sample",
		Cases: []Case{
			{
				ID:       "c1",
				Input:    "hello",
				Expected: map[string]any{"answer_output": "hello answer"},
			},
			{
				ID:       "c2",
				Input:    "world",
				Vars:     map[string]any{"tone": "formal"},
				Expected: map[string]any{"answer_output": "world answer"},
				Tags:     []string{"slow"},
			},
		},
	}
}

func TestDatasetValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Dataset)
		wantErr bool
	}{
		{name: "valid", mutate: func(d *Dataset) {}},
		{
			name:    "missing name",
			mutate:  func(d *Dataset) { d.Name = "" },
			wantErr: true,
		},
		{
			name:    "no cases",
			mutate:  func(d *Dataset) { d.Cases = nil },
			wantErr: true,
		},
		{
			name:    "case missing id",
			mutate:  func(d *Dataset) { d.Cases[0].ID = "" },
			wantErr: true,
		},
		{
			name: "duplicate case ids",
			mutate: func(d *Dataset) {
				d.Cases[1].ID = d.Cases[0].ID
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds := sampleDataset()
			tt.mutate(ds)
			err := ds.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrInvalidDataset) {
				t.Errorf("error should wrap ErrInvalidDataset, got %v", err)
			}
		})
	}
}

func TestDatasetFilter(t *testing.T) {
	ds := sampleDataset()
	if got := len(ds.Filter(nil)); got != 2 {
		t.Errorf("Filter(nil) = %d cases, want 2", got)
	}
	if got := len(ds.Filter([]string{"slow"})); got != 1 {
		t.Errorf("Filter(slow) = %d cases, want 1", got)
	}
	if got := len(ds.Filter([]string{"missing"})); got != 0 {
		t.Errorf("Filter(missing) = %d cases, want 0", got)
	}
}

func TestDatasetSaveLoadRoundTrip(t *testing.T) {
	ds := sampleDataset()
	path := filepath.Join(t.TempDir(), "dataset.json")

	if err := SaveDataset(ds, path); err != nil {
		t.Fatalf("SaveDataset: %v", err)
	}
	loaded, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if loaded.Name != "sample" || len(loaded.Cases) != 2 {
		t.Errorf("round-trip mismatch: %+v", loaded)
	}
	if loaded.Cases[1].Vars["tone"] != "formal" {
		t.Errorf("vars not preserved: %+v", loaded.Cases[1])
	}
}

func TestLoadDatasetInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"name":"x"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadDataset(path); !errors.Is(err, ErrInvalidDataset) {
		t.Errorf("expected ErrInvalidDataset, got %v", err)
	}
	if _, err := LoadDataset(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("expected error for missing file")
	}
}

// --- grader tests ---

func ptr(f float64) *float64 {
	return &f
}

func TestVarEquals(t *testing.T) {
	tests := []struct {
		name       string
		actual     map[string]any
		expected   map[string]any
		wantPassed bool
	}{
		{
			name:       "string match",
			actual:     map[string]any{"out": "hello answer"},
			expected:   map[string]any{"out": "hello answer"},
			wantPassed: true,
		},
		{
			name:       "string differs",
			actual:     map[string]any{"out": "nope"},
			expected:   map[string]any{"out": "hello answer"},
			wantPassed: false,
		},
		{
			name:       "object match regardless of decode types",
			actual:     map[string]any{"out": map[string]any{"count": float64(3), "ok": true}},
			expected:   map[string]any{"out": map[string]any{"count": 3, "ok": true}},
			wantPassed: true,
		},
		{
			name:       "missing var",
			actual:     map[string]any{},
			expected:   map[string]any{"out": "x"},
			wantPassed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := core.NewEnvelope()
			for k, v := range tt.actual {
				env.SetVar(k, v)
			}
			grade, err := VarEquals{Var: "out"}.Grade(context.Background(), CaseEvaluation{
				Case:  Case{ID: "c", Expected: tt.expected},
				Final: env,
			})
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			if grade.Passed != tt.wantPassed {
				t.Errorf("Passed = %v, want %v (grade %+v)", grade.Passed, tt.wantPassed, grade)
			}
		})
	}
}

func TestVarEqualsMissingExpectation(t *testing.T) {
	_, err := VarEquals{Var: "out"}.Grade(context.Background(), CaseEvaluation{
		Case:  Case{ID: "c"},
		Final: core.NewEnvelope(),
	})
	if err == nil {
		t.Fatal("expected error when the case has no expected value")
	}
}

func TestExact(t *testing.T) {
	env := core.NewEnvelope()
	env.SetVar("count", 42.0)

	grade, err := Exact{Var: "count", Value: 42}.Grade(context.Background(), CaseEvaluation{Final: env})
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if !grade.Passed {
		t.Errorf("int/float64 equality should pass: %+v", grade)
	}

	env.SetVar("count", 41.0)
	grade, _ = Exact{Var: "count", Value: 42}.Grade(context.Background(), CaseEvaluation{Final: env})
	if grade.Passed {
		t.Error("mismatched value should fail")
	}
}

func TestContains(t *testing.T) {
	env := core.NewEnvelope()
	env.SetVar("text", "the deployment succeeded on tuesday")

	grade, err := Contains{Var: "text", Substring: "succeeded"}.Grade(context.Background(), CaseEvaluation{Final: env})
	if err != nil || !grade.Passed {
		t.Errorf("expected pass, got %+v err %v", grade, err)
	}

	grade, _ = Contains{Var: "text", Substring: "failed"}.Grade(context.Background(), CaseEvaluation{Final: env})
	if grade.Passed {
		t.Error("expected fail")
	}
}

func TestRegex(t *testing.T) {
	env := core.NewEnvelope()
	env.SetVar("code", "release-1.2.3")

	grade, err := Regex{Var: "code", Pattern: `^release-\d+\.\d+\.\d+$`}.Grade(context.Background(), CaseEvaluation{Final: env})
	if err != nil || !grade.Passed {
		t.Errorf("expected pass, got %+v err %v", grade, err)
	}

	badPattern := Regex{Var: "code", Pattern: "["}
	if _, err := badPattern.Grade(context.Background(), CaseEvaluation{Final: env}); err == nil {
		t.Error("invalid pattern should error")
	}
}

func TestSchemaGrader(t *testing.T) {
	env := core.NewEnvelope()
	env.SetVar("output", map[string]any{"name": "deploy", "version": float64(3)})

	valid := Schema{Var: "output", Schema: map[string]any{
		"type":                 "object",
		"required":             []any{"name", "version"},
		"additionalProperties": false,
		"properties": map[string]any{
			"name":    map[string]any{"type": "string"},
			"version": map[string]any{"type": "number"},
		},
	}}
	grade, err := valid.Grade(context.Background(), CaseEvaluation{Final: env})
	if err != nil || !grade.Passed {
		t.Errorf("expected pass, got %+v err %v", grade, err)
	}

	invalid := Schema{Var: "output", Schema: map[string]any{
		"type":     "object",
		"required": []any{"missing_field"},
	}}
	grade, err = invalid.Grade(context.Background(), CaseEvaluation{Final: env})
	if err != nil || grade.Passed {
		t.Errorf("expected fail, got %+v err %v", grade, err)
	}

	badSchema := Schema{Var: "output", Schema: map[string]any{"type": "not-a-type"}}
	if _, err := badSchema.Grade(context.Background(), CaseEvaluation{Final: env}); err == nil {
		t.Error("invalid schema should error")
	}
}

func TestThreshold(t *testing.T) {
	env := core.NewEnvelope()
	env.SetVar("score", 0.87)

	pass, err := NewThreshold("score", ptr(0.8), ptr(0.9)).Grade(context.Background(), CaseEvaluation{Final: env})
	if err != nil || !pass.Passed {
		t.Errorf("expected pass, got %+v err %v", pass, err)
	}

	fail, _ := NewThreshold("score", ptr(0.9), nil).Grade(context.Background(), CaseEvaluation{Final: env})
	if fail.Passed {
		t.Error("below min should fail")
	}

	env.SetVar("text", "not a number")
	nonNumeric, _ := Threshold{Var: "text"}.Grade(context.Background(), CaseEvaluation{Final: env})
	if nonNumeric.Passed {
		t.Error("non-numeric should fail")
	}
}

func TestGraderNilFinalEnvelope(t *testing.T) {
	g := VarEquals{Var: "out"}
	grade, err := g.Grade(context.Background(), CaseEvaluation{
		Case:  Case{ID: "c", Expected: map[string]any{"out": "x"}},
		Final: nil,
	})
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if grade.Passed {
		t.Error("nil envelope should fail the grade")
	}
}
