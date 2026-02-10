package tool

import (
	"context"
	"fmt"
	"strings"
)

// BuildMCPRegistration discovers MCP tools and builds a registration.
func BuildMCPRegistration(ctx context.Context, name string, transport MCPTransport, config map[string]string, overlayPath string) (Registration, error) {
	var overlay *MCPOverlay
	if strings.TrimSpace(overlayPath) != "" {
		parsed, diags, err := ParseMCPOverlayFile(overlayPath)
		if err != nil {
			return Registration{}, err
		}
		if hasValidationErrors(diags) {
			return Registration{}, &RegistrationValidationError{
				Code:    RegistrationValidationFailedCode,
				Message: "Tool registration failed validation",
				Details: diags,
			}
		}
		overlay = &parsed
	}

	manifest, err := DiscoverMCPManifest(ctx, MCPDiscoveryConfig{
		Name:      name,
		Transport: transport,
		Config:    config,
		Overlay:   overlay,
	})
	if err != nil {
		return Registration{}, err
	}

	reg := Registration{
		Name:     name,
		Origin:   OriginMCP,
		Manifest: manifest,
		Config:   cloneStringMap(config),
		Status:   StatusReady,
		Enabled:  true,
	}
	if strings.TrimSpace(overlayPath) != "" {
		reg.Overlay = &ToolOverlay{Path: overlayPath}
	}

	health := EvaluateMCPHealth(ctx, reg)
	switch health.State {
	case HealthHealthy:
		reg.Status = StatusReady
		reg.HealthFailures = 0
	case HealthUnhealthy:
		reg.HealthFailures = 1
		if reg.HealthFailures >= unhealthyThresholdForRegistration(reg) {
			reg.Status = StatusUnhealthy
		} else {
			reg.Status = StatusUnverified
		}
	default:
		reg.Status = StatusUnverified
	}
	reg.LastHealthCheck = health.CheckedAt

	return reg, nil
}

// RefreshMCPRegistration refreshes an existing MCP registration via discovery.
func RefreshMCPRegistration(ctx context.Context, existing Registration) (Registration, error) {
	transport, ok := existing.Manifest.Transport.AsMCP()
	if !ok {
		return Registration{}, fmt.Errorf("tool %q is not an mcp registration", existing.Name)
	}

	overlayPath := ""
	if existing.Overlay != nil {
		overlayPath = existing.Overlay.Path
	}

	updated, err := BuildMCPRegistration(ctx, existing.Name, transport, existing.Config, overlayPath)
	if err != nil {
		return Registration{}, err
	}
	updated.RegisteredAt = existing.RegisteredAt
	if updated.RegisteredAt.IsZero() {
		updated.RegisteredAt = existing.RegisteredAt
	}
	updated.Enabled = existing.Enabled
	return updated, nil
}
