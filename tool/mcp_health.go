package tool

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// EvaluateMCPHealth checks MCP availability using overlay strategy hints.
// mcpHealthTimeout returns the per-probe HTTP timeout, honoring the overlay's
// configured health timeout and falling back to a default.
func mcpHealthTimeout(overlay *MCPOverlay) time.Duration {
	if overlay != nil && overlay.Health.TimeoutMS > 0 {
		return time.Duration(overlay.Health.TimeoutMS) * time.Millisecond
	}
	return defaultMCPHealthTimeout
}

func EvaluateMCPHealth(ctx context.Context, reg Registration) HealthReport {
	report := HealthReport{
		ToolName:  reg.Name,
		State:     HealthUnknown,
		CheckedAt: time.Now().UTC(),
	}

	overlay, err := loadOverlayForRegistration(reg)
	if err != nil {
		report.State = HealthUnhealthy
		report.ErrorMessage = err.Error()
		return report
	}

	strategy := "ping"
	if overlay != nil && strings.TrimSpace(overlay.Health.Strategy) != "" {
		strategy = strings.ToLower(strings.TrimSpace(overlay.Health.Strategy))
	}

	start := time.Now()
	switch strategy {
	case "endpoint":
		endpoint := ""
		if overlay != nil {
			endpoint = strings.TrimSpace(overlay.Health.Endpoint)
		}
		if endpoint == "" {
			report.State = HealthUnhealthy
			report.ErrorMessage = "overlay health endpoint is required for endpoint strategy"
			return report
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			report.State = HealthUnhealthy
			report.ErrorMessage = err.Error()
			return report
		}
		resp, err := sharedHTTPClientPool.client(mcpHealthTimeout(overlay)).Do(req)
		if err != nil {
			report.State = HealthUnhealthy
			report.ErrorMessage = err.Error()
			return report
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= http.StatusBadRequest {
			report.State = HealthUnhealthy
			report.ErrorMessage = fmt.Sprintf("health endpoint returned status %d", resp.StatusCode)
			return report
		}

	case "process", "connection", "ping":
		transport, ok := reg.Manifest.Transport.AsMCP()
		if !ok {
			report.State = HealthUnhealthy
			report.ErrorMessage = "registration transport is not mcp"
			return report
		}
		client, cleanup, err := newMCPClientFromConfig(ctx, reg.Name, transport, reg.Config, overlay)
		if err != nil {
			report.State = HealthUnhealthy
			report.ErrorMessage = err.Error()
			return report
		}
		defer cleanup()
		if _, err := client.Initialize(ctx); err != nil {
			report.State = HealthUnhealthy
			report.ErrorMessage = err.Error()
			return report
		}

		if strategy == "ping" {
			if _, err := client.ListTools(ctx); err != nil {
				report.State = HealthUnhealthy
				report.ErrorMessage = err.Error()
				return report
			}
		}

	default:
		report.State = HealthUnhealthy
		report.ErrorMessage = fmt.Sprintf("unsupported health strategy %q", strategy)
		return report
	}

	report.State = HealthHealthy
	report.LatencyMS = time.Since(start).Milliseconds()
	return report
}
