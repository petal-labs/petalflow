package tool

import "context"

// StdioAdapter is the runtime adapter for subprocess-backed tools.
type StdioAdapter struct {
	reg Registration
}

// NewStdioAdapter creates a stdio adapter from a registration.
func NewStdioAdapter(reg Registration) *StdioAdapter {
	return &StdioAdapter{reg: reg}
}

// Invoke executes an action through a long-running subprocess transport.
//
// Implementation is intentionally deferred to follow-up tasks.
func (a *StdioAdapter) Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error) {
	return InvokeResponse{}, ErrNotImplemented
}

// Close releases any adapter resources.
func (a *StdioAdapter) Close(ctx context.Context) error {
	return nil
}
