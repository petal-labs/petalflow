package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/petal-labs/petalflow/core"
)

// Grade is the outcome of grading a case.
type Grade struct {
	// Name identifies the grader that produced this grade.
	Name string `json:"name"`
	// Score is a grader-defined quality score (1.0 = pass, 0.0 = fail for
	// binary graders; LLM-judge graders may use any 0..1 scale).
	Score float64 `json:"score"`
	// Passed reports whether the grade meets the assertion.
	Passed bool `json:"passed"`
	// Reason explains the outcome (failure detail or confirmation).
	Reason string `json:"reason,omitempty"`
}

// CaseEvaluation carries everything a grader may inspect: the case, the
// final envelope of the run, and the run error, if any. Envelopes must be
// treated as read-only.
type CaseEvaluation struct {
	Case Case
	// Final is the final envelope of the run (nil when the run failed
	// before producing one).
	Final *core.Envelope
	// RunError is the run's terminal error, if any.
	RunError error
}

// Grader asserts on a case's run output. Implement it outside the PetalFlow
// tree for custom assertions (e.g. an LLM-as-judge calling a model API);
// only this interface and the eval package's value types are required.
type Grader interface {
	// Name identifies the grader in reports.
	Name() string
	// Grade evaluates one case's run outcome.
	Grade(ctx context.Context, eval CaseEvaluation) (Grade, error)
}

// varString extracts an envelope variable as a string.
func varString(env *core.Envelope, key string) (string, bool) {
	if env == nil {
		return "", false
	}
	v, ok := env.GetVar(key)
	if !ok {
		return "", false
	}
	switch typed := v.(type) {
	case string:
		return typed, true
	case fmt.Stringer:
		return typed.String(), true
	default:
		b, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", v), true
		}
		return string(b), true
	}
}

// VarEquals asserts that an envelope variable equals the case's expected
// value (from Case.Expected[Var]) using canonical JSON comparison. Values
// of any JSON type compare correctly regardless of decode types.
type VarEquals struct {
	// Var is the envelope variable to assert on.
	Var string
}

// Name implements Grader.
func (g VarEquals) Name() string { return fmt.Sprintf("var_equals:%s", g.Var) }

// Grade implements Grader.
func (g VarEquals) Grade(ctx context.Context, eval CaseEvaluation) (Grade, error) {
	expected, ok := eval.Case.Expected[g.Var]
	if !ok {
		return Grade{}, fmt.Errorf("case %q has no expected value for var %q", eval.Case.ID, g.Var)
	}

	if eval.Final == nil {
		return Grade{Score: 0, Reason: fmt.Sprintf("no run output for var %q", g.Var)}, nil
	}
	actual, present := eval.Final.GetVar(g.Var)
	if !present {
		return Grade{Score: 0, Reason: fmt.Sprintf("var %q not set", g.Var)}, nil
	}

	aJSON, errA := json.Marshal(actual)
	eJSON, errE := json.Marshal(expected)
	if errA != nil || errE != nil {
		return Grade{}, fmt.Errorf("marshal comparison values: %v / %v", errA, errE)
	}
	if string(aJSON) == string(eJSON) {
		return Grade{Score: 1, Passed: true, Reason: fmt.Sprintf("%s matches expected", g.Var)}, nil
	}
	return Grade{
		Score:  0,
		Reason: fmt.Sprintf("%s = %s, want %s", g.Var, aJSON, eJSON),
	}, nil
}

// Exact asserts that an envelope variable equals a fixed value, independent
// of the case's expectations.
type Exact struct {
	Var   string
	Value any
}

// Name implements Grader.
func (g Exact) Name() string { return fmt.Sprintf("exact:%s", g.Var) }

// Grade implements Grader.
func (g Exact) Grade(ctx context.Context, eval CaseEvaluation) (Grade, error) {
	if eval.Final == nil {
		return Grade{Score: 0, Reason: fmt.Sprintf("no run output for var %q", g.Var)}, nil
	}
	v, ok := eval.Final.GetVar(g.Var)
	if !ok {
		return Grade{Score: 0, Reason: fmt.Sprintf("var %q not set", g.Var)}, nil
	}

	aJSON, errA := json.Marshal(v)
	eJSON, errE := json.Marshal(g.Value)
	if errA != nil || errE != nil {
		return Grade{}, fmt.Errorf("marshal comparison values: %v / %v", errA, errE)
	}
	if string(aJSON) == string(eJSON) {
		return Grade{Score: 1, Passed: true}, nil
	}
	return Grade{Score: 0, Reason: fmt.Sprintf("%s = %s, want %s", g.Var, aJSON, eJSON)}, nil
}

// Contains asserts that an envelope variable's text contains a substring.
type Contains struct {
	Var       string
	Substring string
}

// Name implements Grader.
func (g Contains) Name() string { return fmt.Sprintf("contains:%s", g.Var) }

// Grade implements Grader.
func (g Contains) Grade(ctx context.Context, eval CaseEvaluation) (Grade, error) {
	s, ok := varString(eval.Final, g.Var)
	if !ok {
		return Grade{Score: 0, Reason: fmt.Sprintf("var %q not set", g.Var)}, nil
	}
	if strings.Contains(s, g.Substring) {
		return Grade{Score: 1, Passed: true}, nil
	}
	return Grade{Score: 0, Reason: fmt.Sprintf("%s does not contain %q", g.Var, g.Substring)}, nil
}

// Regex asserts that an envelope variable's text matches a pattern.
type Regex struct {
	Var     string
	Pattern string
}

// Name implements Grader.
func (g Regex) Name() string { return fmt.Sprintf("regex:%s", g.Var) }

// Grade implements Grader.
func (g Regex) Grade(ctx context.Context, eval CaseEvaluation) (Grade, error) {
	pattern, err := regexp.Compile(g.Pattern)
	if err != nil {
		return Grade{}, fmt.Errorf("invalid pattern %q: %w", g.Pattern, err)
	}

	s, ok := varString(eval.Final, g.Var)
	if !ok {
		return Grade{Score: 0, Reason: fmt.Sprintf("var %q not set", g.Var)}, nil
	}
	if pattern.MatchString(s) {
		return Grade{Score: 1, Passed: true}, nil
	}
	return Grade{Score: 0, Reason: fmt.Sprintf("%s does not match %q", g.Var, g.Pattern)}, nil
}

// Schema asserts that an envelope variable validates against a JSON Schema.
type Schema struct {
	Var    string
	Schema map[string]any
}

// Name implements Grader.
func (g Schema) Name() string { return fmt.Sprintf("schema:%s", g.Var) }

// Grade implements Grader.
func (g Schema) Grade(ctx context.Context, eval CaseEvaluation) (Grade, error) {
	schema, err := compileSchema(g.Schema)
	if err != nil {
		return Grade{}, fmt.Errorf("invalid schema: %w", err)
	}

	if eval.Final == nil {
		return Grade{Score: 0, Reason: fmt.Sprintf("no run output for var %q", g.Var)}, nil
	}
	v, ok := eval.Final.GetVar(g.Var)
	if !ok {
		return Grade{Score: 0, Reason: fmt.Sprintf("var %q not set", g.Var)}, nil
	}
	if err := schema.Validate(v); err != nil {
		return Grade{Score: 0, Reason: fmt.Sprintf("%s fails schema: %v", g.Var, err)}, nil
	}
	return Grade{Score: 1, Passed: true}, nil
}

// Threshold asserts that a numeric envelope variable falls within
// [Min, Max]. Min and Max are inclusive; the zero-value bounds mean
// unbounded on that side. Use pointers via NewThreshold to require an
// explicit bound.
type Threshold struct {
	Var string
	Min float64
	Max float64
}

// NewThreshold creates a bounded numeric assertion with explicit inclusive
// bounds (nil means unbounded on that side).
func NewThreshold(v string, min, max *float64) Threshold {
	t := Threshold{Var: v}
	if min != nil {
		t.Min = *min
	} else {
		t.Min = unbounded
	}
	if max != nil {
		t.Max = *max
	} else {
		t.Max = unbounded
	}
	return t
}

const unbounded = -(1 << 62)

// Name implements Grader.
func (g Threshold) Name() string { return fmt.Sprintf("threshold:%s", g.Var) }

// Grade implements Grader.
func (g Threshold) Grade(ctx context.Context, eval CaseEvaluation) (Grade, error) {
	if eval.Final == nil {
		return Grade{Score: 0, Reason: fmt.Sprintf("no run output for var %q", g.Var)}, nil
	}
	v, ok := eval.Final.GetVar(g.Var)
	if !ok {
		return Grade{Score: 0, Reason: fmt.Sprintf("var %q not set", g.Var)}, nil
	}

	number, ok := toFloat(v)
	if !ok {
		return Grade{Score: 0, Reason: fmt.Sprintf("%s = %v is not numeric", g.Var, v)}, nil
	}
	if number < g.Min || number > g.Max {
		return Grade{Score: 0, Reason: fmt.Sprintf("%s = %v outside [%v, %v]", g.Var, number, g.Min, g.Max)}, nil
	}
	return Grade{Score: 1, Passed: true}, nil
}

// compileSchema compiles a JSON Schema map using the same library as the
// LLM node's structured output validation.
func compileSchema(schema map[string]any) (*jsonschema.Schema, error) {
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	const loc = "petalflow://eval-grader/schema"
	if err := compiler.AddResource(loc, doc); err != nil {
		return nil, err
	}
	return compiler.Compile(loc)
}

// toFloat coerces JSON-decoded numeric values to float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
