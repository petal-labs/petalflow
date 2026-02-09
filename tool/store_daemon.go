package tool

import (
	"context"
	"errors"
)

var errNilDaemonBackend = errors.New("tool: daemon backend is nil")

// DaemonRegistryBackend represents the server-side persistence contract for tools.
// It allows the same ToolRegistration model to be used in daemon/API paths.
type DaemonRegistryBackend interface {
	ListToolRegistrations(ctx context.Context) ([]ToolRegistration, error)
	GetToolRegistration(ctx context.Context, name string) (ToolRegistration, bool, error)
	UpsertToolRegistration(ctx context.Context, reg ToolRegistration) error
	DeleteToolRegistration(ctx context.Context, name string) error
}

// DaemonStore adapts a daemon backend to the shared Store interface.
type DaemonStore struct {
	backend DaemonRegistryBackend
}

// NewDaemonStore creates a Store backed by daemon persistence.
func NewDaemonStore(backend DaemonRegistryBackend) *DaemonStore {
	return &DaemonStore{backend: backend}
}

// List returns all tool registrations from daemon persistence.
func (s *DaemonStore) List(ctx context.Context) ([]ToolRegistration, error) {
	if s == nil || s.backend == nil {
		return nil, errNilDaemonBackend
	}
	return s.backend.ListToolRegistrations(ctx)
}

// Get returns a tool registration by name from daemon persistence.
func (s *DaemonStore) Get(ctx context.Context, name string) (ToolRegistration, bool, error) {
	if s == nil || s.backend == nil {
		return ToolRegistration{}, false, errNilDaemonBackend
	}
	return s.backend.GetToolRegistration(ctx, name)
}

// Upsert inserts or updates a tool registration in daemon persistence.
func (s *DaemonStore) Upsert(ctx context.Context, reg ToolRegistration) error {
	if s == nil || s.backend == nil {
		return errNilDaemonBackend
	}
	return s.backend.UpsertToolRegistration(ctx, reg)
}

// Delete removes a tool registration from daemon persistence.
func (s *DaemonStore) Delete(ctx context.Context, name string) error {
	if s == nil || s.backend == nil {
		return errNilDaemonBackend
	}
	return s.backend.DeleteToolRegistration(ctx, name)
}

var _ Store = (*DaemonStore)(nil)
