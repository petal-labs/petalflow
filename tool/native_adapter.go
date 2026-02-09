package tool

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// NativeTool is an in-process implementation that can be invoked directly.
type NativeTool interface {
	Name() string
	Manifest() Manifest
	Invoke(ctx context.Context, action string, inputs map[string]any, config map[string]any) (map[string]any, error)
}

// NativeAdapter is the in-process adapter for native Go tools.
type NativeAdapter struct {
	tool NativeTool
}

// NewNativeAdapter wraps a NativeTool as a transport adapter.
func NewNativeAdapter(tool NativeTool) *NativeAdapter {
	return &NativeAdapter{tool: tool}
}

// Invoke executes a native tool action in-process.
func (a *NativeAdapter) Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error) {
	if a == nil || a.tool == nil {
		return InvokeResponse{}, errors.New("tool: native adapter has no tool")
	}
	if req.Action == "" {
		return InvokeResponse{}, fmt.Errorf("%w: empty action", ErrActionNotFound)
	}

	start := time.Now()
	outputs, err := a.tool.Invoke(ctx, req.Action, req.Inputs, req.Config)
	if err != nil {
		return InvokeResponse{}, err
	}

	return InvokeResponse{
		Outputs:    outputs,
		DurationMS: elapsedMS(start),
	}, nil
}

// Close is a no-op for native tools.
func (a *NativeAdapter) Close(ctx context.Context) error {
	return nil
}
