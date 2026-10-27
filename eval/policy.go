package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// GraderConfig is the declarative form of a built-in grader, used by
// policy files. Type selects the grader; the remaining fields configure it.
type GraderConfig struct {
	// Type is one of: var_equals, exact, contains, regex, schema, threshold.
	Type string `json:"type"`
	// Var is the envelope variable to assert on.
	Var string `json:"var,omitempty"`
	// Value is the fixed expectation for exact.
	Value any `json:"value,omitempty"`
	// Substring is the expectation for contains.
	Substring string `json:"substring,omitempty"`
	// Pattern is the expectation for regex.
	Pattern string `json:"pattern,omitempty"`
	// Schema is the expectation for schema.
	Schema map[string]any `json:"schema,omitempty"`
	// Min and Max are the bounds for threshold.
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

// Build constructs the grader.
func (c GraderConfig) Build() (Grader, error) {
	switch c.Type {
	case "var_equals":
		if c.Var == "" {
			return nil, fmt.Errorf("eval: var_equals grader requires var")
		}
		return VarEquals{Var: c.Var}, nil
	case "exact":
		if c.Var == "" {
			return nil, fmt.Errorf("eval: exact grader requires var")
		}
		return Exact{Var: c.Var, Value: c.Value}, nil
	case "contains":
		if c.Var == "" || c.Substring == "" {
			return nil, fmt.Errorf("eval: contains grader requires var and substring")
		}
		return Contains{Var: c.Var, Substring: c.Substring}, nil
	case "regex":
		if c.Var == "" || c.Pattern == "" {
			return nil, fmt.Errorf("eval: regex grader requires var and pattern")
		}
		return Regex{Var: c.Var, Pattern: c.Pattern}, nil
	case "schema":
		if c.Var == "" || c.Schema == nil {
			return nil, fmt.Errorf("eval: schema grader requires var and schema")
		}
		return Schema{Var: c.Var, Schema: c.Schema}, nil
	case "threshold":
		if c.Var == "" {
			return nil, fmt.Errorf("eval: threshold grader requires var")
		}
		return NewThreshold(c.Var, c.Min, c.Max), nil
	default:
		return nil, fmt.Errorf("eval: unknown grader type %q", c.Type)
	}
}

// Policy is the declarative evaluation policy: which graders to run and
// which gates to enforce. It is the file format consumed by the CLI's eval
// command; library callers construct graders and Gates directly.
type Policy struct {
	Graders []GraderConfig `json:"graders"`
	Gates   Gates          `json:"gates"`
}

// LoadPolicy reads and validates a policy file, compiling its grader list.
func LoadPolicy(path string) (*Policy, []Grader, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path from caller
	if err != nil {
		return nil, nil, fmt.Errorf("eval: read policy: %w", err)
	}

	var policy Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		return nil, nil, fmt.Errorf("eval: decode policy: %w", err)
	}
	if len(policy.Graders) == 0 {
		return nil, nil, fmt.Errorf("eval: policy requires at least one grader")
	}

	graders := make([]Grader, 0, len(policy.Graders))
	for i, cfg := range policy.Graders {
		grader, err := cfg.Build()
		if err != nil {
			return nil, nil, fmt.Errorf("eval: policy graders[%d]: %w", i, err)
		}
		graders = append(graders, grader)
	}
	return &policy, graders, nil
}
