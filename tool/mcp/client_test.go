package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

type mockTransport struct {
	mu            sync.Mutex
	closed        bool
	sendErr       error
	receiveErr    error
	responses     []Message
	notifications []Message
	lastRequests  []Message
	handler       func(req Message) Message
}

func (m *mockTransport) Send(ctx context.Context, message Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sendErr != nil {
		return m.sendErr
	}
	if message.Method != "" && message.ID == 0 {
		m.notifications = append(m.notifications, message)
		return nil
	}

	m.lastRequests = append(m.lastRequests, message)
	if m.handler != nil {
		m.responses = append(m.responses, m.handler(message))
	}
	return nil
}

func (m *mockTransport) Receive(ctx context.Context) (Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.receiveErr != nil {
		return Message{}, m.receiveErr
	}
	if len(m.responses) == 0 {
		return Message{}, errors.New("mock transport: no queued responses")
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

func (m *mockTransport) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func TestClientInitialize(t *testing.T) {
	transport := &mockTransport{
		handler: func(req Message) Message {
			if req.Method != "initialize" {
				return Message{
					JSONRPC: jsonRPCVersion,
					ID:      req.ID,
					Error: &RPCError{
						Code:    -32601,
						Message: "method not found",
					},
				}
			}
			params := decodeParams(t, req.Params)
			if params["protocolVersion"] != "2026-01-01" {
				t.Fatalf("protocolVersion = %v, want 2026-01-01", params["protocolVersion"])
			}
			clientInfo, _ := params["clientInfo"].(map[string]any)
			if clientInfo["name"] != "petalflow-test" {
				t.Fatalf("clientInfo.name = %v, want petalflow-test", clientInfo["name"])
			}

			result := InitializeResult{
				ProtocolVersion: "2026-01-01",
				Capabilities: map[string]any{
					"tools": map[string]any{},
				},
				ServerInfo: ServerInfo{
					Name:    "mock-mcp",
					Version: "1.0.0",
				},
			}
			return Message{
				JSONRPC: jsonRPCVersion,
				ID:      req.ID,
				Result:  mustJSON(t, result),
			}
		},
	}

	client := NewClient(transport, Options{
		ProtocolVersion: "2026-01-01",
		ClientInfo: ClientInfo{
			Name:    "petalflow-test",
			Version: "0.1.0",
		},
		Capabilities: map[string]any{
			"tools": map[string]any{},
		},
	})

	result, err := client.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if result.ServerInfo.Name != "mock-mcp" {
		t.Fatalf("ServerInfo.Name = %q, want mock-mcp", result.ServerInfo.Name)
	}

	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.notifications) == 0 {
		t.Fatal("expected initialized notification to be sent")
	}
	if transport.notifications[0].Method != "notifications/initialized" {
		t.Fatalf("notification method = %q, want notifications/initialized", transport.notifications[0].Method)
	}
}

func TestClientInitializeIsIdempotent(t *testing.T) {
	callCount := 0
	transport := &mockTransport{
		handler: func(req Message) Message {
			callCount++
			result := InitializeResult{
				ProtocolVersion: "2026-01-01",
				ServerInfo: ServerInfo{
					Name: "mock-mcp",
				},
			}
			return Message{
				JSONRPC: jsonRPCVersion,
				ID:      req.ID,
				Result:  mustJSON(t, result),
			}
		},
	}

	client := NewClient(transport, Options{
		ProtocolVersion: "2026-01-01",
		ClientInfo: ClientInfo{
			Name: "petalflow-test",
		},
	})

	first, err := client.Initialize(context.Background())
	if err != nil {
		t.Fatalf("first Initialize() error = %v", err)
	}
	second, err := client.Initialize(context.Background())
	if err != nil {
		t.Fatalf("second Initialize() error = %v", err)
	}
	if first.ServerInfo.Name != second.ServerInfo.Name {
		t.Fatalf("cached initialize result mismatch: first=%q second=%q", first.ServerInfo.Name, second.ServerInfo.Name)
	}
	if callCount != 1 {
		t.Fatalf("initialize call count = %d, want 1", callCount)
	}
}

func TestClientListTools(t *testing.T) {
	transport := &mockTransport{
		handler: func(req Message) Message {
			if req.Method != "tools/list" {
				return Message{
					JSONRPC: jsonRPCVersion,
					ID:      req.ID,
					Error: &RPCError{
						Code:    -32601,
						Message: "method not found",
					},
				}
			}
			result := ToolsListResult{
				Tools: []Tool{
					{
						Name:        "list_s3_objects",
						Description: "List objects in bucket",
						InputSchema: map[string]any{
							"type": "object",
						},
					},
				},
			}
			return Message{
				JSONRPC: jsonRPCVersion,
				ID:      req.ID,
				Result:  mustJSON(t, result),
			}
		},
	}

	client := NewClient(transport, Options{})
	result, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(result.Tools))
	}
	if result.Tools[0].Name != "list_s3_objects" {
		t.Fatalf("tool name = %q, want list_s3_objects", result.Tools[0].Name)
	}
}

func TestClientCallTool(t *testing.T) {
	transport := &mockTransport{
		handler: func(req Message) Message {
			if req.Method != "tools/call" {
				return Message{
					JSONRPC: jsonRPCVersion,
					ID:      req.ID,
					Error: &RPCError{
						Code:    -32601,
						Message: "method not found",
					},
				}
			}
			params := decodeParams(t, req.Params)
			if params["name"] != "list_s3_objects" {
				t.Fatalf("params.name = %v, want list_s3_objects", params["name"])
			}

			result := ToolsCallResult{
				Content: []ContentBlock{
					{
						Type: "text",
						Text: `{"keys":["a.pdf","b.pdf"],"count":2}`,
					},
				},
			}
			return Message{
				JSONRPC: jsonRPCVersion,
				ID:      req.ID,
				Result:  mustJSON(t, result),
			}
		},
	}

	client := NewClient(transport, Options{})
	result, err := client.CallTool(context.Background(), ToolsCallParams{
		Name: "list_s3_objects",
		Arguments: map[string]any{
			"bucket": "reports",
		},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(result.Content))
	}
	if result.Content[0].Type != "text" {
		t.Fatalf("content type = %q, want text", result.Content[0].Type)
	}
}

func TestClientRPCError(t *testing.T) {
	transport := &mockTransport{
		handler: func(req Message) Message {
			return Message{
				JSONRPC: jsonRPCVersion,
				ID:      req.ID,
				Error: &RPCError{
					Code:    -32001,
					Message: "server failure",
				},
			}
		},
	}

	client := NewClient(transport, Options{})
	_, err := client.ListTools(context.Background())
	if err == nil {
		t.Fatal("ListTools() error = nil, want non-nil")
	}
	var reqErr *RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("error type = %T, want *RequestError", err)
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error does not wrap *RPCError: %v", err)
	}
	if rpcErr.Code != -32001 {
		t.Fatalf("rpc error code = %d, want -32001", rpcErr.Code)
	}
}

func TestClientClose(t *testing.T) {
	transport := &mockTransport{}
	client := NewClient(transport, Options{})
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	transport.mu.Lock()
	defer transport.mu.Unlock()
	if !transport.closed {
		t.Fatal("transport.closed = false, want true")
	}
	if len(transport.notifications) == 0 {
		t.Fatal("expected close notification before transport close")
	}
	if transport.notifications[0].Method != "close" {
		t.Fatalf("close notification method = %q, want close", transport.notifications[0].Method)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return data
}

func decodeParams(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return obj
}
