package petalflow

import (
	"context"
	"errors"
	"testing"
)

func TestNewGateNode(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		node := NewGateNode("gate1", GateNodeConfig{
			Condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return true, nil
			},
		})

		if node.ID() != "gate1" {
			t.Errorf("expected ID 'gate1', got %q", node.ID())
		}
		if node.Kind() != NodeKindGate {
			t.Errorf("expected kind %v, got %v", NodeKindGate, node.Kind())
		}

		config := node.Config()
		if config.OnFail != GateActionBlock {
			t.Errorf("expected default OnFail %q, got %q", GateActionBlock, config.OnFail)
		}
		if config.FailMessage != "gate condition failed" {
			t.Errorf("expected default FailMessage, got %q", config.FailMessage)
		}
	})

	t.Run("custom config", func(t *testing.T) {
		node := NewGateNode("custom", GateNodeConfig{
			ConditionVar:   "is_valid",
			OnFail:         GateActionSkip,
			FailMessage:    "validation failed",
			RedirectNodeID: "error_handler",
			ResultVar:      "gate_result",
		})

		config := node.Config()
		if config.ConditionVar != "is_valid" {
			t.Errorf("expected ConditionVar 'is_valid', got %q", config.ConditionVar)
		}
		if config.OnFail != GateActionSkip {
			t.Errorf("expected OnFail %q, got %q", GateActionSkip, config.OnFail)
		}
		if config.FailMessage != "validation failed" {
			t.Errorf("expected FailMessage 'validation failed', got %q", config.FailMessage)
		}
		if config.RedirectNodeID != "error_handler" {
			t.Errorf("expected RedirectNodeID 'error_handler', got %q", config.RedirectNodeID)
		}
		if config.ResultVar != "gate_result" {
			t.Errorf("expected ResultVar 'gate_result', got %q", config.ResultVar)
		}
	})
}

func TestGateNode_Run_FunctionCondition(t *testing.T) {
	t.Run("condition passes", func(t *testing.T) {
		node := NewGateNode("pass", GateNodeConfig{
			Condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return true, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("data", "test")

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Original data should be preserved
		data, ok := result.GetVar("data")
		if !ok || data != "test" {
			t.Errorf("expected data='test', got %v", data)
		}
	})

	t.Run("condition fails with block", func(t *testing.T) {
		node := NewGateNode("block", GateNodeConfig{
			Condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return false, nil
			},
			FailMessage: "access denied",
		})

		env := NewEnvelope()

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error when gate blocks")
		}
		if err.Error() != "gate node block: access denied" {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("condition evaluates envelope data", func(t *testing.T) {
		node := NewGateNode("check", GateNodeConfig{
			Condition: func(ctx context.Context, env *Envelope) (bool, error) {
				score, ok := env.GetVar("score")
				if !ok {
					return false, nil
				}
				return score.(float64) >= 0.5, nil
			},
		})

		t.Run("passes when score is high", func(t *testing.T) {
			env := NewEnvelope()
			env.SetVar("score", 0.8)

			result, err := node.Run(context.Background(), env)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected result envelope")
			}
		})

		t.Run("blocks when score is low", func(t *testing.T) {
			env := NewEnvelope()
			env.SetVar("score", 0.3)

			_, err := node.Run(context.Background(), env)
			if err == nil {
				t.Fatal("expected error when score is low")
			}
		})
	})

	t.Run("condition returns error", func(t *testing.T) {
		node := NewGateNode("error", GateNodeConfig{
			Condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return false, errors.New("condition evaluation failed")
			},
		})

		env := NewEnvelope()

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "gate node error: condition error: condition evaluation failed" {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestGateNode_Run_ConditionVar(t *testing.T) {
	t.Run("variable exists and is true", func(t *testing.T) {
		node := NewGateNode("varGate", GateNodeConfig{
			ConditionVar: "is_authorized",
		})

		env := NewEnvelope()
		env.SetVar("is_authorized", true)

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected result envelope")
		}
	})

	t.Run("variable exists and is false", func(t *testing.T) {
		node := NewGateNode("varGate", GateNodeConfig{
			ConditionVar: "is_authorized",
		})

		env := NewEnvelope()
		env.SetVar("is_authorized", false)

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error when condition var is false")
		}
	})

	t.Run("variable does not exist", func(t *testing.T) {
		node := NewGateNode("missing", GateNodeConfig{
			ConditionVar: "nonexistent",
		})

		env := NewEnvelope()

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error when condition var is missing")
		}
	})

	t.Run("condition function takes precedence over var", func(t *testing.T) {
		node := NewGateNode("precedence", GateNodeConfig{
			Condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return true, nil // Always passes
			},
			ConditionVar: "is_authorized", // This is false
		})

		env := NewEnvelope()
		env.SetVar("is_authorized", false)

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected result - function should take precedence")
		}
	})
}

func TestGateNode_Run_Actions(t *testing.T) {
	t.Run("block action", func(t *testing.T) {
		node := NewGateNode("blocker", GateNodeConfig{
			Condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return false, nil
			},
			OnFail:      GateActionBlock,
			FailMessage: "blocked by gate",
		})

		env := NewEnvelope()

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected blocking error")
		}
	})

	t.Run("skip action", func(t *testing.T) {
		node := NewGateNode("skipper", GateNodeConfig{
			Condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return false, nil
			},
			OnFail: GateActionSkip,
		})

		env := NewEnvelope()
		env.SetVar("data", "preserved")

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("skip action should not return error: %v", err)
		}

		data, _ := result.GetVar("data")
		if data != "preserved" {
			t.Errorf("expected data to be preserved on skip")
		}
	})

	t.Run("redirect action", func(t *testing.T) {
		node := NewGateNode("redirector", GateNodeConfig{
			Condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return false, nil
			},
			OnFail:         GateActionRedirect,
			RedirectNodeID: "error_handler",
		})

		env := NewEnvelope()

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("redirect action should not return error: %v", err)
		}

		redirect, ok := result.GetVar("__gate_redirect__")
		if !ok {
			t.Fatal("expected redirect hint in envelope")
		}
		if redirect != "error_handler" {
			t.Errorf("expected redirect to 'error_handler', got %v", redirect)
		}
	})

	t.Run("redirect without node ID fails", func(t *testing.T) {
		node := NewGateNode("badRedirect", GateNodeConfig{
			Condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return false, nil
			},
			OnFail: GateActionRedirect,
			// RedirectNodeID not set
		})

		env := NewEnvelope()

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for redirect without node ID")
		}
	})
}

func TestGateNode_Run_ResultVar(t *testing.T) {
	t.Run("stores pass result", func(t *testing.T) {
		node := NewGateNode("resultPass", GateNodeConfig{
			Condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return true, nil
			},
			ResultVar: "gate_result",
		})

		env := NewEnvelope()

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gateResult, ok := result.GetVar("gate_result")
		if !ok {
			t.Fatal("expected gate_result variable")
		}

		gr := gateResult.(GateResult)
		if !gr.Passed {
			t.Error("expected Passed=true")
		}
	})

	t.Run("stores fail result with skip action", func(t *testing.T) {
		node := NewGateNode("resultFail", GateNodeConfig{
			Condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return false, nil
			},
			OnFail:    GateActionSkip,
			ResultVar: "gate_result",
		})

		env := NewEnvelope()

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gateResult, ok := result.GetVar("gate_result")
		if !ok {
			t.Fatal("expected gate_result variable")
		}

		gr := gateResult.(GateResult)
		if gr.Passed {
			t.Error("expected Passed=false")
		}
		if gr.Action != "skip" {
			t.Errorf("expected Action='skip', got %q", gr.Action)
		}
	})
}

func TestGateNode_Run_NoCondition(t *testing.T) {
	t.Run("always passes when no condition", func(t *testing.T) {
		node := NewGateNode("noCondition", GateNodeConfig{
			// No Condition or ConditionVar
		})

		env := NewEnvelope()
		env.SetVar("data", "test")

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected result envelope")
		}

		data, _ := result.GetVar("data")
		if data != "test" {
			t.Errorf("expected data preserved")
		}
	})
}

func TestGateNode_EnvelopeIsolation(t *testing.T) {
	t.Run("original envelope not modified", func(t *testing.T) {
		node := NewGateNode("isolated", GateNodeConfig{
			Condition: func(ctx context.Context, env *Envelope) (bool, error) {
				return true, nil
			},
			ResultVar: "gate_result",
		})

		env := NewEnvelope()
		env.SetVar("original", "value")

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Original envelope should not have gate_result
		_, hasResult := env.GetVar("gate_result")
		if hasResult {
			t.Error("original envelope should not have gate_result")
		}

		// Result envelope should have both
		_, hasOriginal := result.GetVar("original")
		if !hasOriginal {
			t.Error("result envelope should preserve original vars")
		}
	})
}

func TestIsTruthy(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected bool
	}{
		{"nil", nil, false},
		{"true", true, true},
		{"false", false, false},
		{"int zero", 0, false},
		{"int nonzero", 42, true},
		{"int64 zero", int64(0), false},
		{"int64 nonzero", int64(42), true},
		{"float64 zero", 0.0, false},
		{"float64 nonzero", 3.14, true},
		{"empty string", "", false},
		{"non-empty string", "hello", true},
		{"empty slice", []any{}, false},
		{"non-empty slice", []any{1, 2, 3}, true},
		{"empty map", map[string]any{}, false},
		{"non-empty map", map[string]any{"key": "value"}, true},
		{"struct", struct{ Name string }{Name: "test"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTruthy(tt.value)
			if result != tt.expected {
				t.Errorf("isTruthy(%v) = %v, expected %v", tt.value, result, tt.expected)
			}
		})
	}
}
