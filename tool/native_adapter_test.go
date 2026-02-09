package tool

import (
	"context"
	"errors"
	"testing"
)

type fakeNativeTool struct {
	name       string
	manifest   Manifest
	wantAction string
	outputs    map[string]any
	err        error
}

func (f fakeNativeTool) Name() string {
	return f.name
}

func (f fakeNativeTool) Manifest() Manifest {
	return f.manifest
}

func (f fakeNativeTool) Invoke(ctx context.Context, action string, inputs map[string]any, config map[string]any) (map[string]any, error) {
	if f.wantAction != "" && action != f.wantAction {
		return nil, errors.New("unexpected action")
	}
	return f.outputs, f.err
}

func TestNativeAdapterInvoke(t *testing.T) {
	adapter := NewNativeAdapter(fakeNativeTool{
		name:       "native_test",
		manifest:   NewManifest("native_test"),
		wantAction: "list",
		outputs: map[string]any{
			"count": 1,
		},
	})

	resp, err := adapter.Invoke(context.Background(), InvokeRequest{
		Action: "list",
		Inputs: map[string]any{"bucket": "reports"},
	})
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
	if resp.Outputs["count"] != 1 {
		t.Errorf("outputs[count] = %v, want 1", resp.Outputs["count"])
	}
}

func TestNativeAdapterInvokeNoTool(t *testing.T) {
	adapter := NewNativeAdapter(nil)
	_, err := adapter.Invoke(context.Background(), InvokeRequest{Action: "list"})
	if err == nil {
		t.Fatal("Invoke error = nil, want non-nil")
	}
}
