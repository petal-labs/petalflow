package tool

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBuiltinRegistrationsIncludePhaseOneNativeTools(t *testing.T) {
	regs := BuiltinRegistrations()

	if len(regs) < 2 {
		t.Fatalf("len(BuiltinRegistrations) = %d, want >= 2", len(regs))
	}

	names := map[string]ToolRegistration{}
	for _, reg := range regs {
		names[reg.Name] = reg
	}

	for _, name := range []string{"template_render", "http_fetch"} {
		reg, ok := names[name]
		if !ok {
			t.Fatalf("missing built-in registration %q", name)
		}
		if reg.Origin != OriginNative {
			t.Fatalf("%s origin = %q, want %q", name, reg.Origin, OriginNative)
		}
		if reg.Manifest.Transport.Type != TransportTypeNative {
			t.Fatalf("%s transport = %q, want %q", name, reg.Manifest.Transport.Type, TransportTypeNative)
		}
	}
}

func TestTemplateRenderBuiltinInvoke(t *testing.T) {
	native, ok := LookupBuiltinNativeTool("template_render")
	if !ok {
		t.Fatal("LookupBuiltinNativeTool(template_render) = false")
	}

	adapter := NewNativeAdapter(native)
	resp, err := adapter.Invoke(context.Background(), InvokeRequest{
		Action: "render",
		Inputs: map[string]any{
			"template": "Hello, {{.name}}!",
			"name":     "Ada",
		},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if got := resp.Outputs["rendered"]; got != "Hello, Ada!" {
		t.Fatalf("outputs[rendered] = %v, want %q", got, "Hello, Ada!")
	}
}

func TestHTTPFetchBuiltinInvoke(t *testing.T) {
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
		}, nil
	})
	t.Cleanup(func() {
		http.DefaultTransport = prevTransport
	})

	native, ok := LookupBuiltinNativeTool("http_fetch")
	if !ok {
		t.Fatal("LookupBuiltinNativeTool(http_fetch) = false")
	}

	adapter := NewNativeAdapter(native)
	resp, err := adapter.Invoke(context.Background(), InvokeRequest{
		Action: "fetch",
		Inputs: map[string]any{
			"url": "http://unit-test.local/ok",
		},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if got := resp.Outputs["status_code"]; got != 200 {
		t.Fatalf("outputs[status_code] = %v, want 200", got)
	}
}
