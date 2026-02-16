package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
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

func TestWriteStdioInvokeRequest(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		writer := &captureWriteCloser{}
		err := writeStdioInvokeRequest(nil, writer, InvokeRequest{
			ToolName: "echo_stdio",
			Action:   "echo",
			Inputs: map[string]any{
				"value": "hello",
			},
		})
		if err != nil {
			t.Fatalf("writeStdioInvokeRequest() error = %v, want nil", err)
		}
		if !writer.closed {
			t.Fatal("writer.closed = false, want true")
		}

		var decoded map[string]any
		if err := json.Unmarshal(writer.buf.Bytes(), &decoded); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if got := decoded["action"]; got != "echo" {
			t.Fatalf("encoded action = %v, want echo", got)
		}
	})

	t.Run("encode error", func(t *testing.T) {
		writer := &captureWriteCloser{writeErr: errors.New("write boom")}
		cmd := exec.Command("true")
		err := writeStdioInvokeRequest(cmd, writer, InvokeRequest{Action: "echo"})
		if err == nil {
			t.Fatal("writeStdioInvokeRequest() error = nil, want non-nil")
		}
		if got := toolErrorCode(err); got != ToolErrorCodeInvalidRequest {
			t.Fatalf("toolErrorCode = %q, want %q", got, ToolErrorCodeInvalidRequest)
		}
	})

	t.Run("close error", func(t *testing.T) {
		writer := &captureWriteCloser{closeErr: errors.New("close boom")}
		cmd := exec.Command("true")
		err := writeStdioInvokeRequest(cmd, writer, InvokeRequest{Action: "echo"})
		if err == nil {
			t.Fatal("writeStdioInvokeRequest() error = nil, want non-nil")
		}
		if got := toolErrorCode(err); got != ToolErrorCodeTransportFailure {
			t.Fatalf("toolErrorCode = %q, want %q", got, ToolErrorCodeTransportFailure)
		}
	})
}

func TestReadStdioInvokeOutputReadError(t *testing.T) {
	cmd := exec.Command("true")
	_, _, _, err := readStdioInvokeOutput(
		cmd,
		&failingReadCloser{err: errors.New("stdout read boom")},
		io.NopCloser(strings.NewReader("stderr")),
	)
	if err == nil {
		t.Fatal("readStdioInvokeOutput() error = nil, want non-nil")
	}
	if got := toolErrorCode(err); got != ToolErrorCodeTransportFailure {
		t.Fatalf("toolErrorCode = %q, want %q", got, ToolErrorCodeTransportFailure)
	}
}

func TestDecodeStdioInvokeResult(t *testing.T) {
	t.Run("wait error uses fallback message", func(t *testing.T) {
		_, err := decodeStdioInvokeResult(
			context.Background(),
			[]byte(`{"outputs":{"ok":true}}`),
			[]byte("   "),
			errors.New("process exited"),
			time.Now(),
		)
		if err == nil {
			t.Fatal("decodeStdioInvokeResult() error = nil, want non-nil")
		}
		if got := toolErrorCode(err); got != ToolErrorCodeUpstreamFailure {
			t.Fatalf("toolErrorCode = %q, want %q", got, ToolErrorCodeUpstreamFailure)
		}
		if !strings.Contains(err.Error(), "process exited") {
			t.Fatalf("error = %v, want fallback wait error message", err)
		}
	})

	t.Run("context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := decodeStdioInvokeResult(
			ctx,
			[]byte(`{"outputs":{"ok":true}}`),
			nil,
			nil,
			time.Now(),
		)
		if err == nil {
			t.Fatal("decodeStdioInvokeResult() error = nil, want non-nil")
		}
		if got := toolErrorCode(err); got != ToolErrorCodeTransportFailure {
			t.Fatalf("toolErrorCode = %q, want %q", got, ToolErrorCodeTransportFailure)
		}
	})
}

func TestWithStdioInvokeTimeout(t *testing.T) {
	parent := context.Background()
	ctx, cancel := withStdioInvokeTimeout(parent, 50*time.Millisecond)
	defer cancel()

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		t.Fatal("withStdioInvokeTimeout() should set deadline when parent has none")
	}

	parentWithDeadline, parentCancel := context.WithTimeout(context.Background(), time.Second)
	defer parentCancel()
	ctx2, cancel2 := withStdioInvokeTimeout(parentWithDeadline, 50*time.Millisecond)
	defer cancel2()

	parentDeadline, _ := parentWithDeadline.Deadline()
	deadline2, hasDeadline2 := ctx2.Deadline()
	if !hasDeadline2 {
		t.Fatal("expected deadline on returned context")
	}
	if !deadline2.Equal(parentDeadline) {
		t.Fatalf("deadline changed: got %v, want %v", deadline2, parentDeadline)
	}
}

type captureWriteCloser struct {
	buf      bytes.Buffer
	closed   bool
	writeErr error
	closeErr error
}

func (w *captureWriteCloser) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.buf.Write(p)
}

func (w *captureWriteCloser) Close() error {
	w.closed = true
	return w.closeErr
}

type failingReadCloser struct {
	err error
}

func (r *failingReadCloser) Read(p []byte) (int, error) {
	return 0, r.err
}

func (r *failingReadCloser) Close() error {
	return nil
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
