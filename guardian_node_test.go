package petalflow

import (
	"context"
	"errors"
	"testing"
)

func TestNewGuardianNode(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		node := NewGuardianNode("guardian1", GuardianNodeConfig{
			InputVar: "data",
			Checks:   []GuardianCheck{{Name: "test", Type: GuardianCheckRequired}},
		})

		if node.ID() != "guardian1" {
			t.Errorf("expected ID 'guardian1', got %q", node.ID())
		}
		if node.Kind() != NodeKindGuardian {
			t.Errorf("expected kind %v, got %v", NodeKindGuardian, node.Kind())
		}

		config := node.Config()
		if config.OnFail != GuardianActionFail {
			t.Errorf("expected default OnFail %q, got %q", GuardianActionFail, config.OnFail)
		}
		if config.FailMessage != "validation failed" {
			t.Errorf("expected default FailMessage, got %q", config.FailMessage)
		}
	})
}

func TestGuardianNode_Required(t *testing.T) {
	t.Run("passes when field exists and non-empty", func(t *testing.T) {
		node := NewGuardianNode("required", GuardianNodeConfig{
			InputVar: "user",
			Checks: []GuardianCheck{
				{Name: "name_check", Type: GuardianCheckRequired, Field: "name"},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("user", map[string]any{"name": "John", "age": 30})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if !gr.Passed {
			t.Errorf("expected validation to pass")
		}
	})

	t.Run("fails when field is missing", func(t *testing.T) {
		node := NewGuardianNode("required", GuardianNodeConfig{
			InputVar: "user",
			Checks: []GuardianCheck{
				{Name: "email_check", Type: GuardianCheckRequired, Field: "email"},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("user", map[string]any{"name": "John"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail")
		}
		if len(gr.Failures) != 1 {
			t.Errorf("expected 1 failure, got %d", len(gr.Failures))
		}
	})

	t.Run("fails when field is empty string", func(t *testing.T) {
		node := NewGuardianNode("required", GuardianNodeConfig{
			InputVar: "user",
			Checks: []GuardianCheck{
				{Name: "name_check", Type: GuardianCheckRequired, Field: "name"},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("user", map[string]any{"name": ""})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail for empty string")
		}
	})

	t.Run("checks multiple required fields", func(t *testing.T) {
		node := NewGuardianNode("requiredMulti", GuardianNodeConfig{
			InputVar: "user",
			Checks: []GuardianCheck{
				{
					Name:           "required_fields",
					Type:           GuardianCheckRequired,
					RequiredFields: []string{"name", "email", "age"},
				},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("user", map[string]any{"name": "John"}) // Missing email and age

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail")
		}
		if len(gr.Failures) != 2 {
			t.Errorf("expected 2 failures, got %d", len(gr.Failures))
		}
	})
}

func TestGuardianNode_Length(t *testing.T) {
	t.Run("max length passes", func(t *testing.T) {
		node := NewGuardianNode("maxLen", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "name_length", Type: GuardianCheckMaxLength, Field: "name", MaxLength: 50},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"name": "John"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if !gr.Passed {
			t.Errorf("expected validation to pass")
		}
	})

	t.Run("max length fails", func(t *testing.T) {
		node := NewGuardianNode("maxLen", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "name_length", Type: GuardianCheckMaxLength, Field: "name", MaxLength: 3},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"name": "John"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail")
		}
		if gr.Failures[0].Actual != 4 {
			t.Errorf("expected actual length 4, got %v", gr.Failures[0].Actual)
		}
	})

	t.Run("min length passes", func(t *testing.T) {
		node := NewGuardianNode("minLen", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "name_length", Type: GuardianCheckMinLength, Field: "name", MinLength: 3},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"name": "John"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if !gr.Passed {
			t.Errorf("expected validation to pass")
		}
	})

	t.Run("min length fails", func(t *testing.T) {
		node := NewGuardianNode("minLen", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "name_length", Type: GuardianCheckMinLength, Field: "name", MinLength: 10},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"name": "John"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail")
		}
	})

	t.Run("checks array length", func(t *testing.T) {
		node := NewGuardianNode("arrayLen", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "tags_length", Type: GuardianCheckMaxLength, Field: "tags", MaxLength: 3},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"tags": []any{"a", "b", "c", "d", "e"}})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail for array length 5 > 3")
		}
	})
}

func TestGuardianNode_Pattern(t *testing.T) {
	t.Run("pattern matches", func(t *testing.T) {
		node := NewGuardianNode("pattern", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "email_format", Type: GuardianCheckPattern, Field: "email", Pattern: `^[a-z]+@[a-z]+\.[a-z]+$`},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"email": "john@example.com"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if !gr.Passed {
			t.Errorf("expected validation to pass")
		}
	})

	t.Run("pattern does not match", func(t *testing.T) {
		node := NewGuardianNode("pattern", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "email_format", Type: GuardianCheckPattern, Field: "email", Pattern: `^[a-z]+@[a-z]+\.[a-z]+$`},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"email": "invalid-email"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail")
		}
	})

	t.Run("error for invalid pattern", func(t *testing.T) {
		node := NewGuardianNode("badPattern", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "bad", Type: GuardianCheckPattern, Field: "text", Pattern: `[invalid`},
			},
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"text": "test"})

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for invalid pattern")
		}
	})
}

func TestGuardianNode_Enum(t *testing.T) {
	t.Run("value in enum", func(t *testing.T) {
		node := NewGuardianNode("enum", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "status_check", Type: GuardianCheckEnum, Field: "status", AllowedValues: []any{"pending", "active", "completed"}},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"status": "active"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if !gr.Passed {
			t.Errorf("expected validation to pass")
		}
	})

	t.Run("value not in enum", func(t *testing.T) {
		node := NewGuardianNode("enum", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "status_check", Type: GuardianCheckEnum, Field: "status", AllowedValues: []any{"pending", "active", "completed"}},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"status": "invalid"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail")
		}
	})

	t.Run("numeric enum", func(t *testing.T) {
		node := NewGuardianNode("numEnum", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "priority_check", Type: GuardianCheckEnum, Field: "priority", AllowedValues: []any{1, 2, 3}},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"priority": 2})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if !gr.Passed {
			t.Errorf("expected validation to pass for numeric enum")
		}
	})
}

func TestGuardianNode_Type(t *testing.T) {
	t.Run("correct type", func(t *testing.T) {
		node := NewGuardianNode("type", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "name_type", Type: GuardianCheckType_, Field: "name", ExpectedType: "string"},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"name": "John"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if !gr.Passed {
			t.Errorf("expected validation to pass")
		}
	})

	t.Run("incorrect type", func(t *testing.T) {
		node := NewGuardianNode("type", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "age_type", Type: GuardianCheckType_, Field: "age", ExpectedType: "string"},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"age": 30}) // number, not string

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail")
		}
	})

	t.Run("checks array type", func(t *testing.T) {
		node := NewGuardianNode("arrayType", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "tags_type", Type: GuardianCheckType_, Field: "tags", ExpectedType: "array"},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"tags": []any{"a", "b"}})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if !gr.Passed {
			t.Errorf("expected validation to pass for array type")
		}
	})
}

func TestGuardianNode_Range(t *testing.T) {
	t.Run("value in range", func(t *testing.T) {
		min, max := 0.0, 100.0
		node := NewGuardianNode("range", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "score_range", Type: GuardianCheckRange, Field: "score", Min: &min, Max: &max},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"score": 75.5})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if !gr.Passed {
			t.Errorf("expected validation to pass")
		}
	})

	t.Run("value below minimum", func(t *testing.T) {
		min := 0.0
		node := NewGuardianNode("range", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "score_range", Type: GuardianCheckRange, Field: "score", Min: &min},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"score": -5.0})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail for value below minimum")
		}
	})

	t.Run("value above maximum", func(t *testing.T) {
		max := 100.0
		node := NewGuardianNode("range", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "score_range", Type: GuardianCheckRange, Field: "score", Max: &max},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"score": 150.0})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail for value above maximum")
		}
	})
}

func TestGuardianNode_PII(t *testing.T) {
	t.Run("detects SSN", func(t *testing.T) {
		node := NewGuardianNode("pii", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "pii_check", Type: GuardianCheckPII, Field: "text", PIITypes: []PIIType{PIITypeSSN}, BlockPII: true},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"text": "My SSN is 123-45-6789"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected PII detection to fail")
		}
		if gr.Failures[0].PIIType != "ssn" {
			t.Errorf("expected PII type 'ssn', got %q", gr.Failures[0].PIIType)
		}
	})

	t.Run("detects email", func(t *testing.T) {
		node := NewGuardianNode("pii", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "pii_check", Type: GuardianCheckPII, Field: "text", PIITypes: []PIIType{PIITypeEmail}, BlockPII: true},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"text": "Contact me at john@example.com"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected PII detection to fail")
		}
	})

	t.Run("detects phone number", func(t *testing.T) {
		node := NewGuardianNode("pii", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "pii_check", Type: GuardianCheckPII, Field: "text", PIITypes: []PIIType{PIITypePhone}, BlockPII: true},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"text": "Call me at 555-123-4567"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected PII detection to fail")
		}
	})

	t.Run("detects credit card", func(t *testing.T) {
		node := NewGuardianNode("pii", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "pii_check", Type: GuardianCheckPII, Field: "text", PIITypes: []PIIType{PIITypeCreditCard}, BlockPII: true},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"text": "Card: 4111-1111-1111-1111"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected PII detection to fail")
		}
	})

	t.Run("passes when no PII", func(t *testing.T) {
		node := NewGuardianNode("pii", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "pii_check", Type: GuardianCheckPII, Field: "text", BlockPII: true},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"text": "Hello, this is a clean message"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if !gr.Passed {
			t.Errorf("expected validation to pass with no PII")
		}
	})

	t.Run("does not block when BlockPII is false", func(t *testing.T) {
		node := NewGuardianNode("pii", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "pii_check", Type: GuardianCheckPII, Field: "text", BlockPII: false},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"text": "My SSN is 123-45-6789"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if !gr.Passed {
			t.Errorf("expected validation to pass when BlockPII is false")
		}
	})
}

func TestGuardianNode_Schema(t *testing.T) {
	t.Run("validates simple schema", func(t *testing.T) {
		node := NewGuardianNode("schema", GuardianNodeConfig{
			InputVar: "user",
			Checks: []GuardianCheck{
				{
					Name: "user_schema",
					Type: GuardianCheckSchema,
					Schema: map[string]any{
						"type":     "object",
						"required": []any{"name", "email"},
						"properties": map[string]any{
							"name":  map[string]any{"type": "string", "minLength": 1.0},
							"email": map[string]any{"type": "string"},
							"age":   map[string]any{"type": "number", "minimum": 0.0},
						},
					},
				},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("user", map[string]any{
			"name":  "John",
			"email": "john@example.com",
			"age":   30,
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if !gr.Passed {
			t.Errorf("expected validation to pass: %v", gr.Failures)
		}
	})

	t.Run("fails for missing required", func(t *testing.T) {
		node := NewGuardianNode("schema", GuardianNodeConfig{
			InputVar: "user",
			Checks: []GuardianCheck{
				{
					Name: "user_schema",
					Type: GuardianCheckSchema,
					Schema: map[string]any{
						"type":     "object",
						"required": []any{"name", "email"},
					},
				},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("user", map[string]any{"name": "John"}) // Missing email

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail for missing required field")
		}
	})

	t.Run("fails for wrong type", func(t *testing.T) {
		node := NewGuardianNode("schema", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{
					Name: "type_check",
					Type: GuardianCheckSchema,
					Schema: map[string]any{
						"type": "string",
					},
				},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", 123) // number, not string

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail for wrong type")
		}
	})
}

func TestGuardianNode_Custom(t *testing.T) {
	t.Run("custom check passes", func(t *testing.T) {
		node := NewGuardianNode("custom", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{
					Name:  "custom_check",
					Type:  GuardianCheckCustom,
					Field: "value",
					CustomFunc: func(ctx context.Context, value any, env *Envelope) (bool, string, error) {
						num, ok := value.(int)
						if !ok {
							return false, "expected int", nil
						}
						return num > 0, "must be positive", nil
					},
				},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"value": 42})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if !gr.Passed {
			t.Errorf("expected validation to pass")
		}
	})

	t.Run("custom check fails", func(t *testing.T) {
		node := NewGuardianNode("custom", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{
					Name:  "custom_check",
					Type:  GuardianCheckCustom,
					Field: "value",
					CustomFunc: func(ctx context.Context, value any, env *Envelope) (bool, string, error) {
						num, _ := value.(int)
						return num > 0, "must be positive", nil
					},
				},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"value": -5})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected validation to fail")
		}
		if gr.Failures[0].Message != "must be positive" {
			t.Errorf("expected custom message")
		}
	})

	t.Run("custom check returns error", func(t *testing.T) {
		node := NewGuardianNode("custom", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{
					Name:  "error_check",
					Type:  GuardianCheckCustom,
					Field: "value",
					CustomFunc: func(ctx context.Context, value any, env *Envelope) (bool, string, error) {
						return false, "", errors.New("custom error")
					},
				},
			},
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"value": 1})

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error from custom function")
		}
	})
}

func TestGuardianNode_Actions(t *testing.T) {
	t.Run("fail action returns error", func(t *testing.T) {
		node := NewGuardianNode("fail", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "check", Type: GuardianCheckRequired, Field: "missing"},
			},
			OnFail:      GuardianActionFail,
			FailMessage: "custom error message",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{})

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for fail action")
		}
		if !contains(err.Error(), "custom error message") {
			t.Errorf("expected custom error message in error: %v", err)
		}
	})

	t.Run("skip action passes through", func(t *testing.T) {
		node := NewGuardianNode("skip", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "check", Type: GuardianCheckRequired, Field: "missing"},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"other": "value"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("skip action should not return error: %v", err)
		}

		// Result should show failures but not error
		gr := result.Vars["result"].(GuardianResult)
		if gr.Passed {
			t.Errorf("expected passed=false")
		}
	})

	t.Run("redirect action sets hint", func(t *testing.T) {
		node := NewGuardianNode("redirect", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "check", Type: GuardianCheckRequired, Field: "missing"},
			},
			OnFail:         GuardianActionRedirect,
			RedirectNodeID: "error_handler",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("redirect action should not return error: %v", err)
		}

		redirect, ok := result.GetVar("__guardian_redirect__")
		if !ok {
			t.Fatal("expected redirect hint")
		}
		if redirect != "error_handler" {
			t.Errorf("expected redirect to 'error_handler', got %v", redirect)
		}
	})

	t.Run("redirect without node ID fails", func(t *testing.T) {
		node := NewGuardianNode("badRedirect", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "check", Type: GuardianCheckRequired, Field: "missing"},
			},
			OnFail: GuardianActionRedirect,
			// No RedirectNodeID
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{})

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for redirect without node ID")
		}
	})
}

func TestGuardianNode_StopOnFirstFailure(t *testing.T) {
	t.Run("stops after first failure", func(t *testing.T) {
		node := NewGuardianNode("stopFirst", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "check1", Type: GuardianCheckRequired, Field: "field1"},
				{Name: "check2", Type: GuardianCheckRequired, Field: "field2"},
				{Name: "check3", Type: GuardianCheckRequired, Field: "field3"},
			},
			OnFail:             GuardianActionSkip,
			ResultVar:          "result",
			StopOnFirstFailure: true,
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{}) // All fields missing

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if len(gr.Failures) != 1 {
			t.Errorf("expected 1 failure (stop on first), got %d", len(gr.Failures))
		}
	})

	t.Run("runs all checks when not set", func(t *testing.T) {
		node := NewGuardianNode("runAll", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "check1", Type: GuardianCheckRequired, Field: "field1"},
				{Name: "check2", Type: GuardianCheckRequired, Field: "field2"},
				{Name: "check3", Type: GuardianCheckRequired, Field: "field3"},
			},
			OnFail:             GuardianActionSkip,
			ResultVar:          "result",
			StopOnFirstFailure: false,
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{}) // All fields missing

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if len(gr.Failures) != 3 {
			t.Errorf("expected 3 failures (run all), got %d", len(gr.Failures))
		}
	})
}

func TestGuardianNode_CustomMessage(t *testing.T) {
	t.Run("uses custom message", func(t *testing.T) {
		node := NewGuardianNode("customMsg", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{
					Name:    "email_check",
					Type:    GuardianCheckRequired,
					Field:   "email",
					Message: "Please provide your email address",
				},
			},
			OnFail:    GuardianActionSkip,
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gr := result.Vars["result"].(GuardianResult)
		if gr.Failures[0].Message != "Please provide your email address" {
			t.Errorf("expected custom message, got %q", gr.Failures[0].Message)
		}
	})
}

func TestGuardianNode_EnvelopeIsolation(t *testing.T) {
	t.Run("original envelope not modified", func(t *testing.T) {
		node := NewGuardianNode("isolated", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "check", Type: GuardianCheckRequired, Field: "name"},
			},
			ResultVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"name": "John"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Original should not have result
		_, hasResult := env.GetVar("result")
		if hasResult {
			t.Error("original envelope should not have 'result'")
		}

		// Result should have it
		_, hasResult = result.GetVar("result")
		if !hasResult {
			t.Error("result envelope should have 'result'")
		}
	})
}

func TestGuardianNode_ContextCancellation(t *testing.T) {
	t.Run("respects context cancellation", func(t *testing.T) {
		node := NewGuardianNode("cancel", GuardianNodeConfig{
			InputVar: "data",
			Checks: []GuardianCheck{
				{Name: "check1", Type: GuardianCheckRequired, Field: "field1"},
				{Name: "check2", Type: GuardianCheckRequired, Field: "field2"},
			},
		})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"field1": "value"})

		_, err := node.Run(ctx, env)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})
}

func TestHelperFunctions(t *testing.T) {
	t.Run("isEmpty", func(t *testing.T) {
		tests := []struct {
			value    any
			expected bool
		}{
			{nil, true},
			{"", true},
			{"hello", false},
			{[]any{}, true},
			{[]any{1}, false},
			{map[string]any{}, true},
			{map[string]any{"a": 1}, false},
			{0, false},     // numbers are not "empty"
			{false, false}, // bools are not "empty"
		}

		for _, tt := range tests {
			result := isEmpty(tt.value)
			if result != tt.expected {
				t.Errorf("isEmpty(%v) = %v, expected %v", tt.value, result, tt.expected)
			}
		}
	})

	t.Run("getLength", func(t *testing.T) {
		tests := []struct {
			value    any
			expected int
		}{
			{"hello", 5},
			{[]any{1, 2, 3}, 3},
			{map[string]any{"a": 1, "b": 2}, 2},
		}

		for _, tt := range tests {
			result := getLength(tt.value)
			if result != tt.expected {
				t.Errorf("getLength(%v) = %v, expected %v", tt.value, result, tt.expected)
			}
		}
	})

	t.Run("getTypeString", func(t *testing.T) {
		tests := []struct {
			value    any
			expected string
		}{
			{"hello", "string"},
			{42, "number"},
			{3.14, "number"},
			{true, "bool"},
			{[]any{1, 2}, "array"},
			{map[string]any{"a": 1}, "object"},
			{nil, "null"},
		}

		for _, tt := range tests {
			result := getTypeString(tt.value)
			if result != tt.expected {
				t.Errorf("getTypeString(%v) = %v, expected %v", tt.value, result, tt.expected)
			}
		}
	})
}
