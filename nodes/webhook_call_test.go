package nodes

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
)

func TestParseWebhookCallConfig_ValidatesURL(t *testing.T) {
	_, err := ParseWebhookCallConfig(map[string]any{})
	if err == nil {
		t.Fatal("expected missing url error, got nil")
	}
}

func TestWebhookCallNode_Success(t *testing.T) {
	mockClient := NewMockHTTPClient(200)
	mockClient.Response = &http.Response{
		StatusCode: 200,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}

	node := NewWebhookCallNode("call", WebhookCallNodeConfig{
		URL:         "https://example.com/webhook",
		Method:      http.MethodPost,
		Headers:     map[string]string{"X-Test": "yes"},
		ResultVar:   "webhook_result",
		ErrorPolicy: WebhookCallErrorPolicyFail,
		HTTPClient:  mockClient,
	})
	env := core.NewEnvelope()
	env.SetVar("order_id", "ord_123")
	env.SetVar("status", "created")

	out, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(mockClient.Requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(mockClient.Requests))
	}
	if got := mockClient.Requests[0].Header.Get("X-Test"); got != "yes" {
		t.Fatalf("request X-Test header = %q, want yes", got)
	}

	resultRaw, ok := out.GetVar("webhook_result")
	if !ok {
		t.Fatal("expected webhook_result var")
	}
	result, ok := resultRaw.(map[string]any)
	if !ok {
		t.Fatalf("webhook_result type = %T, want map[string]any", resultRaw)
	}
	if result["ok"] != true {
		t.Fatalf("webhook_result.ok = %v, want true", result["ok"])
	}
	if result["status_code"] != 200 {
		t.Fatalf("webhook_result.status_code = %v, want 200", result["status_code"])
	}
}

func TestWebhookCallNode_ErrorPolicy(t *testing.T) {
	tests := []struct {
		name          string
		policy        WebhookCallErrorPolicy
		wantErr       bool
		wantRecorded  bool
		wantResultVar bool
		statusCode    int
	}{
		{name: "fail", policy: WebhookCallErrorPolicyFail, wantErr: true, statusCode: 500},
		{name: "record", policy: WebhookCallErrorPolicyRecord, wantRecorded: true, wantResultVar: true, statusCode: 500},
		{name: "continue", policy: WebhookCallErrorPolicyContinue, wantResultVar: true, statusCode: 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := NewMockHTTPClient(tt.statusCode)
			node := NewWebhookCallNode("call", WebhookCallNodeConfig{
				URL:         "https://example.com/webhook",
				Method:      http.MethodPost,
				ResultVar:   "result",
				ErrorPolicy: tt.policy,
				HTTPClient:  mockClient,
			})

			out, err := node.Run(context.Background(), core.NewEnvelope())
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantRecorded && !out.HasErrors() {
				t.Fatal("expected recorded node error")
			}
			if tt.wantResultVar {
				if _, ok := out.GetVar("result"); !ok {
					t.Fatal("expected result var to be set")
				}
			}
		})
	}
}

func TestWebhookCallNode_Template(t *testing.T) {
	mockClient := NewMockHTTPClient(200)
	mockClient.Response = &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("ok")),
	}

	node := NewWebhookCallNode("call", WebhookCallNodeConfig{
		URL:        "https://example.com/webhook",
		Template:   `{"id":"{{.vars.order_id}}"}`,
		HTTPClient: mockClient,
	})

	env := core.NewEnvelope()
	env.SetVar("order_id", "ord_42")
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	req := mockClient.Requests[0]
	body, _ := io.ReadAll(req.Body)
	if string(body) != `{"id":"ord_42"}` {
		t.Fatalf("request body = %q, want template output", string(body))
	}
}

type timeoutHTTPClient struct {
	delay time.Duration
}

func (c timeoutHTTPClient) Do(req *http.Request) (*http.Response, error) {
	select {
	case <-time.After(c.delay):
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

func TestWebhookCallNode_Timeout(t *testing.T) {
	node := NewWebhookCallNode("call", WebhookCallNodeConfig{
		URL:         "https://example.com/webhook",
		Timeout:     10 * time.Millisecond,
		ErrorPolicy: WebhookCallErrorPolicyFail,
		HTTPClient:  timeoutHTTPClient{delay: 200 * time.Millisecond},
	})

	_, err := node.Run(context.Background(), core.NewEnvelope())
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("timeout error = %v, want context deadline exceeded", err)
	}
}

func TestWebhookCallNode_InvalidPolicy(t *testing.T) {
	_, err := ParseWebhookCallConfig(map[string]any{
		"url":          "https://example.com",
		"error_policy": "unknown",
	})
	if err == nil {
		t.Fatal("expected invalid error_policy error, got nil")
	}
	if !strings.Contains(err.Error(), "error_policy") {
		t.Fatalf("error = %v, want error_policy context", err)
	}
}

func TestWebhookCallNode_ConfigDefaults(t *testing.T) {
	cfg, err := ParseWebhookCallConfig(map[string]any{
		"url": "https://example.com/webhook",
	})
	if err != nil {
		t.Fatalf("ParseWebhookCallConfig() error = %v", err)
	}
	if cfg.Method != http.MethodPost {
		t.Fatalf("Method = %q, want POST", cfg.Method)
	}
	if cfg.ErrorPolicy != WebhookCallErrorPolicyFail {
		t.Fatalf("ErrorPolicy = %q, want fail", cfg.ErrorPolicy)
	}
}

func TestWebhookCallNode_RequestBodyContainsVars(t *testing.T) {
	mockClient := NewMockHTTPClient(200)
	mockClient.Response = &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}
	node := NewWebhookCallNode("call", WebhookCallNodeConfig{
		URL:        "https://example.com/webhook",
		HTTPClient: mockClient,
	})
	env := core.NewEnvelope()
	env.SetVar("foo", "bar")

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	body, err := io.ReadAll(mockClient.Requests[0].Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !strings.Contains(string(body), "foo") || !strings.Contains(string(body), "bar") {
		t.Fatalf("request body = %s, want serialized vars", string(body))
	}
}

func ExampleWebhookCallNode() {
	mockClient := NewMockHTTPClient(200)
	node := NewWebhookCallNode("call", WebhookCallNodeConfig{
		URL:        "https://example.com/webhook",
		HTTPClient: mockClient,
	})
	env := core.NewEnvelope().WithVar("message", "hello")
	_, _ = node.Run(context.Background(), env)
	fmt.Println(len(mockClient.Requests))
	// Output: 1
}
