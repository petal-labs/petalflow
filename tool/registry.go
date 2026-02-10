package tool

import (
	"context"
	"slices"
	"time"
)

// Status indicates registry-level availability of a tool.
type Status string

const (
	StatusReady      Status = "ready"
	StatusUnhealthy  Status = "unhealthy"
	StatusDisabled   Status = "disabled"
	StatusUnverified Status = "unverified"
)

// ToolOrigin indicates how a tool is integrated into PetalFlow.
type ToolOrigin string

const (
	OriginNative ToolOrigin = "native"
	OriginMCP    ToolOrigin = "mcp"
	OriginHTTP   ToolOrigin = "http"
	OriginStdio  ToolOrigin = "stdio"
)

// ToolOverlay contains optional overlay metadata used for MCP tools.
type ToolOverlay struct {
	Path string `json:"path,omitempty"`
}

// ToolRegistration is the persisted record for a tool instance in the registry.
type ToolRegistration struct {
	Name            string            `json:"name"`
	Manifest        ToolManifest      `json:"manifest"`
	Origin          ToolOrigin        `json:"origin,omitempty"`
	Config          map[string]string `json:"config,omitempty"`
	Status          Status            `json:"status"`
	RegisteredAt    time.Time         `json:"registered_at,omitempty"`
	LastHealthCheck time.Time         `json:"last_health_check,omitempty"`
	HealthFailures  int               `json:"health_failures,omitempty"`
	Overlay         *ToolOverlay      `json:"overlay,omitempty"`
	Enabled         bool              `json:"enabled,omitempty"`
}

// Registration is kept as an alias for backward compatibility while the package
// adopts ToolRegistration naming used in the implementation plan.
type Registration = ToolRegistration

// ActionNames returns registered action names in deterministic order.
func (r ToolRegistration) ActionNames() []string {
	names := make([]string, 0, len(r.Manifest.Actions))
	for name := range r.Manifest.Actions {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Store abstracts persistence for CLI (file) and daemon (service-backed) modes.
type Store interface {
	List(ctx context.Context) ([]ToolRegistration, error)
	Get(ctx context.Context, name string) (ToolRegistration, bool, error)
	Upsert(ctx context.Context, reg ToolRegistration) error
	Delete(ctx context.Context, name string) error
}
