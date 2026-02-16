package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestStdioAdapterInvoke(t *testing.T) {
	reg := ToolRegistration{
		Name:     "echo_stdio",
		Origin:   OriginStdio,
		Manifest: NewManifest("echo_stdio"),
	}
	reg.Manifest.Transport = NewStdioTransport(StdioTransport{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioAdapterHelperProcess", "--"},
		Env: map[string]string{
			"GO_WANT_STDIO_HELPER": "1",
		},
	})

	adapter := NewStdioAdapter(reg)
	resp, err := adapter.Invoke(context.Background(), InvokeRequest{
		ToolName: "echo_stdio",
		Action:   "echo",
		Inputs: map[string]any{
			"value": "hello",
		},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if got := resp.Outputs["action"]; got != "echo" {
		t.Fatalf("outputs[action] = %v, want echo", got)
	}
	if got := resp.Outputs["ok"]; got != true {
		t.Fatalf("outputs[ok] = %v, want true", got)
	}
}

func TestStdioAdapterInvokeError(t *testing.T) {
	reg := ToolRegistration{
		Name:     "echo_stdio",
		Origin:   OriginStdio,
		Manifest: NewManifest("echo_stdio"),
	}
	reg.Manifest.Transport = NewStdioTransport(StdioTransport{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioAdapterHelperProcess", "--"},
		Env: map[string]string{
			"GO_WANT_STDIO_HELPER": "1",
			"GO_STDIO_HELPER_FAIL": "1",
		},
	})

	adapter := NewStdioAdapter(reg)
	_, err := adapter.Invoke(context.Background(), InvokeRequest{Action: "echo"})
	if err == nil {
		t.Fatal("Invoke() error = nil, want non-nil")
	}
}

func TestStdioAdapterInvokeValidation(t *testing.T) {
	t.Run("nil adapter", func(t *testing.T) {
		var adapter *StdioAdapter
		_, err := adapter.Invoke(context.Background(), InvokeRequest{Action: "echo"})
		if err == nil {
			t.Fatal("Invoke() error = nil, want non-nil")
		}
		if got := toolErrorCode(err); got != ToolErrorCodeInvalidRequest {
			t.Fatalf("toolErrorCode = %q, want %q", got, ToolErrorCodeInvalidRequest)
		}
	})

	t.Run("empty command", func(t *testing.T) {
		reg := ToolRegistration{
			Name:     "bad_stdio",
			Origin:   OriginStdio,
			Manifest: NewManifest("bad_stdio"),
		}
		reg.Manifest.Transport = NewStdioTransport(StdioTransport{
			Command: "   ",
		})

		adapter := NewStdioAdapter(reg)
		_, err := adapter.Invoke(context.Background(), InvokeRequest{Action: "echo"})
		if err == nil {
			t.Fatal("Invoke() error = nil, want non-nil")
		}
		if got := toolErrorCode(err); got != ToolErrorCodeInvalidRequest {
			t.Fatalf("toolErrorCode = %q, want %q", got, ToolErrorCodeInvalidRequest)
		}
	})

	t.Run("empty action", func(t *testing.T) {
		reg := ToolRegistration{
			Name:     "echo_stdio",
			Origin:   OriginStdio,
			Manifest: NewManifest("echo_stdio"),
		}
		reg.Manifest.Transport = NewStdioTransport(StdioTransport{
			Command: os.Args[0],
			Args:    []string{"-test.run=TestStdioAdapterHelperProcess", "--"},
			Env: map[string]string{
				"GO_WANT_STDIO_HELPER": "1",
			},
		})

		adapter := NewStdioAdapter(reg)
		_, err := adapter.Invoke(context.Background(), InvokeRequest{Action: "  "})
		if err == nil {
			t.Fatal("Invoke() error = nil, want non-nil")
		}
		if got := toolErrorCode(err); got != ToolErrorCodeActionNotFound {
			t.Fatalf("toolErrorCode = %q, want %q", got, ToolErrorCodeActionNotFound)
		}
	})
}

func TestStdioAdapterInvokeDecodeError(t *testing.T) {
	reg := ToolRegistration{
		Name:     "echo_stdio",
		Origin:   OriginStdio,
		Manifest: NewManifest("echo_stdio"),
	}
	reg.Manifest.Transport = NewStdioTransport(StdioTransport{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioAdapterHelperProcess", "--"},
		Env: map[string]string{
			"GO_WANT_STDIO_HELPER":     "1",
			"GO_STDIO_HELPER_BAD_JSON": "1",
		},
	})

	adapter := NewStdioAdapter(reg)
	_, err := adapter.Invoke(context.Background(), InvokeRequest{Action: "echo"})
	if err == nil {
		t.Fatal("Invoke() error = nil, want non-nil")
	}
	if got := toolErrorCode(err); got != ToolErrorCodeInvocationFailed {
		t.Fatalf("toolErrorCode = %q, want %q", got, ToolErrorCodeInvocationFailed)
	}
	if !strings.Contains(err.Error(), "stdio invoke failed") {
		t.Fatalf("Invoke() error = %v, want wrapped stdio invoke failure", err)
	}
}

func TestStdioAdapterHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_STDIO_HELPER") != "1" {
		return
	}

	if os.Getenv("GO_STDIO_HELPER_FAIL") == "1" {
		_, _ = fmt.Fprintln(os.Stderr, "helper failed")
		os.Exit(2)
	}
	if os.Getenv("GO_STDIO_HELPER_BAD_JSON") == "1" {
		_, _ = fmt.Fprintln(os.Stdout, "{bad json")
		os.Exit(0)
	}

	var req InvokeRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(2)
	}

	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"outputs": map[string]any{
			"ok":     true,
			"action": req.Action,
		},
	})
	os.Exit(0)
}
