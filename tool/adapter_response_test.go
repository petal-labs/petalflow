package tool

import "testing"

func TestDecodeInvokeResponseStructuredError(t *testing.T) {
	_, err := decodeInvokeResponse([]byte(`{
		"error": {
			"code": "RATE_LIMITED",
			"message": "too many requests",
			"retryable": true,
			"details": {"limit":"minute"}
		}
	}`), 10)
	if err == nil {
		t.Fatal("decodeInvokeResponse() error = nil, want non-nil")
	}
	toolErr, ok := toolErrorFrom(err)
	if !ok || toolErr == nil {
		t.Fatalf("decodeInvokeResponse() error type = %T, want *ToolError", err)
	}
	if toolErr.Code != "RATE_LIMITED" {
		t.Fatalf("code = %q, want RATE_LIMITED", toolErr.Code)
	}
	if !toolErr.Retryable {
		t.Fatal("retryable = false, want true")
	}
	if got := toolErr.Details["limit"]; got != "minute" {
		t.Fatalf("details.limit = %v, want minute", got)
	}
}
