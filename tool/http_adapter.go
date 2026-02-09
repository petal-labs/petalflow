package tool

import "context"

// HTTPAdapter is the runtime adapter for HTTP-backed tools.
type HTTPAdapter struct {
	reg Registration
}

// NewHTTPAdapter creates an HTTP adapter from a registration.
func NewHTTPAdapter(reg Registration) *HTTPAdapter {
	return &HTTPAdapter{reg: reg}
}

// Invoke executes an action over HTTP.
//
// Implementation is intentionally deferred to follow-up tasks.
func (a *HTTPAdapter) Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error) {
	return InvokeResponse{}, ErrNotImplemented
}

// Close releases any adapter resources.
func (a *HTTPAdapter) Close(ctx context.Context) error {
	return nil
}
