package petalflow

import (
	"errors"
	"testing"
	"time"
)

func TestNodeKind_String(t *testing.T) {
	tests := []struct {
		kind     NodeKind
		expected string
	}{
		{NodeKindLLM, "llm"},
		{NodeKindTool, "tool"},
		{NodeKindRouter, "router"},
		{NodeKindMerge, "merge"},
		{NodeKindMap, "map"},
		{NodeKindGate, "gate"},
		{NodeKindNoop, "noop"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.expected {
				t.Errorf("NodeKind.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNodeError_Error(t *testing.T) {
	err := NodeError{
		NodeID:  "test-node",
		Kind:    NodeKindLLM,
		Message: "something went wrong",
		Attempt: 1,
		At:      time.Now(),
	}

	if got := err.Error(); got != "something went wrong" {
		t.Errorf("NodeError.Error() = %v, want %v", got, "something went wrong")
	}
}

func TestNodeError_Unwrap(t *testing.T) {
	cause := errors.New("underlying cause")
	err := NodeError{
		NodeID:  "test-node",
		Kind:    NodeKindTool,
		Message: "tool failed",
		Cause:   cause,
	}

	if got := err.Unwrap(); got != cause {
		t.Errorf("NodeError.Unwrap() = %v, want %v", got, cause)
	}
}

func TestNodeError_Unwrap_Nil(t *testing.T) {
	err := NodeError{
		NodeID:  "test-node",
		Message: "no cause",
	}

	if got := err.Unwrap(); got != nil {
		t.Errorf("NodeError.Unwrap() = %v, want nil", got)
	}
}

func TestDefaultRetryPolicy(t *testing.T) {
	policy := DefaultRetryPolicy()

	if policy.MaxAttempts != 3 {
		t.Errorf("DefaultRetryPolicy().MaxAttempts = %v, want 3", policy.MaxAttempts)
	}
	if policy.Backoff != time.Second {
		t.Errorf("DefaultRetryPolicy().Backoff = %v, want %v", policy.Backoff, time.Second)
	}
}

func TestTokenUsage_Add(t *testing.T) {
	u1 := TokenUsage{
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		CostUSD:      0.01,
	}
	u2 := TokenUsage{
		InputTokens:  200,
		OutputTokens: 100,
		TotalTokens:  300,
		CostUSD:      0.02,
	}

	result := u1.Add(u2)

	if result.InputTokens != 300 {
		t.Errorf("TokenUsage.Add().InputTokens = %v, want 300", result.InputTokens)
	}
	if result.OutputTokens != 150 {
		t.Errorf("TokenUsage.Add().OutputTokens = %v, want 150", result.OutputTokens)
	}
	if result.TotalTokens != 450 {
		t.Errorf("TokenUsage.Add().TotalTokens = %v, want 450", result.TotalTokens)
	}
	if result.CostUSD != 0.03 {
		t.Errorf("TokenUsage.Add().CostUSD = %v, want 0.03", result.CostUSD)
	}
}

func TestErrorPolicy_Values(t *testing.T) {
	if ErrorPolicyFail != "fail" {
		t.Errorf("ErrorPolicyFail = %v, want 'fail'", ErrorPolicyFail)
	}
	if ErrorPolicyContinue != "continue" {
		t.Errorf("ErrorPolicyContinue = %v, want 'continue'", ErrorPolicyContinue)
	}
	if ErrorPolicyRecord != "record" {
		t.Errorf("ErrorPolicyRecord = %v, want 'record'", ErrorPolicyRecord)
	}
}
