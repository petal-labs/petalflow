package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	mcpclient "github.com/petal-labs/petalflow/tool/mcp"
)

const (
	defaultMCPReconnectAttempts = 3
	defaultMCPReconnectBackoff  = 250 * time.Millisecond
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

	case MCPModeSSE:
		dialer := func(ctx context.Context) (mcpclient.Transport, error) {
			return mcpclient.NewSSETransport(mcpclient.SSETransportConfig{
				Endpoint: transport.Endpoint,
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
