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

// Registration is the persisted record for a tool instance in the registry.
type Registration struct {
	Name        string         `json:"name"`
	Source      string         `json:"source,omitempty"`
	Manifest    Manifest       `json:"manifest"`
	Status      Status         `json:"status"`
	Enabled     bool           `json:"enabled"`
	OverlayPath string         `json:"overlay_path,omitempty"`
	Config      map[string]any `json:"config,omitempty"`
	CreatedAt   time.Time      `json:"created_at,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at,omitempty"`
}

// ActionNames returns registered action names in deterministic order.
func (r Registration) ActionNames() []string {
	names := make([]string, 0, len(r.Manifest.Actions))
	for name := range r.Manifest.Actions {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Store abstracts persistence for CLI (file) and daemon (service-backed) modes.
type Store interface {
	List(ctx context.Context) ([]Registration, error)
	Get(ctx context.Context, name string) (Registration, bool, error)
	Upsert(ctx context.Context, reg Registration) error
	Delete(ctx context.Context, name string) error
}
