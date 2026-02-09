package tool

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotImplemented is returned by placeholder adapters during skeleton phase.
	ErrNotImplemented = errors.New("tool: not implemented")
	// ErrActionNotFound indicates the requested action does not exist in a manifest.
	ErrActionNotFound = errors.New("tool: action not found")
)

// InvokeRequest is the transport-agnostic invocation payload.
type InvokeRequest struct {
	ToolName  string         `json:"tool_name,omitempty"`
	Action    string         `json:"action"`
	Inputs    map[string]any `json:"inputs,omitempty"`
	Config    map[string]any `json:"config,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

// InvokeResponse is the transport-agnostic invocation result.
type InvokeResponse struct {
	Outputs    map[string]any `json:"outputs,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// Adapter hides transport details (native, HTTP, stdio, MCP).
type Adapter interface {
	Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error)
	Close(ctx context.Context) error
}

// AdapterFactory builds adapters from a tool registration.
type AdapterFactory interface {
	New(reg Registration) (Adapter, error)
}

func elapsedMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}
