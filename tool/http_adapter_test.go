package tool

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHTTPAdapterInvoke(t *testing.T) {
	adapterResponse := `{"outputs":{"ok":true},"duration_ms":42}`
	reg := ToolRegistration{
		Name:     "echo_http",
		Origin:   OriginHTTP,
		Manifest: NewManifest("echo_http"),
	}
	reg.Manifest.Transport = NewHTTPTransport(HTTPTransport{
		Endpoint: "http://unit-test.local/echo",
	})

	adapter := NewHTTPAdapter(reg)
	adapter.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if payload["action"] != "echo" {
				t.Fatalf("payload action = %v, want echo", payload["action"])
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(adapterResponse)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	resp, err := adapter.Invoke(context.Background(), InvokeRequest{
		ToolName: "echo_http",
		Action:   "echo",
		Inputs: map[string]any{
			"value": "hello",
		},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if got := resp.Outputs["ok"]; got != true {
		t.Fatalf("outputs[ok] = %v, want true", got)
	}
	if resp.DurationMS != 42 {
		t.Fatalf("DurationMS = %d, want 42", resp.DurationMS)
	}
}

func TestHTTPAdapterInvokeStatusError(t *testing.T) {
	reg := ToolRegistration{
		Name:     "echo_http",
		Origin:   OriginHTTP,
		Manifest: NewManifest("echo_http"),
	}
	reg.Manifest.Transport = NewHTTPTransport(HTTPTransport{
		Endpoint: "http://unit-test.local/echo",
	})

	adapter := NewHTTPAdapter(reg)
	adapter.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("downstream failure")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	_, err := adapter.Invoke(context.Background(), InvokeRequest{Action: "echo"})
	if err == nil {
		t.Fatal("Invoke() error = nil, want non-nil")
	}
}

type roundTripFunc func(r *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
