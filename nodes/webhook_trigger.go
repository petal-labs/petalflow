package nodes

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/core"
)

const (
	// WebhookRequestEnvKey is the internal envelope var where server ingress
	// stores normalized webhook request payload before workflow execution.
	WebhookRequestEnvKey = "__webhook_request"
)

var httpMethodTokenPattern = regexp.MustCompile(`^[!#$%&'*+.^_` + "`" + `|~0-9A-Za-z-]+$`)

// WebhookAuthType controls ingress request authentication strategy.
type WebhookAuthType string

const (
	WebhookAuthTypeNone        WebhookAuthType = "none"
	WebhookAuthTypeHeaderToken WebhookAuthType = "header_token"
)

// WebhookTriggerAuthConfig configures trigger authentication behavior.
type WebhookTriggerAuthConfig struct {
	Type   WebhookAuthType
	Header string
	Token  string
}

// WebhookTriggerNodeConfig configures a WebhookTriggerNode.
type WebhookTriggerNodeConfig struct {
	Methods     []string
	Auth        WebhookTriggerAuthConfig
	RequestVar  string
	BodyVar     string
	HeadersVar  string
	QueryVar    string
	MetadataVar string
	Timeout     time.Duration
}

// ParseWebhookTriggerConfig normalizes webhook trigger config from graph JSON.
func ParseWebhookTriggerConfig(m map[string]any) (WebhookTriggerNodeConfig, error) {
	cfg := WebhookTriggerNodeConfig{}

	if methods, ok := webhookConfigStringSlice(m, "methods"); ok {
		if len(methods) == 0 {
			return WebhookTriggerNodeConfig{}, fmt.Errorf("methods must not be empty")
		}
		cfg.Methods = methods
	}

	if authRaw, ok := m["auth"].(map[string]any); ok {
		cfg.Auth = WebhookTriggerAuthConfig{
			Type:   WebhookAuthType(strings.ToLower(strings.TrimSpace(webhookConfigMapString(authRaw, "type")))),
			Header: strings.TrimSpace(webhookConfigMapString(authRaw, "header")),
			Token:  strings.TrimSpace(webhookConfigMapString(authRaw, "token")),
		}
	}

	cfg.RequestVar = strings.TrimSpace(webhookConfigString(m, "request_var"))
	cfg.BodyVar = strings.TrimSpace(webhookConfigString(m, "body_var"))
	cfg.HeadersVar = strings.TrimSpace(webhookConfigString(m, "headers_var"))
	cfg.QueryVar = strings.TrimSpace(webhookConfigString(m, "query_var"))
	cfg.MetadataVar = strings.TrimSpace(webhookConfigString(m, "metadata_var"))
	cfg.Timeout = webhookConfigDuration(m, "timeout")

	return normalizeWebhookTriggerConfig(cfg)
}

func normalizeWebhookTriggerConfig(cfg WebhookTriggerNodeConfig) (WebhookTriggerNodeConfig, error) {
	if len(cfg.Methods) == 0 {
		cfg.Methods = []string{"POST"}
	}
	for i, rawMethod := range cfg.Methods {
		method := strings.ToUpper(strings.TrimSpace(rawMethod))
		if method == "" {
			return WebhookTriggerNodeConfig{}, fmt.Errorf("methods[%d] must not be empty", i)
		}
		if !httpMethodTokenPattern.MatchString(method) {
			return WebhookTriggerNodeConfig{}, fmt.Errorf("methods[%d] contains invalid HTTP method %q", i, rawMethod)
		}
		cfg.Methods[i] = method
	}

	if cfg.Auth.Type == "" {
		cfg.Auth.Type = WebhookAuthTypeNone
	}
	if cfg.Auth.Header == "" {
		cfg.Auth.Header = "X-PetalFlow-Webhook-Token"
	}
	switch cfg.Auth.Type {
	case WebhookAuthTypeNone:
		// no-op
	case WebhookAuthTypeHeaderToken:
		if strings.TrimSpace(cfg.Auth.Token) == "" {
			return WebhookTriggerNodeConfig{}, fmt.Errorf("auth.token is required when auth.type=header_token")
		}
	default:
		return WebhookTriggerNodeConfig{}, fmt.Errorf("auth.type must be one of: none, header_token")
	}

	if cfg.RequestVar == "" {
		cfg.RequestVar = "webhook_request"
	}
	if cfg.BodyVar == "" {
		cfg.BodyVar = "webhook_body"
	}
	if cfg.HeadersVar == "" {
		cfg.HeadersVar = "webhook_headers"
	}
	if cfg.QueryVar == "" {
		cfg.QueryVar = "webhook_query"
	}
	if cfg.MetadataVar == "" {
		cfg.MetadataVar = "webhook_meta"
	}

	return cfg, nil
}

// WebhookTriggerNode maps ingress request context into workflow vars.
type WebhookTriggerNode struct {
	core.BaseNode
	config WebhookTriggerNodeConfig
}

// NewWebhookTriggerNode creates a WebhookTriggerNode.
func NewWebhookTriggerNode(id string, config WebhookTriggerNodeConfig) *WebhookTriggerNode {
	normalized, err := normalizeWebhookTriggerConfig(config)
	if err != nil {
		// Keep constructor side-effect free for call sites; invalid config
		// surfaces during node Run and/or server trigger validation.
		normalized = config
	}

	return &WebhookTriggerNode{
		BaseNode: core.NewBaseNode(id, core.NodeKindWebhookTrigger),
		config:   normalized,
	}
}

// Config returns the node's normalized configuration.
func (n *WebhookTriggerNode) Config() WebhookTriggerNodeConfig {
	return n.config
}

// Run maps __webhook_request payload into configured output vars.
func (n *WebhookTriggerNode) Run(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	requestRaw, ok := env.GetVar(WebhookRequestEnvKey)
	if !ok {
		return nil, fmt.Errorf("webhook_trigger node %s: missing %s payload", n.ID(), WebhookRequestEnvKey)
	}

	requestMap, ok := requestRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("webhook_trigger node %s: %s must be object, got %T", n.ID(), WebhookRequestEnvKey, requestRaw)
	}

	result := env.Clone()
	result.SetVar(n.config.RequestVar, requestMap)

	if body, exists := requestMap["body"]; exists {
		result.SetVar(n.config.BodyVar, body)
	}
	if headers, exists := requestMap["headers"]; exists {
		result.SetVar(n.config.HeadersVar, headers)
	}
	if query, exists := requestMap["query"]; exists {
		result.SetVar(n.config.QueryVar, query)
	}

	metadata := map[string]any{}
	for _, key := range []string{"workflow_id", "trigger_id", "method", "path", "remote_addr", "received_at"} {
		if value, exists := requestMap[key]; exists {
			metadata[key] = value
		}
	}
	result.SetVar(n.config.MetadataVar, metadata)

	return result, nil
}

var _ core.Node = (*WebhookTriggerNode)(nil)
