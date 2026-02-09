package tool

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestDefaultAdapterFactoryNative(t *testing.T) {
	reg, ok := BuiltinRegistration("template_render")
	if !ok {
		t.Fatal("BuiltinRegistration(template_render) = false")
	}

	factory := DefaultAdapterFactory{NativeLookup: LookupBuiltinNativeTool}
	adapter, err := factory.New(reg)
	if err != nil {
		t.Fatalf("factory.New() error = %v", err)
	}

	resp, err := invokeViaAdapter(context.Background(), adapter, InvokeRequest{
		Action: "render",
		Inputs: map[string]any{
			"template": "Hi {{.name}}",
			"name":     "Ada",
		},
	})
	if err != nil {
		t.Fatalf("invokeViaAdapter() error = %v", err)
	}
	if got := resp.Outputs["rendered"]; got != "Hi Ada" {
		t.Fatalf("outputs[rendered] = %v, want %q", got, "Hi Ada")
	}
}

func TestDefaultAdapterFactoryHTTP(t *testing.T) {
	reg := ToolRegistration{
		Name:     "http_tool",
		Origin:   OriginHTTP,
		Manifest: NewManifest("http_tool"),
	}
	reg.Manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://unit-test.local/check"})
	reg.Manifest.Actions["check"] = ActionSpec{
		Inputs:  map[string]FieldSpec{},
		Outputs: map[string]FieldSpec{},
	}

	factory := DefaultAdapterFactory{NativeLookup: LookupBuiltinNativeTool}
	adapter, err := factory.New(reg)
	if err != nil {
		t.Fatalf("factory.New() error = %v", err)
	}
	httpAdapter, ok := adapter.(*HTTPAdapter)
	if !ok {
		t.Fatalf("adapter type = %T, want *HTTPAdapter", adapter)
	}
	httpAdapter.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"outputs":{"ok":true}}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	resp, err := invokeViaAdapter(context.Background(), adapter, InvokeRequest{Action: "check"})
	if err != nil {
		t.Fatalf("invokeViaAdapter() error = %v", err)
	}
	if got := resp.Outputs["ok"]; got != true {
		t.Fatalf("outputs[ok] = %v, want true", got)
	}
}

func TestDefaultAdapterFactoryStdio(t *testing.T) {
	reg := ToolRegistration{
		Name:     "stdio_tool",
		Origin:   OriginStdio,
		Manifest: NewManifest("stdio_tool"),
	}
	reg.Manifest.Transport = NewStdioTransport(StdioTransport{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioAdapterHelperProcess", "--"},
		Env: map[string]string{
			"GO_WANT_STDIO_HELPER": "1",
		},
	})
	reg.Manifest.Actions["check"] = ActionSpec{
		Inputs:  map[string]FieldSpec{},
		Outputs: map[string]FieldSpec{},
	}

	factory := DefaultAdapterFactory{NativeLookup: LookupBuiltinNativeTool}
	adapter, err := factory.New(reg)
	if err != nil {
		t.Fatalf("factory.New() error = %v", err)
	}

	resp, err := invokeViaAdapter(context.Background(), adapter, InvokeRequest{Action: "check"})
	if err != nil {
		t.Fatalf("invokeViaAdapter() error = %v", err)
	}
	if got := resp.Outputs["ok"]; got != true {
		t.Fatalf("outputs[ok] = %v, want true", got)
	}
}

func invokeViaAdapter(ctx context.Context, adapter Adapter, req InvokeRequest) (InvokeResponse, error) {
	defer adapter.Close(ctx)
	return adapter.Invoke(ctx, req)
}
