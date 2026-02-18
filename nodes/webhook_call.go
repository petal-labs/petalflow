package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/petal-labs/petalflow/core"
)

// HTTPClient abstracts outbound HTTP execution.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// WebhookCallErrorPolicy controls node behavior on outbound request failures.
type WebhookCallErrorPolicy string

const (
	WebhookCallErrorPolicyFail     WebhookCallErrorPolicy = "fail"
	WebhookCallErrorPolicyContinue WebhookCallErrorPolicy = "continue"
	WebhookCallErrorPolicyRecord   WebhookCallErrorPolicy = "record"
)

// WebhookCallNodeConfig configures a WebhookCallNode.
type WebhookCallNodeConfig struct {
	URL              string
	Method           string
	Headers          map[string]string
	Timeout          time.Duration
	InputVars        []string
	IncludeArtifacts bool
	IncludeMessages  bool
	IncludeTrace     bool
	Template         string
	ResultVar        string
	ErrorPolicy      WebhookCallErrorPolicy
	HTTPClient       HTTPClient
}

// ParseWebhookCallConfig normalizes webhook_call config from graph JSON.
func ParseWebhookCallConfig(m map[string]any) (WebhookCallNodeConfig, error) {
	cfg := WebhookCallNodeConfig{
		URL:         strings.TrimSpace(webhookConfigString(m, "url")),
		Method:      strings.TrimSpace(webhookConfigString(m, "method")),
		Template:    webhookConfigString(m, "template"),
		ResultVar:   strings.TrimSpace(webhookConfigString(m, "result_var")),
		ErrorPolicy: WebhookCallErrorPolicy(strings.TrimSpace(webhookConfigString(m, "error_policy"))),
		Timeout:     webhookConfigDuration(m, "timeout"),
	}
	if inputVars, ok := webhookConfigStringSlice(m, "input_vars"); ok {
		cfg.InputVars = inputVars
	}
	if includeArtifacts, ok := m["include_artifacts"].(bool); ok {
		cfg.IncludeArtifacts = includeArtifacts
	}
	if includeMessages, ok := m["include_messages"].(bool); ok {
		cfg.IncludeMessages = includeMessages
	}
	if includeTrace, ok := m["include_trace"].(bool); ok {
		cfg.IncludeTrace = includeTrace
	}
	if headersRaw, ok := m["headers"].(map[string]any); ok {
		cfg.Headers = make(map[string]string, len(headersRaw))
		for key, value := range headersRaw {
			if s, ok := value.(string); ok {
				cfg.Headers[key] = s
			}
		}
	}

	return normalizeWebhookCallConfig(cfg)
}

func normalizeWebhookCallConfig(cfg WebhookCallNodeConfig) (WebhookCallNodeConfig, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return WebhookCallNodeConfig{}, fmt.Errorf("url is required")
	}
	if strings.TrimSpace(cfg.Method) == "" {
		cfg.Method = http.MethodPost
	}
	cfg.Method = strings.ToUpper(strings.TrimSpace(cfg.Method))
	if !httpMethodTokenPattern.MatchString(cfg.Method) {
		return WebhookCallNodeConfig{}, fmt.Errorf("method %q is invalid", cfg.Method)
	}
	if cfg.ErrorPolicy == "" {
		cfg.ErrorPolicy = WebhookCallErrorPolicyFail
	}
	switch cfg.ErrorPolicy {
	case WebhookCallErrorPolicyFail, WebhookCallErrorPolicyContinue, WebhookCallErrorPolicyRecord:
		// valid
	default:
		return WebhookCallNodeConfig{}, fmt.Errorf("error_policy must be one of: fail, continue, record")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if len(cfg.Headers) == 0 {
		cfg.Headers = nil
	}
	return cfg, nil
}

// WebhookCallNode executes outbound HTTP webhook calls.
type WebhookCallNode struct {
	core.BaseNode
	config WebhookCallNodeConfig
}

// NewWebhookCallNode creates a new WebhookCallNode with normalized config.
func NewWebhookCallNode(id string, config WebhookCallNodeConfig) *WebhookCallNode {
	normalized, err := normalizeWebhookCallConfig(config)
	if err != nil {
		normalized = config
		if normalized.HTTPClient == nil {
			normalized.HTTPClient = http.DefaultClient
		}
		if normalized.Method == "" {
			normalized.Method = http.MethodPost
		}
		if normalized.ErrorPolicy == "" {
			normalized.ErrorPolicy = WebhookCallErrorPolicyFail
		}
	}

	return &WebhookCallNode{
		BaseNode: core.NewBaseNode(id, core.NodeKindWebhookCall),
		config:   normalized,
	}
}

// Config returns the node configuration.
func (n *WebhookCallNode) Config() WebhookCallNodeConfig {
	return n.config
}

// Run executes a webhook call.
func (n *WebhookCallNode) Run(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	outputData := n.buildOutputData(env)
	body, err := n.buildBody(outputData)
	if err != nil {
		return nil, fmt.Errorf("webhook_call node %s: %w", n.ID(), err)
	}

	requestCtx := ctx
	cancel := func() {}
	if n.config.Timeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, n.config.Timeout)
	}
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, n.config.Method, n.config.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("webhook_call node %s: build request: %w", n.ID(), err)
	}

	req.Header.Set("Content-Type", "application/json")
	for key, value := range n.config.Headers {
		req.Header.Set(key, value)
	}

	resp, err := n.config.HTTPClient.Do(req)
	if err != nil {
		return n.handleFailure(env, 0, nil, nil, err)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return n.handleFailure(env, resp.StatusCode, resp.Header, nil, fmt.Errorf("read response body: %w", readErr))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusErr := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		return n.handleFailure(env, resp.StatusCode, resp.Header, respBody, statusErr)
	}

	result := env.Clone()
	if n.config.ResultVar != "" {
		result.SetVar(n.config.ResultVar, map[string]any{
			"ok":          true,
			"status_code": resp.StatusCode,
			"headers":     responseHeaders(resp.Header),
			"body":        string(respBody),
			"url":         n.config.URL,
			"method":      n.config.Method,
		})
	}

	return result, nil
}

func (n *WebhookCallNode) buildOutputData(env *core.Envelope) map[string]any {
	data := make(map[string]any)

	if len(n.config.InputVars) > 0 {
		vars := make(map[string]any)
		for _, varName := range n.config.InputVars {
			if value, ok := env.GetVar(varName); ok {
				vars[varName] = value
			}
		}
		data["vars"] = vars
	} else if len(env.Vars) > 0 {
		vars := make(map[string]any, len(env.Vars))
		for key, value := range env.Vars {
			vars[key] = value
		}
		data["vars"] = vars
	}

	if n.config.IncludeArtifacts && len(env.Artifacts) > 0 {
		artifacts := make([]map[string]any, len(env.Artifacts))
		for i, art := range env.Artifacts {
			artifacts[i] = map[string]any{
				"id":        art.ID,
				"type":      art.Type,
				"mime_type": art.MimeType,
				"text":      art.Text,
				"uri":       art.URI,
				"meta":      art.Meta,
			}
		}
		data["artifacts"] = artifacts
	}

	if n.config.IncludeMessages && len(env.Messages) > 0 {
		messages := make([]map[string]any, len(env.Messages))
		for i, msg := range env.Messages {
			messages[i] = map[string]any{
				"role":    msg.Role,
				"content": msg.Content,
				"name":    msg.Name,
				"meta":    msg.Meta,
			}
		}
		data["messages"] = messages
	}

	if n.config.IncludeTrace {
		data["trace"] = map[string]any{
			"run_id":    env.Trace.RunID,
			"parent_id": env.Trace.ParentID,
			"span_id":   env.Trace.SpanID,
			"started":   env.Trace.Started,
		}
	}

	if env.Input != nil {
		data["input"] = env.Input
	}

	return data
}

func (n *WebhookCallNode) buildBody(payload map[string]any) ([]byte, error) {
	if n.config.Template == "" {
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		return body, nil
	}

	tpl, err := template.New("webhook_call").Funcs(webhookCallTemplateFuncs()).Parse(n.config.Template)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, payload); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}
	return buf.Bytes(), nil
}

func webhookCallTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"json": func(v any) string {
			data, err := json.Marshal(v)
			if err != nil {
				return fmt.Sprintf("error: %v", err)
			}
			return string(data)
		},
		"jsonPretty": func(v any) string {
			data, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				return fmt.Sprintf("error: %v", err)
			}
			return string(data)
		},
	}
}

func (n *WebhookCallNode) handleFailure(
	env *core.Envelope,
	statusCode int,
	headers http.Header,
	body []byte,
	runErr error,
) (*core.Envelope, error) {
	result := env.Clone()
	if n.config.ResultVar != "" {
		result.SetVar(n.config.ResultVar, map[string]any{
			"ok":          false,
			"status_code": statusCode,
			"headers":     responseHeaders(headers),
			"body":        string(body),
			"url":         n.config.URL,
			"method":      n.config.Method,
			"error":       runErr.Error(),
		})
	}

	switch n.config.ErrorPolicy {
	case WebhookCallErrorPolicyContinue:
		return result, nil
	case WebhookCallErrorPolicyRecord:
		result.AppendError(core.NodeError{
			NodeID:  n.ID(),
			Kind:    core.NodeKindWebhookCall,
			Message: runErr.Error(),
			At:      time.Now(),
			Cause:   runErr,
		})
		return result, nil
	case WebhookCallErrorPolicyFail:
		fallthrough
	default:
		return nil, fmt.Errorf("webhook_call node %s: %w", n.ID(), runErr)
	}
}

func responseHeaders(headers http.Header) map[string]any {
	if len(headers) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(headers))
	for key, values := range headers {
		out[key] = strings.Join(values, ", ")
	}
	return out
}

// MockHTTPClient is a mock HTTP client for tests.
type MockHTTPClient struct {
	Requests   []*http.Request
	Response   *http.Response
	Error      error
	StatusCode int
}

// NewMockHTTPClient creates a new mock HTTP client.
func NewMockHTTPClient(statusCode int) *MockHTTPClient {
	return &MockHTTPClient{StatusCode: statusCode, Requests: make([]*http.Request, 0)}
}

// Do implements HTTPClient.
func (c *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.Requests = append(c.Requests, req)
	if c.Error != nil {
		return nil, c.Error
	}
	if c.Response != nil {
		return c.Response, nil
	}
	return &http.Response{
		StatusCode: c.StatusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte{})),
	}, nil
}

var _ core.Node = (*WebhookCallNode)(nil)
var _ HTTPClient = (*MockHTTPClient)(nil)
