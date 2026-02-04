package nodes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
)

func TestNewRuleRouter(t *testing.T) {
	router := NewRuleRouter("my-router", RuleRouterConfig{
		Rules: []RouteRule{
			{Target: "node-a", Reason: "default"},
		},
	})

	if router.ID() != "my-router" {
		t.Errorf("expected ID 'my-router', got %q", router.ID())
	}
	if router.Kind() != core.NodeKindRouter {
		t.Errorf("expected kind %q, got %q", core.NodeKindRouter, router.Kind())
	}
}

func TestNewRuleRouter_DefaultDecisionKey(t *testing.T) {
	router := NewRuleRouter("test", RuleRouterConfig{})

	config := router.Config()
	if config.DecisionKey != "test_decision" {
		t.Errorf("expected default decision key 'test_decision', got %q", config.DecisionKey)
	}
}

func TestRuleRouter_Route_SingleMatch(t *testing.T) {
	router := NewRuleRouter("test", RuleRouterConfig{
		Rules: []RouteRule{
			{
				Conditions: []RouteCondition{
					{VarPath: "category", Op: OpEquals, Value: "urgent"},
				},
				Target: "urgent-handler",
				Reason: "Category is urgent",
			},
			{
				Target: "default-handler",
				Reason: "Default route",
			},
		},
	})

	env := core.NewEnvelope().WithVar("category", "urgent")
	decision, err := router.Route(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decision.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(decision.Targets))
	}
	if decision.Targets[0] != "urgent-handler" {
		t.Errorf("expected target 'urgent-handler', got %q", decision.Targets[0])
	}
	if decision.Reason != "Category is urgent" {
		t.Errorf("expected reason 'Category is urgent', got %q", decision.Reason)
	}
}

func TestRuleRouter_Route_DefaultTarget(t *testing.T) {
	router := NewRuleRouter("test", RuleRouterConfig{
		Rules: []RouteRule{
			{
				Conditions: []RouteCondition{
					{VarPath: "category", Op: OpEquals, Value: "special"},
				},
				Target: "special-handler",
			},
		},
		DefaultTarget: "fallback",
	})

	env := core.NewEnvelope().WithVar("category", "normal")
	decision, err := router.Route(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decision.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(decision.Targets))
	}
	if decision.Targets[0] != "fallback" {
		t.Errorf("expected target 'fallback', got %q", decision.Targets[0])
	}
	if decision.Reason != "default route" {
		t.Errorf("expected reason 'default route', got %q", decision.Reason)
	}
}

func TestRuleRouter_Route_MultipleMatches(t *testing.T) {
	router := NewRuleRouter("test", RuleRouterConfig{
		Rules: []RouteRule{
			{
				Conditions: []RouteCondition{
					{VarPath: "notify_email", Op: OpExists},
				},
				Target: "email-handler",
				Reason: "Has email",
			},
			{
				Conditions: []RouteCondition{
					{VarPath: "notify_sms", Op: OpExists},
				},
				Target: "sms-handler",
				Reason: "Has SMS",
			},
		},
		AllowMultiple: true,
	})

	env := core.NewEnvelope().
		WithVar("notify_email", true).
		WithVar("notify_sms", true)
	decision, err := router.Route(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decision.Targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(decision.Targets))
	}
	if decision.Reason != "Has email; Has SMS" {
		t.Errorf("expected combined reason, got %q", decision.Reason)
	}
}

func TestRuleRouter_Route_Conditions(t *testing.T) {
	tests := []struct {
		name      string
		condition RouteCondition
		vars      map[string]any
		expected  bool
	}{
		{
			name:      "OpEquals match",
			condition: RouteCondition{VarPath: "status", Op: OpEquals, Value: "active"},
			vars:      map[string]any{"status": "active"},
			expected:  true,
		},
		{
			name:      "OpEquals no match",
			condition: RouteCondition{VarPath: "status", Op: OpEquals, Value: "active"},
			vars:      map[string]any{"status": "inactive"},
			expected:  false,
		},
		{
			name:      "OpNotEquals match",
			condition: RouteCondition{VarPath: "status", Op: OpNotEquals, Value: "active"},
			vars:      map[string]any{"status": "inactive"},
			expected:  true,
		},
		{
			name:      "OpContains match",
			condition: RouteCondition{VarPath: "message", Op: OpContains, Value: "urgent"},
			vars:      map[string]any{"message": "This is urgent!"},
			expected:  true,
		},
		{
			name:      "OpGreaterThan match",
			condition: RouteCondition{VarPath: "score", Op: OpGreaterThan, Value: 50},
			vars:      map[string]any{"score": 75},
			expected:  true,
		},
		{
			name:      "OpLessThan match",
			condition: RouteCondition{VarPath: "priority", Op: OpLessThan, Value: 5},
			vars:      map[string]any{"priority": 3},
			expected:  true,
		},
		{
			name:      "OpExists match",
			condition: RouteCondition{VarPath: "user_id", Op: OpExists},
			vars:      map[string]any{"user_id": "123"},
			expected:  true,
		},
		{
			name:      "OpExists no match",
			condition: RouteCondition{VarPath: "user_id", Op: OpExists},
			vars:      map[string]any{},
			expected:  false,
		},
		{
			name:      "OpNotExists match",
			condition: RouteCondition{VarPath: "deleted", Op: OpNotExists},
			vars:      map[string]any{},
			expected:  true,
		},
		{
			name:      "OpIn match",
			condition: RouteCondition{VarPath: "role", Op: OpIn, Values: []any{"admin", "moderator"}},
			vars:      map[string]any{"role": "admin"},
			expected:  true,
		},
		{
			name:      "OpIn no match",
			condition: RouteCondition{VarPath: "role", Op: OpIn, Values: []any{"admin", "moderator"}},
			vars:      map[string]any{"role": "user"},
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := NewRuleRouter("test", RuleRouterConfig{
				Rules: []RouteRule{
					{
						Conditions: []RouteCondition{tt.condition},
						Target:     "target",
					},
				},
				DefaultTarget: "default",
			})

			env := core.NewEnvelope()
			for k, v := range tt.vars {
				env.SetVar(k, v)
			}

			decision, err := router.Route(context.Background(), env)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expected && decision.Targets[0] != "target" {
				t.Errorf("expected condition to match, but got default target")
			}
			if !tt.expected && decision.Targets[0] == "target" {
				t.Errorf("expected condition to not match, but got target")
			}
		})
	}
}

func TestRuleRouter_Route_NestedVar(t *testing.T) {
	router := NewRuleRouter("test", RuleRouterConfig{
		Rules: []RouteRule{
			{
				Conditions: []RouteCondition{
					{VarPath: "user.role", Op: OpEquals, Value: "admin"},
				},
				Target: "admin-panel",
			},
		},
		DefaultTarget: "user-panel",
	})

	env := core.NewEnvelope().WithVar("user", map[string]any{
		"id":   "123",
		"role": "admin",
	})

	decision, err := router.Route(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Targets[0] != "admin-panel" {
		t.Errorf("expected 'admin-panel', got %q", decision.Targets[0])
	}
}

func TestRuleRouter_Run_StoresDecision(t *testing.T) {
	router := NewRuleRouter("my-router", RuleRouterConfig{
		Rules: []RouteRule{
			{Target: "next-node", Reason: "Always"},
		},
		DecisionKey: "my_decision",
	})

	env := core.NewEnvelope()
	result, err := router.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decision, ok := result.GetVar("my_decision")
	if !ok {
		t.Fatal("expected decision to be stored")
	}
	routeDecision := decision.(core.RouteDecision)
	if routeDecision.Targets[0] != "next-node" {
		t.Errorf("expected target 'next-node', got %v", routeDecision.Targets)
	}
}

func TestRuleRouter_InterfaceCompliance(t *testing.T) {
	var _ core.Node = (*RuleRouter)(nil)
	var _ core.RouterNode = (*RuleRouter)(nil)
}

// LLMRouter tests

func TestNewLLMRouter(t *testing.T) {
	client := &mockLLMClient{}
	router := NewLLMRouter("classifier", client, LLMRouterConfig{
		Model: "gpt-4",
		AllowedTargets: map[string]string{
			"positive": "positive-handler",
			"negative": "negative-handler",
		},
	})

	if router.ID() != "classifier" {
		t.Errorf("expected ID 'classifier', got %q", router.ID())
	}
	if router.Kind() != core.NodeKindRouter {
		t.Errorf("expected kind %q, got %q", core.NodeKindRouter, router.Kind())
	}
}

func TestNewLLMRouter_DefaultTemperature(t *testing.T) {
	client := &mockLLMClient{}
	router := NewLLMRouter("test", client, LLMRouterConfig{})

	config := router.Config()
	if config.Temperature == nil {
		t.Fatal("expected default temperature to be set")
	}
	if *config.Temperature != 0.1 {
		t.Errorf("expected default temperature 0.1, got %v", *config.Temperature)
	}
}

func TestLLMRouter_Route_JSONResponse(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{
			Text:  `{"choice": "positive", "reason": "Contains good sentiment"}`,
			Model: "gpt-4",
		},
	}

	router := NewLLMRouter("test", client, LLMRouterConfig{
		Model:     "gpt-4",
		InputVars: []string{"text"},
		AllowedTargets: map[string]string{
			"positive": "positive-handler",
			"negative": "negative-handler",
		},
	})

	env := core.NewEnvelope().WithVar("text", "I love this product!")
	decision, err := router.Route(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decision.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(decision.Targets))
	}
	if decision.Targets[0] != "positive-handler" {
		t.Errorf("expected 'positive-handler', got %q", decision.Targets[0])
	}
	if decision.Reason != "Contains good sentiment" {
		t.Errorf("expected reason 'Contains good sentiment', got %q", decision.Reason)
	}
}

func TestLLMRouter_Route_WithJSONField(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{
			JSON: map[string]any{
				"choice":     "negative",
				"reason":     "Bad sentiment",
				"confidence": 0.9,
			},
			Model: "gpt-4",
		},
	}

	router := NewLLMRouter("test", client, LLMRouterConfig{
		Model: "gpt-4",
		AllowedTargets: map[string]string{
			"positive": "positive-handler",
			"negative": "negative-handler",
		},
	})

	env := core.NewEnvelope().WithVar("text", "I hate this!")
	decision, err := router.Route(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Targets[0] != "negative-handler" {
		t.Errorf("expected 'negative-handler', got %q", decision.Targets[0])
	}
	if decision.Confidence == nil || *decision.Confidence != 0.9 {
		t.Errorf("expected confidence 0.9, got %v", decision.Confidence)
	}
}

func TestLLMRouter_Route_TextFallback(t *testing.T) {
	// When JSON parsing fails, try to find label in raw text
	client := &mockLLMClient{
		response: core.LLMResponse{
			Text:  "I classify this as positive sentiment",
			Model: "gpt-4",
		},
	}

	router := NewLLMRouter("test", client, LLMRouterConfig{
		Model: "gpt-4",
		AllowedTargets: map[string]string{
			"positive": "positive-handler",
			"negative": "negative-handler",
		},
	})

	env := core.NewEnvelope()
	decision, err := router.Route(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Targets[0] != "positive-handler" {
		t.Errorf("expected 'positive-handler' from text fallback, got %q", decision.Targets[0])
	}
}

func TestLLMRouter_Route_Error(t *testing.T) {
	client := &mockLLMClient{
		err: errors.New("API error"),
	}

	router := NewLLMRouter("test", client, LLMRouterConfig{
		Model:       "gpt-4",
		RetryPolicy: core.RetryPolicy{MaxAttempts: 1, Backoff: time.Millisecond},
		Timeout:     100 * time.Millisecond,
		AllowedTargets: map[string]string{
			"a": "handler-a",
		},
	})

	env := core.NewEnvelope()
	_, err := router.Route(context.Background(), env)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLLMRouter_Route_InvalidChoice(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{
			Text: `{"choice": "unknown", "reason": "test"}`,
		},
	}

	router := NewLLMRouter("test", client, LLMRouterConfig{
		Model: "gpt-4",
		AllowedTargets: map[string]string{
			"positive": "positive-handler",
			"negative": "negative-handler",
		},
	})

	env := core.NewEnvelope()
	_, err := router.Route(context.Background(), env)

	if err == nil {
		t.Fatal("expected error for invalid choice")
	}
}

func TestLLMRouter_Run_StoresDecision(t *testing.T) {
	client := &mockLLMClient{
		response: core.LLMResponse{
			Text: `{"choice": "a"}`,
		},
	}

	router := NewLLMRouter("my-router", client, LLMRouterConfig{
		DecisionKey: "my_decision",
		AllowedTargets: map[string]string{
			"a": "handler-a",
		},
	})

	env := core.NewEnvelope()
	result, err := router.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decision, ok := result.GetVar("my_decision")
	if !ok {
		t.Fatal("expected decision to be stored")
	}
	routeDecision := decision.(core.RouteDecision)
	if routeDecision.Targets[0] != "handler-a" {
		t.Errorf("expected 'handler-a', got %v", routeDecision.Targets)
	}
}

func TestLLMRouter_InterfaceCompliance(t *testing.T) {
	var _ core.Node = (*LLMRouter)(nil)
	var _ core.RouterNode = (*LLMRouter)(nil)
}
