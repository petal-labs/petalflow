package conditional

import (
	"context"
	"testing"

	"github.com/petal-labs/petalflow/core"
)

func TestNewConditionalNode_Defaults(t *testing.T) {
	node, err := NewConditionalNode("test", Config{
		Conditions: []Condition{
			{Name: "a", Expression: "input.x == 1"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.ID() != "test" {
		t.Errorf("ID() = %q, want %q", node.ID(), "test")
	}
	if node.Kind() != core.NodeKindConditional {
		t.Errorf("Kind() = %q, want %q", node.Kind(), core.NodeKindConditional)
	}
	cfg := node.Config()
	if cfg.EvaluationOrder != "first_match" {
		t.Errorf("EvaluationOrder = %q, want %q", cfg.EvaluationOrder, "first_match")
	}
	if cfg.OutputKey != "test_output" {
		t.Errorf("OutputKey = %q, want %q", cfg.OutputKey, "test_output")
	}
}

func TestNewConditionalNode_Validation(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{
			name:    "no conditions",
			config:  Config{},
			wantErr: "at least one condition",
		},
		{
			name: "empty condition name",
			config: Config{
				Conditions: []Condition{{Name: "", Expression: "true"}},
			},
			wantErr: "empty name",
		},
		{
			name: "duplicate condition names",
			config: Config{
				Conditions: []Condition{
					{Name: "a", Expression: "true"},
					{Name: "a", Expression: "false"},
				},
			},
			wantErr: "duplicate condition name",
		},
		{
			name: "empty expression",
			config: Config{
				Conditions: []Condition{{Name: "a", Expression: ""}},
			},
			wantErr: "empty expression",
		},
		{
			name: "invalid expression",
			config: Config{
				Conditions: []Condition{{Name: "a", Expression: "== =="}},
			},
			wantErr: "condition \"a\"",
		},
		{
			name: "invalid evaluation order",
			config: Config{
				Conditions:      []Condition{{Name: "a", Expression: "true"}},
				EvaluationOrder: "random",
			},
			wantErr: "evaluation_order",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewConditionalNode("test", tt.config)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if tt.wantErr != "" && !contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestConditionalNode_FirstMatch(t *testing.T) {
	node, err := NewConditionalNode("route", Config{
		Conditions: []Condition{
			{Name: "high", Expression: "input.score >= 0.8"},
			{Name: "medium", Expression: "input.score >= 0.5"},
			{Name: "low", Expression: "input.score >= 0"},
		},
		PassThrough: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name       string
		score      float64
		wantTarget string
	}{
		{"high score", 0.9, "high"},
		{"medium score", 0.6, "medium"},
		{"low score", 0.1, "low"},
		{"boundary high", 0.8, "high"},
		{"boundary medium", 0.5, "medium"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := core.NewEnvelope()
			env.SetVar("input", map[string]any{"score": tt.score})

			result, err := node.Run(context.Background(), env)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			decisionVal, ok := result.GetVar("route_decision")
			if !ok {
				t.Fatal("decision not stored in envelope")
			}
			decision, ok := decisionVal.(core.RouteDecision)
			if !ok {
				t.Fatalf("decision is %T, want RouteDecision", decisionVal)
			}
			if len(decision.Targets) != 1 {
				t.Fatalf("Targets = %v, want 1 target", decision.Targets)
			}
			if decision.Targets[0] != tt.wantTarget {
				t.Errorf("Target = %q, want %q", decision.Targets[0], tt.wantTarget)
			}
		})
	}
}

func TestConditionalNode_AllMode(t *testing.T) {
	node, err := NewConditionalNode("fanout", Config{
		Conditions: []Condition{
			{Name: "notify_slack", Expression: "input.score >= 0.5"},
			{Name: "notify_email", Expression: "input.urgent == true"},
			{Name: "log_only", Expression: "true"},
		},
		EvaluationOrder: "all",
		PassThrough:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	env := core.NewEnvelope()
	env.SetVar("input", map[string]any{
		"score":  0.9,
		"urgent": true,
	})

	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decisionVal, ok := result.GetVar("fanout_decision")
	if !ok {
		t.Fatal("decision not stored in envelope")
	}
	decision := decisionVal.(core.RouteDecision)

	if len(decision.Targets) != 3 {
		t.Fatalf("Targets = %v, want 3 targets", decision.Targets)
	}
	want := map[string]bool{"notify_slack": true, "notify_email": true, "log_only": true}
	for _, target := range decision.Targets {
		if !want[target] {
			t.Errorf("unexpected target %q", target)
		}
	}
}

func TestConditionalNode_DefaultBranch(t *testing.T) {
	node, err := NewConditionalNode("route", Config{
		Conditions: []Condition{
			{Name: "approved", Expression: "input.status == \"approved\""},
		},
		Default:     "rejected",
		PassThrough: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	env := core.NewEnvelope()
	env.SetVar("input", map[string]any{"status": "pending"})

	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decision := result.Vars["route_decision"].(core.RouteDecision)
	if len(decision.Targets) != 1 || decision.Targets[0] != "rejected" {
		t.Errorf("Targets = %v, want [rejected]", decision.Targets)
	}
}

func TestConditionalNode_NoMatchNoDefault(t *testing.T) {
	node, err := NewConditionalNode("route", Config{
		Conditions: []Condition{
			{Name: "approved", Expression: "input.status == \"approved\""},
		},
		PassThrough: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	env := core.NewEnvelope()
	env.SetVar("input", map[string]any{"status": "pending"})

	_, err = node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected error when no conditions match and no default")
	}
	if !contains(err.Error(), "no conditions matched") {
		t.Errorf("error = %q, want to contain 'no conditions matched'", err.Error())
	}
}

func TestConditionalNode_PassThroughFalse(t *testing.T) {
	node, err := NewConditionalNode("route", Config{
		Conditions: []Condition{
			{Name: "yes", Expression: "true"},
		},
		PassThrough: false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	env := core.NewEnvelope()
	env.SetVar("input", map[string]any{"data": "test"})

	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outputVal, ok := result.GetVar("route_output")
	if !ok {
		t.Fatal("output not stored in envelope")
	}
	output, ok := outputVal.(map[string]any)
	if !ok {
		t.Fatalf("output is %T, want map[string]any", outputVal)
	}
	if output["matched"] != true {
		t.Errorf("matched = %v, want true", output["matched"])
	}
	if output["condition"] != "yes" {
		t.Errorf("condition = %v, want 'yes'", output["condition"])
	}
}

func TestConditionalNode_ComplexExpressions(t *testing.T) {
	node, err := NewConditionalNode("complex", Config{
		Conditions: []Condition{
			{
				Name:       "priority",
				Expression: `input.score >= 0.8 && input.source != "test"`,
			},
			{
				Name:       "has_tags",
				Expression: `input.event_type in ["payment.succeeded", "payment.failed"]`,
			},
		},
		Default:     "other",
		PassThrough: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Run("first condition matches", func(t *testing.T) {
		env := core.NewEnvelope()
		env.SetVar("input", map[string]any{
			"score":  0.9,
			"source": "production",
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		decision := result.Vars["complex_decision"].(core.RouteDecision)
		if decision.Targets[0] != "priority" {
			t.Errorf("Target = %q, want %q", decision.Targets[0], "priority")
		}
	})

	t.Run("second condition matches", func(t *testing.T) {
		env := core.NewEnvelope()
		env.SetVar("input", map[string]any{
			"score":      0.3,
			"event_type": "payment.succeeded",
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		decision := result.Vars["complex_decision"].(core.RouteDecision)
		if decision.Targets[0] != "has_tags" {
			t.Errorf("Target = %q, want %q", decision.Targets[0], "has_tags")
		}
	})

	t.Run("default", func(t *testing.T) {
		env := core.NewEnvelope()
		env.SetVar("input", map[string]any{
			"score":      0.3,
			"event_type": "order.created",
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		decision := result.Vars["complex_decision"].(core.RouteDecision)
		if decision.Targets[0] != "other" {
			t.Errorf("Target = %q, want %q", decision.Targets[0], "other")
		}
	})
}

func TestConditionalNode_InterfaceCompliance(t *testing.T) {
	var _ core.Node = (*ConditionalNode)(nil)
	var _ core.RouterNode = (*ConditionalNode)(nil)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
