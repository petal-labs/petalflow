package nodes

import (
	"context"
	"testing"

	"github.com/petal-labs/petalflow/core"
)

func TestParseWebhookTriggerConfig_Defaults(t *testing.T) {
	cfg, err := ParseWebhookTriggerConfig(map[string]any{})
	if err != nil {
		t.Fatalf("ParseWebhookTriggerConfig() error = %v", err)
	}

	if len(cfg.Methods) != 1 || cfg.Methods[0] != "POST" {
		t.Fatalf("Methods = %v, want [POST]", cfg.Methods)
	}
	if cfg.Auth.Type != WebhookAuthTypeNone {
		t.Fatalf("Auth.Type = %q, want %q", cfg.Auth.Type, WebhookAuthTypeNone)
	}
	if cfg.RequestVar != "webhook_request" {
		t.Fatalf("RequestVar = %q, want webhook_request", cfg.RequestVar)
	}
	if cfg.BodyVar != "webhook_body" {
		t.Fatalf("BodyVar = %q, want webhook_body", cfg.BodyVar)
	}
	if cfg.HeadersVar != "webhook_headers" {
		t.Fatalf("HeadersVar = %q, want webhook_headers", cfg.HeadersVar)
	}
	if cfg.QueryVar != "webhook_query" {
		t.Fatalf("QueryVar = %q, want webhook_query", cfg.QueryVar)
	}
	if cfg.MetadataVar != "webhook_meta" {
		t.Fatalf("MetadataVar = %q, want webhook_meta", cfg.MetadataVar)
	}
}

func TestParseWebhookTriggerConfig_HeaderTokenRequiresToken(t *testing.T) {
	_, err := ParseWebhookTriggerConfig(map[string]any{
		"auth": map[string]any{
			"type": "header_token",
		},
	})
	if err == nil {
		t.Fatal("expected auth token validation error, got nil")
	}
}

func TestWebhookTriggerNode_Run_MapsRequest(t *testing.T) {
	node := NewWebhookTriggerNode("trigger", WebhookTriggerNodeConfig{})
	env := core.NewEnvelope()
	env.SetVar(WebhookRequestEnvKey, map[string]any{
		"workflow_id": "wf-1",
		"trigger_id":  "incoming",
		"method":      "POST",
		"path":        "/api/workflows/wf-1/webhooks/incoming",
		"remote_addr": "127.0.0.1:9999",
		"received_at": "2026-02-16T00:00:00Z",
		"headers": map[string]any{
			"content-type": "application/json",
		},
		"query": map[string]any{
			"tenant": []string{"acme"},
		},
		"body": map[string]any{
			"event": "created",
		},
	})

	out, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if _, ok := out.GetVar("webhook_request"); !ok {
		t.Fatal("expected webhook_request var")
	}
	if body, ok := out.GetVar("webhook_body"); !ok {
		t.Fatal("expected webhook_body var")
	} else if bodyMap, ok := body.(map[string]any); !ok || bodyMap["event"] != "created" {
		t.Fatalf("webhook_body = %#v, want event=created", body)
	}
	metaRaw, ok := out.GetVar("webhook_meta")
	if !ok {
		t.Fatal("expected webhook_meta var")
	}
	meta, ok := metaRaw.(map[string]any)
	if !ok {
		t.Fatalf("webhook_meta type = %T, want map[string]any", metaRaw)
	}
	if meta["trigger_id"] != "incoming" {
		t.Fatalf("webhook_meta.trigger_id = %v, want incoming", meta["trigger_id"])
	}
}

func TestWebhookTriggerNode_Run_MissingRequest(t *testing.T) {
	node := NewWebhookTriggerNode("trigger", WebhookTriggerNodeConfig{})
	env := core.NewEnvelope()

	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for missing __webhook_request, got nil")
	}
}
