package tool

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpclient "github.com/petal-labs/petalflow/tool/mcp"
)

func TestManifestSchemaAcceptsStreamableHTTPMode(t *testing.T) {
	manifest := `{
		"manifest_version": "1.0",
		"tool": {"name": "http_tool"},
		"transport": {
			"type": "mcp",
			"mode": "streamable_http",
			"endpoint": "https://mcp.example.com/mcp",
			"auth": {
				"type": "oauth2",
				"oauth": {
					"client_id": "petalflow",
					"client_secret_ref": "client_secret",
					"scopes": ["tools:call"]
				}
			}
		},
		"actions": {"run": {"description": "run"}}
	}`

	result := ValidateManifestJSON([]byte(manifest))
	for _, diag := range result.Diagnostics {
		if diag.Severity == SeverityError {
			t.Fatalf("unexpected diagnostic: %+v", diag)
		}
	}
}

func TestManifestSchemaRejectsCredentialsInEndpointURL(t *testing.T) {
	manifest := `{
		"manifest_version": "1.0",
		"tool": {"name": "http_tool"},
		"transport": {
			"type": "mcp",
			"mode": "streamable_http",
			"endpoint": "https://mcp.example.com/mcp?access_token=secret123"
		},
		"actions": {"run": {}}
	}`

	result := ValidateManifestJSON([]byte(manifest))
	found := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "CREDENTIALS_IN_URL" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected CREDENTIALS_IN_URL diagnostic, got %+v", result.Diagnostics)
	}
}

func TestManifestSchemaRejectsUserinfoInEndpointURL(t *testing.T) {
	manifest := `{
		"manifest_version": "1.0",
		"tool": {"name": "http_tool"},
		"transport": {
			"type": "mcp",
			"mode": "streamable_http",
			"endpoint": "https://user:pass@mcp.example.com/mcp"
		},
		"actions": {"run": {}}
	}`

	result := ValidateManifestJSON([]byte(manifest))
	found := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "CREDENTIALS_IN_URL" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected CREDENTIALS_IN_URL diagnostic, got %+v", result.Diagnostics)
	}
}

func TestManifestSchemaRejectsBearerAuthWithoutTokenRef(t *testing.T) {
	manifest := `{
		"manifest_version": "1.0",
		"tool": {"name": "http_tool"},
		"transport": {
			"type": "mcp",
			"mode": "streamable_http",
			"endpoint": "https://mcp.example.com/mcp",
			"auth": {"type": "bearer"}
		},
		"actions": {"run": {}}
	}`

	result := ValidateManifestJSON([]byte(manifest))
	found := false
	for _, diag := range result.Diagnostics {
		if diag.Field == "transport.auth.token_ref" && diag.Code == "REQUIRED" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected transport.auth.token_ref REQUIRED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestMCPAuthReferenceValidatorRequiresSensitiveConfig(t *testing.T) {
	reg := Registration{
		Name: "valid_ref",
		Manifest: func() Manifest {
			m := NewManifest("valid_ref")
			m.Actions = map[string]ActionSpec{"run": {}}
			m.Config = map[string]FieldSpec{
				"client_secret": {Type: "string", Sensitive: true},
			}
			m.Transport = NewMCPTransport(MCPTransport{
				Mode:     MCPModeStreamableHTTP,
				Endpoint: "https://mcp.example.com/mcp",
				Auth: &MCPAuthConfig{
					Type: MCPAuthOAuth2,
					OAuth: &MCPOAuth2Auth{
						ClientID:        "petalflow",
						ClientSecretRef: "client_secret",
					},
				},
			})
			return m
		}(),
		Config: map[string]string{"client_secret": "shh"},
	}

	if diags := (MCPAuthReferenceValidator{}).ValidateRegistration(reg); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	spec := reg.Manifest.Config["client_secret"]
	spec.Sensitive = false
	reg.Manifest.Config["client_secret"] = spec
	diags := (MCPAuthReferenceValidator{}).ValidateRegistration(reg)
	if len(diags) != 1 || diags[0].Code != "CONFIG_REF_NOT_SENSITIVE" {
		t.Fatalf("diags = %+v, want CONFIG_REF_NOT_SENSITIVE", diags)
	}
}

func TestResolveMCPHeadersTemplateAndOverlayInjection(t *testing.T) {
	transport := MCPTransport{
		Mode: MCPModeStreamableHTTP,
		Headers: map[string]string{
			"X-Tenant":    "{{config:tenant}}",
			"X-Static":    "plain",
			"X-Universal": "Bearer {{config:token}}",
		},
	}
	config := map[string]string{"tenant": "acme", "token": "shh"}
	overlay := &MCPOverlay{
		Config: map[string]MCPOverlayConfigField{
			"token": {FieldSpec: FieldSpec{Type: "string", Sensitive: true}, Header: "X-Api-Key"},
		},
	}

	headers, err := resolveMCPHeaders(transport, config, overlay)
	if err != nil {
		t.Fatalf("resolveMCPHeaders() error = %v", err)
	}
	if headers["X-Tenant"] != "acme" || headers["X-Static"] != "plain" {
		t.Fatalf("headers = %+v", headers)
	}
	if headers["X-Universal"] != "Bearer shh" || headers["X-Api-Key"] != "shh" {
		t.Fatalf("headers = %+v, want template and overlay injection", headers)
	}
}

func TestResolveMCPHeadersRejectsUnresolvedTemplate(t *testing.T) {
	transport := MCPTransport{
		Mode:    MCPModeStreamableHTTP,
		Headers: map[string]string{"Authorization": "Bearer {{config:missing}}"},
	}
	if _, err := resolveMCPHeaders(transport, map[string]string{}, nil); err == nil {
		t.Fatal("resolveMCPHeaders() with missing config key should fail")
	}
}

func TestNewMCPAuthorizerBuildsBearerFromConfig(t *testing.T) {
	transport := MCPTransport{
		Mode:     MCPModeStreamableHTTP,
		Endpoint: "https://mcp.example.com/mcp",
		Auth:     &MCPAuthConfig{Type: MCPAuthBearer, TokenRef: "api_key"},
	}
	config := map[string]string{"api_key": "shh"}

	authorizer, err := newMCPAuthorizer("reg", transport, config)
	if err != nil {
		t.Fatalf("newMCPAuthorizer() error = %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", nil)
	if err := authorizer.Authorize(context.Background(), req); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer shh" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestNewMCPAuthorizerRejectsMissingTokenValue(t *testing.T) {
	transport := MCPTransport{
		Mode:     MCPModeStreamableHTTP,
		Endpoint: "https://mcp.example.com/mcp",
		Auth:     &MCPAuthConfig{Type: MCPAuthBearer, TokenRef: "api_key"},
	}
	if _, err := newMCPAuthorizer("reg", transport, map[string]string{}); err == nil {
		t.Fatal("newMCPAuthorizer() with missing token value should fail")
	}
}

func TestNewMCPRuntimeTransportStreamableHTTPWiring(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request mcpclient.Message
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "wire-test")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"t","version":"1"}}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`))
	}))
	defer server.Close()

	transport := MCPTransport{
		Mode:                MCPModeStreamableHTTP,
		Endpoint:            server.URL,
		AllowPrivateNetwork: true,
	}
	runtime, err := newMCPRuntimeTransport(context.Background(), "wired", transport, nil, nil)
	if err != nil {
		t.Fatalf("newMCPRuntimeTransport() error = %v", err)
	}
	defer runtime.Close(context.Background())

	client := mcpclient.NewClient(runtime, mcpclient.Options{})
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
}

func TestNewMCPRuntimeTransportBlocksPrivateEndpointsByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	transport := MCPTransport{
		Mode:     MCPModeStreamableHTTP,
		Endpoint: server.URL,
	}
	if _, err := newMCPRuntimeTransport(context.Background(), "ssrf", transport, nil, nil); err == nil {
		t.Fatal("newMCPRuntimeTransport() with private endpoint should fail by default")
	} else if !strings.Contains(err.Error(), "egress") && !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("error = %v, want egress policy rejection", err)
	}
}

func TestNewMCPRuntimeTransportRejectsSecretsInEndpointURL(t *testing.T) {
	transport := MCPTransport{
		Mode:     MCPModeStreamableHTTP,
		Endpoint: "https://mcp.example.com/mcp?access_token=secret",
	}
	if _, err := newMCPRuntimeTransport(context.Background(), "leaky", transport, nil, nil); err == nil {
		t.Fatal("newMCPRuntimeTransport() with credential in URL should fail")
	}
}

func TestClassifyMCPInvokeErrorMapsAuthorizationErrors(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantCode  string
		retryable bool
	}{
		{
			name:      "unauthorized",
			err:       &mcpclient.RequestError{Method: "tools/call", Err: mcpclient.ErrUnauthorized},
			wantCode:  ToolErrorCodeUnauthorized,
			retryable: false,
		},
		{
			name:      "insufficient scope",
			err:       &mcpclient.RequestError{Method: "tools/call", Err: mcpclient.ErrInsufficientScope},
			wantCode:  ToolErrorCodeForbidden,
			retryable: false,
		},
		{
			name:      "session expired",
			err:       &mcpclient.RequestError{Method: "tools/call", Err: mcpclient.ErrSessionExpired},
			wantCode:  ToolErrorCodeTransportFailure,
			retryable: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			classified := classifyMCPInvokeError(tc.err)
			var toolErr *ToolError
			if !errors.As(classified, &toolErr) {
				t.Fatalf("classifyMCPInvokeError() = %v, want ToolError", classified)
			}
			if toolErr.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", toolErr.Code, tc.wantCode)
			}
			if toolErr.Retryable != tc.retryable {
				t.Fatalf("retryable = %v, want %v", toolErr.Retryable, tc.retryable)
			}
		})
	}
}

func TestRedactRegistrationMasksTransportCredentials(t *testing.T) {
	reg := ToolRegistration{
		Name: "secret_tool",
		Manifest: func() Manifest {
			m := NewManifest("secret_tool")
			m.Actions = map[string]ActionSpec{"run": {}}
			m.Transport = NewMCPTransport(MCPTransport{
				Mode:     MCPModeStreamableHTTP,
				Endpoint: "https://mcp.example.com/mcp?access_token=leaky",
				Headers: map[string]string{
					"Authorization": "Bearer shh",
					"X-Tenant":      "acme",
				},
			})
			return m
		}(),
	}

	redacted := RedactRegistration(reg)
	if got := redacted.Manifest.Transport.Headers["Authorization"]; got != MaskedSecretValue {
		t.Fatalf("Authorization header = %q, want masked", got)
	}
	if got := redacted.Manifest.Transport.Headers["X-Tenant"]; got != "acme" {
		t.Fatalf("X-Tenant header = %q, want preserved", got)
	}
	if strings.Contains(redacted.Manifest.Transport.Endpoint, "access_token") {
		t.Fatalf("endpoint = %q, want query string stripped", redacted.Manifest.Transport.Endpoint)
	}
}
