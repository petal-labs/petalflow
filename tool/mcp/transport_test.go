package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestStdioTransportSendReceive(t *testing.T) {
	transport, err := NewStdioTransport(context.Background(), StdioTransportConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPStdioHelperProcess", "--"},
		Env: map[string]string{
			"GO_WANT_MCP_STDIO_HELPER": "1",
		},
	})
	if err != nil {
		t.Fatalf("NewStdioTransport() error = %v", err)
	}
	defer transport.Close(context.Background())

	req := Message{
		JSONRPC: jsonRPCVersion,
		ID:      1,
		Method:  "tools/list",
	}
	if err := transport.Send(context.Background(), req); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resp, err := transport.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if resp.ID != 1 {
		t.Fatalf("response id = %d, want 1", resp.ID)
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Result, &payload); err != nil {
		t.Fatalf("Unmarshal(result) error = %v", err)
	}
	if payload["ok"] != true {
		t.Fatalf("result.ok = %v, want true", payload["ok"])
	}
}

func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_STDIO_HELPER") != "1" {
		return
	}

	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for {
		var req Message
		if err := decoder.Decode(&req); err != nil {
			os.Exit(0)
		}
		resp := Message{
			JSONRPC: jsonRPCVersion,
			ID:      req.ID,
			Result:  mustRawJSON(t, map[string]any{"ok": true, "method": req.Method}),
		}
		if err := encoder.Encode(resp); err != nil {
			os.Exit(2)
		}
	}
}

func TestSSETransportSendReceive(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := Message{
				JSONRPC: jsonRPCVersion,
				ID:      7,
				Result:  mustRawJSON(t, map[string]any{"pong": true}),
			}
			responseBytes, _ := json.Marshal(body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(responseBytes)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	transport, err := NewSSETransport(SSETransportConfig{
		Endpoint: "http://mcp.local/rpc",
		Client:   client,
	})
	if err != nil {
		t.Fatalf("NewSSETransport() error = %v", err)
	}
	defer transport.Close(context.Background())

	req := Message{
		JSONRPC: jsonRPCVersion,
		ID:      7,
		Method:  "ping",
	}
	if err := transport.Send(context.Background(), req); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resp, err := transport.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if resp.ID != 7 {
		t.Fatalf("response id = %d, want 7", resp.ID)
	}
}

func TestReconnectingTransportReconnectsAfterError(t *testing.T) {
	var dials int32
	dialer := func(ctx context.Context) (Transport, error) {
		attempt := atomic.AddInt32(&dials, 1)
		return &flakyTransport{
			failFirstSend: attempt == 1,
			response: Message{
				JSONRPC: jsonRPCVersion,
				ID:      9,
				Result:  mustRawJSON(t, map[string]any{"ok": true}),
			},
		}, nil
	}

	transport, err := NewReconnectingTransport(context.Background(), dialer, ReconnectConfig{
		MaxAttempts: 3,
		BaseBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewReconnectingTransport() error = %v", err)
	}
	defer transport.Close(context.Background())

	req := Message{
		JSONRPC: jsonRPCVersion,
		ID:      9,
		Method:  "tools/list",
	}
	if err := transport.Send(context.Background(), req); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resp, err := transport.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if resp.ID != 9 {
		t.Fatalf("response id = %d, want 9", resp.ID)
	}
	if atomic.LoadInt32(&dials) < 2 {
		t.Fatalf("dial attempts = %d, want >= 2", dials)
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type flakyTransport struct {
	failFirstSend bool
	sent          bool
	response      Message
}

func (f *flakyTransport) Send(ctx context.Context, message Message) error {
	if f.failFirstSend && !f.sent {
		f.sent = true
		return errors.New("send failed")
	}
	f.sent = true
	return nil
}

func (f *flakyTransport) Receive(ctx context.Context) (Message, error) {
	if !f.sent {
		return Message{}, errors.New("nothing sent")
	}
	return f.response, nil
}

func (f *flakyTransport) Close(ctx context.Context) error {
	return nil
}

func mustRawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return data
}
