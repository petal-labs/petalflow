package tool

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/security"
	mcpclient "github.com/petal-labs/petalflow/tool/mcp"
)

const (
	defaultMCPReconnectAttempts = 3
	defaultMCPReconnectBackoff  = 250 * time.Millisecond

	// configHeaderTemplatePrefix marks {{config:key}} template values in
	// static transport headers, resolved from registration config so
	// credentials stay in encrypted configuration, not the manifest.
	configHeaderTemplatePrefix = "{{config:"
	configHeaderTemplateSuffix = "}}"
)

func newMCPRuntimeTransport(ctx context.Context, registrationName string, transport MCPTransport, config map[string]string, overlay *MCPOverlay) (mcpclient.Transport, error) {
	switch transport.Mode {
	case MCPModeStdio:
		env := applyMCPConfigInjection(cloneStringMap(transport.Env), config, overlay)

		dialer := func(ctx context.Context) (mcpclient.Transport, error) {
			return mcpclient.NewStdioTransport(ctx, mcpclient.StdioTransportConfig{
				Command: transport.Command,
				Args:    transport.Args,
				Env:     env,
			})
		}
		return mcpclient.NewReconnectingTransport(ctx, dialer, mcpclient.ReconnectConfig{
			MaxAttempts: defaultMCPReconnectAttempts,
			BaseBackoff: defaultMCPReconnectBackoff,
		})

	case MCPModeStreamableHTTP:
		headers, err := resolveMCPHeaders(transport, config, overlay)
		if err != nil {
			return nil, fmt.Errorf("tool: mcp transport headers for %s: %w", registrationName, err)
		}
		if err := validateMCPEndpointEgress(ctx, registrationName, transport); err != nil {
			return nil, err
		}
		authorizer, err := newMCPAuthorizer(registrationName, transport, config)
		if err != nil {
			return nil, err
		}

		return mcpclient.NewStreamableHTTPTransport(mcpclient.StreamableHTTPConfig{
			Endpoint:   transport.Endpoint,
			Headers:    headers,
			Authorizer: authorizer,
			Client:     mcpHTTPClient(transport.TimeoutMS),
		})

	case MCPModeSSE:
		headers, err := resolveMCPHeaders(transport, config, overlay)
		if err != nil {
			return nil, fmt.Errorf("tool: mcp transport headers for %s: %w", registrationName, err)
		}
		if err := validateMCPEndpointEgress(ctx, registrationName, transport); err != nil {
			return nil, err
		}
		authorizer, err := newMCPAuthorizer(registrationName, transport, config)
		if err != nil {
			return nil, err
		}

		dialer := func(ctx context.Context) (mcpclient.Transport, error) {
			return mcpclient.NewSSETransport(mcpclient.SSETransportConfig{
				Endpoint:   transport.Endpoint,
				Headers:    headers,
				Authorizer: authorizer,
				Client:     mcpHTTPClient(transport.TimeoutMS),
			})
		}
		return mcpclient.NewReconnectingTransport(ctx, dialer, mcpclient.ReconnectConfig{
			MaxAttempts: defaultMCPReconnectAttempts,
			BaseBackoff: defaultMCPReconnectBackoff,
		})

	default:
		return nil, fmt.Errorf("tool: unsupported mcp mode %q for %s", transport.Mode, registrationName)
	}
}

// mcpHTTPClient builds an HTTP client for MCP transports. The timeout
// bounds response headers only; SSE stream bodies stay open for
// server-to-client delivery and are governed by request contexts.
func mcpHTTPClient(timeoutMS int) *http.Client {
	headerTimeout := time.Duration(timeoutMS) * time.Millisecond
	if headerTimeout <= 0 {
		headerTimeout = 30 * time.Second
	}
	return &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: headerTimeout,
		},
	}
}

// validateMCPEndpointEgress applies the outbound egress (SSRF) policy to
// MCP HTTP endpoints unless the registration opted out.
func validateMCPEndpointEgress(ctx context.Context, registrationName string, transport MCPTransport) error {
	if strings.TrimSpace(transport.Endpoint) == "" {
		return fmt.Errorf("tool: mcp endpoint is required for %s", registrationName)
	}
	if err := security.ValidateOutboundURL(ctx, transport.Endpoint, transport.AllowPrivateNetwork); err != nil {
		return fmt.Errorf("tool: mcp endpoint for %s: %w", registrationName, err)
	}
	return nil
}

// resolveMCPHeaders builds the static HTTP header set for an MCP HTTP
// transport: static header values with {{config:key}} templates resolved
// from registration config, plus overlay-declared config-to-header
// mappings for sensitive values.
func resolveMCPHeaders(transport MCPTransport, config map[string]string, overlay *MCPOverlay) (map[string]string, error) {
	headers := make(map[string]string, len(transport.Headers))
	for key, value := range transport.Headers {
		resolved, err := resolveMCPHeaderValue(value, config)
		if err != nil {
			return nil, fmt.Errorf("header %q: %w", key, err)
		}
		headers[key] = resolved
	}

	if overlay != nil {
		for key, field := range overlay.Config {
			if strings.TrimSpace(field.Header) == "" {
				continue
			}
			value := strings.TrimSpace(config[key])
			if value == "" {
				continue
			}
			headers[field.Header] = value
		}
	}
	return headers, nil
}

func resolveMCPHeaderValue(value string, config map[string]string) (string, error) {
	if !strings.Contains(value, configHeaderTemplatePrefix) {
		return value, nil
	}

	var out strings.Builder
	remaining := value
	for {
		start := strings.Index(remaining, configHeaderTemplatePrefix)
		if start < 0 {
			out.WriteString(remaining)
			break
		}
		end := strings.Index(remaining[start:], configHeaderTemplateSuffix)
		if end < 0 {
			return "", fmt.Errorf("unterminated %s...}} template", configHeaderTemplatePrefix)
		}
		end += start

		key := strings.TrimSpace(remaining[start+len(configHeaderTemplatePrefix) : end])
		resolved := ""
		if key != "" {
			resolved = strings.TrimSpace(config[key])
		}
		if resolved == "" {
			return "", fmt.Errorf("config key %q has no value for header template", key)
		}

		out.WriteString(remaining[:start])
		out.WriteString(resolved)
		remaining = remaining[end+len(configHeaderTemplateSuffix):]
	}
	return out.String(), nil
}

// newMCPAuthorizer builds the authorization strategy for an MCP HTTP
// transport. Credentials are resolved from registration config (which is
// decrypted in memory and encrypted at rest), never from URLs or manifests.
func newMCPAuthorizer(registrationName string, transport MCPTransport, config map[string]string) (mcpclient.TransportAuthorizer, error) {
	auth := transport.Auth
	if auth == nil {
		return nil, nil
	}

	switch auth.Type {
	case MCPAuthBearer:
		token := strings.TrimSpace(config[auth.TokenRef])
		if token == "" {
			return nil, fmt.Errorf("tool: mcp auth token_ref %q for %s has no config value", auth.TokenRef, registrationName)
		}
		return &mcpclient.StaticTokenAuthorizer{
			Header: auth.Header,
			Scheme: auth.Scheme,
			Token:  token,
		}, nil

	case MCPAuthOAuth2:
		oauth := auth.OAuth
		if oauth == nil {
			return nil, fmt.Errorf("tool: mcp auth.type oauth2 for %s requires auth.oauth settings", registrationName)
		}
		clientSecret := ""
		if strings.TrimSpace(oauth.ClientSecretRef) != "" {
			clientSecret = config[oauth.ClientSecretRef]
			if strings.TrimSpace(clientSecret) == "" {
				return nil, fmt.Errorf("tool: mcp auth oauth client_secret_ref %q for %s has no config value", oauth.ClientSecretRef, registrationName)
			}
		}
		authorizer, err := mcpclient.NewOAuth2Authorizer(mcpclient.OAuth2Config{
			TokenURL:     oauth.TokenURL,
			ClientID:     oauth.ClientID,
			ClientSecret: clientSecret,
			Scopes:       oauth.Scopes,
			Resource:     oauth.Resource,
			Endpoint:     transport.Endpoint,
		})
		if err != nil {
			return nil, fmt.Errorf("tool: mcp oauth2 authorization for %s: %w", registrationName, err)
		}
		return authorizer, nil

	default:
		return nil, fmt.Errorf("tool: unsupported mcp auth type %q for %s", auth.Type, registrationName)
	}
}

func applyMCPConfigInjection(env map[string]string, config map[string]string, overlay *MCPOverlay) map[string]string {
	if env == nil {
		env = map[string]string{}
	}
	for key, value := range config {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		envKey := strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
		if overlay != nil {
			if field, ok := overlay.Config[key]; ok && strings.TrimSpace(field.EnvVar) != "" {
				envKey = field.EnvVar
			}
		}
		env[envKey] = value
	}
	return env
}
