package tool

import "slices"

// Manifest schema constants for the initial tool contract version.
const (
	ManifestVersionV1 = "1.0"
	SchemaToolV1      = "https://petalflow.dev/schemas/tool-manifest/v1.json"
)

// TransportType identifies the runtime transport protocol for a tool.
type TransportType string

const (
	TransportTypeNative TransportType = "native"
	TransportTypeHTTP   TransportType = "http"
	TransportTypeStdio  TransportType = "stdio"
	TransportTypeMCP    TransportType = "mcp"
)

// MCPMode defines how an MCP server is connected.
type MCPMode string

const (
	MCPModeStdio MCPMode = "stdio"
	MCPModeSSE   MCPMode = "sse"
	// MCPModeStreamableHTTP is the current MCP transport mode per the
	// Streamable HTTP specification: POST-based JSON-RPC with
	// Mcp-Session-Id sessions, SSE response streams, and optional OAuth
	// 2.1 authorization.
	MCPModeStreamableHTTP MCPMode = "streamable_http"
)

// ToolManifest describes a registered tool independent of tool origin.
type ToolManifest struct {
	Schema          string                `json:"$schema,omitempty"`
	ManifestVersion string                `json:"manifest_version"`
	Tool            ToolMetadata          `json:"tool"`
	Transport       TransportSpec         `json:"transport"`
	Actions         map[string]ActionSpec `json:"actions"`
	Config          map[string]FieldSpec  `json:"config,omitempty"`
	Health          *HealthConfig         `json:"health,omitempty"`
}

// Manifest is kept as an alias for backward compatibility while the package
// adopts ToolManifest naming used in the implementation plan.
type Manifest = ToolManifest

// ToolMetadata contains display metadata for a tool.
type ToolMetadata struct {
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	License     string   `json:"license,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// ToolInfo is an alias for backward compatibility with previous naming.
type ToolInfo = ToolMetadata

// ActionSpec defines callable behavior on a tool.
type ActionSpec struct {
	MCPToolName string               `json:"mcp_tool_name,omitempty"`
	Description string               `json:"description,omitempty"`
	Inputs      map[string]FieldSpec `json:"inputs,omitempty"`
	Outputs     map[string]FieldSpec `json:"outputs,omitempty"`
	LLMCallable *bool                `json:"llm_callable,omitempty"`
	Idempotent  bool                 `json:"idempotent,omitempty"`
}

// FieldSpec is the v1 field/type descriptor used for inputs, outputs, and config.
type FieldSpec struct {
	Type        string               `json:"type"`
	Required    bool                 `json:"required,omitempty"`
	Description string               `json:"description,omitempty"`
	Default     any                  `json:"default,omitempty"`
	Sensitive   bool                 `json:"sensitive,omitempty"`
	Items       *FieldSpec           `json:"items,omitempty"`
	Properties  map[string]FieldSpec `json:"properties,omitempty"`
}

// TransportSpec describes how PetalFlow invokes the tool.
type TransportSpec struct {
	Type      TransportType     `json:"type"`
	Endpoint  string            `json:"endpoint,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Mode      MCPMode           `json:"mode,omitempty"`
	TimeoutMS int               `json:"timeout_ms,omitempty"`
	Retry     RetryPolicy       `json:"retry,omitempty"`
	// Headers are static HTTP headers for MCP HTTP modes. Secret values
	// must come from sensitive config via {{config:key}} templates; the
	// manifest itself must not carry credentials.
	Headers map[string]string `json:"headers,omitempty"`
	// Auth configures bearer or OAuth 2.1 authorization for MCP HTTP modes.
	Auth *MCPAuthConfig `json:"auth,omitempty"`
	// AllowPrivateNetwork opts an MCP HTTP endpoint out of the outbound
	// egress (SSRF) policy for loopback/private destinations.
	AllowPrivateNetwork bool `json:"allow_private_network,omitempty"`
}

// HTTPTransport is the typed view for HTTP transport configuration.
type HTTPTransport struct {
	Endpoint  string      `json:"endpoint"`
	TimeoutMS int         `json:"timeout_ms,omitempty"`
	Retry     RetryPolicy `json:"retry,omitempty"`
}

// StdioTransport is the typed view for subprocess transport configuration.
type StdioTransport struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	TimeoutMS int               `json:"timeout_ms,omitempty"`
	Retry     RetryPolicy       `json:"retry,omitempty"`
}

// MCPTransport is the typed view for MCP transport configuration.
type MCPTransport struct {
	Mode      MCPMode           `json:"mode"`
	Endpoint  string            `json:"endpoint,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	TimeoutMS int               `json:"timeout_ms,omitempty"`
	Retry     RetryPolicy       `json:"retry,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Auth      *MCPAuthConfig    `json:"auth,omitempty"`
	// AllowPrivateNetwork opts the endpoint out of the SSRF egress policy.
	AllowPrivateNetwork bool `json:"allow_private_network,omitempty"`
}

// MCPAuthType identifies the authorization mechanism for MCP HTTP modes.
type MCPAuthType string

const (
	// MCPAuthBearer attaches a static bearer token from sensitive config.
	MCPAuthBearer MCPAuthType = "bearer"
	// MCPAuthOAuth2 runs the OAuth 2.1 client credentials flow.
	MCPAuthOAuth2 MCPAuthType = "oauth2"
)

// MCPAuthConfig configures authorization for MCP HTTP transports.
type MCPAuthConfig struct {
	// Type is "bearer" or "oauth2". Empty disables explicit authorization
	// (still allows static transport headers).
	Type MCPAuthType `json:"type,omitempty"`
	// Header overrides the authorization header name for bearer tokens.
	Header string `json:"header,omitempty"`
	// Scheme overrides the authorization scheme for bearer tokens.
	Scheme string `json:"scheme,omitempty"`
	// TokenRef names a config key whose value is the bearer token. The
	// referenced field should be marked sensitive so the value is
	// encrypted at rest and redacted in outputs.
	TokenRef string `json:"token_ref,omitempty"`
	// OAuth configures the OAuth 2.1 client credentials flow.
	OAuth *MCPOAuth2Auth `json:"oauth,omitempty"`
}

// MCPOAuth2Auth holds OAuth 2.1 client credentials settings. Secrets are
// referenced by config key, never stored inline in the manifest.
type MCPOAuth2Auth struct {
	// TokenURL is the authorization server token endpoint. When empty it
	// is discovered via protected resource / authorization server metadata.
	TokenURL string `json:"token_url,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	// ClientSecretRef names a config key holding the client secret. The
	// referenced field must be marked sensitive.
	ClientSecretRef string   `json:"client_secret_ref,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
	Resource        string   `json:"resource,omitempty"`
}

// RetryPolicy defines adapter retry behavior.
type RetryPolicy struct {
	MaxAttempts    int   `json:"max_attempts,omitempty"`
	BackoffMS      int   `json:"backoff_ms,omitempty"`
	RetryableCodes []int `json:"retryable_codes,omitempty"`
}

// HealthConfig defines optional tool health-check settings.
type HealthConfig struct {
	Endpoint           string `json:"endpoint,omitempty"`
	Method             string `json:"method,omitempty"`
	IntervalSeconds    int    `json:"interval_seconds,omitempty"`
	TimeoutMS          int    `json:"timeout_ms,omitempty"`
	UnhealthyThreshold int    `json:"unhealthy_threshold,omitempty"`
}

// NewManifest returns a manifest pre-populated with v1 schema metadata.
func NewManifest(name string) ToolManifest {
	return ToolManifest{
		Schema:          SchemaToolV1,
		ManifestVersion: ManifestVersionV1,
		Tool: ToolMetadata{
			Name: name,
		},
		Actions: make(map[string]ActionSpec),
	}
}

// NewHTTPTransport creates a transport specification for HTTP tools.
func NewHTTPTransport(cfg HTTPTransport) TransportSpec {
	return TransportSpec{
		Type:      TransportTypeHTTP,
		Endpoint:  cfg.Endpoint,
		TimeoutMS: cfg.TimeoutMS,
		Retry:     cfg.Retry,
	}
}

// NewNativeTransport creates a transport specification for in-process tools.
func NewNativeTransport() TransportSpec {
	return TransportSpec{
		Type: TransportTypeNative,
	}
}

// NewStdioTransport creates a transport specification for subprocess tools.
func NewStdioTransport(cfg StdioTransport) TransportSpec {
	return TransportSpec{
		Type:      TransportTypeStdio,
		Command:   cfg.Command,
		Args:      slices.Clone(cfg.Args),
		Env:       cloneStringMap(cfg.Env),
		TimeoutMS: cfg.TimeoutMS,
		Retry:     cfg.Retry,
	}
}

// NewMCPTransport creates a transport specification for MCP tools.
func NewMCPTransport(cfg MCPTransport) TransportSpec {
	return TransportSpec{
		Type:                TransportTypeMCP,
		Mode:                cfg.Mode,
		Endpoint:            cfg.Endpoint,
		Command:             cfg.Command,
		Args:                slices.Clone(cfg.Args),
		Env:                 cloneStringMap(cfg.Env),
		TimeoutMS:           cfg.TimeoutMS,
		Retry:               cfg.Retry,
		Headers:             cloneStringMap(cfg.Headers),
		Auth:                cfg.Auth,
		AllowPrivateNetwork: cfg.AllowPrivateNetwork,
	}
}

// AsHTTP converts a transport specification to HTTP transport config.
func (t TransportSpec) AsHTTP() (HTTPTransport, bool) {
	if t.Type != TransportTypeHTTP {
		return HTTPTransport{}, false
	}
	return HTTPTransport{
		Endpoint:  t.Endpoint,
		TimeoutMS: t.TimeoutMS,
		Retry:     t.Retry,
	}, true
}

// AsStdio converts a transport specification to stdio transport config.
func (t TransportSpec) AsStdio() (StdioTransport, bool) {
	if t.Type != TransportTypeStdio {
		return StdioTransport{}, false
	}
	return StdioTransport{
		Command:   t.Command,
		Args:      slices.Clone(t.Args),
		Env:       cloneStringMap(t.Env),
		TimeoutMS: t.TimeoutMS,
		Retry:     t.Retry,
	}, true
}

// AsMCP converts a transport specification to MCP transport config.
func (t TransportSpec) AsMCP() (MCPTransport, bool) {
	if t.Type != TransportTypeMCP {
		return MCPTransport{}, false
	}
	return MCPTransport{
		Mode:                t.Mode,
		Endpoint:            t.Endpoint,
		Command:             t.Command,
		Args:                slices.Clone(t.Args),
		Env:                 cloneStringMap(t.Env),
		TimeoutMS:           t.TimeoutMS,
		Retry:               t.Retry,
		Headers:             cloneStringMap(t.Headers),
		Auth:                t.Auth,
		AllowPrivateNetwork: t.AllowPrivateNetwork,
	}, true
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
