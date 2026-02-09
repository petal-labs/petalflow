package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

const (
	defaultProtocolVersion = "2025-06-18"
	defaultClientName      = "petalflow"
	defaultClientVersion   = "dev"
)

// Transport is the message transport contract used by the MCP client core.
// Transport implementations for stdio and SSE are added in subsequent tasks.
type Transport interface {
	Send(ctx context.Context, message Message) error
	Receive(ctx context.Context) (Message, error)
	Close(ctx context.Context) error
}

// Options configures client identity and capabilities.
type Options struct {
	ProtocolVersion string
	ClientInfo      ClientInfo
	Capabilities    map[string]any
}

// Client is a JSON-RPC based MCP client.
type Client struct {
	transport Transport
	options   Options

	mu          sync.Mutex
	nextID      int64
	initialized bool
	initResult  InitializeResult
}

// NewClient returns a new MCP client for a given transport.
func NewClient(transport Transport, options Options) *Client {
	if options.ProtocolVersion == "" {
		options.ProtocolVersion = defaultProtocolVersion
	}
	if options.ClientInfo.Name == "" {
		options.ClientInfo.Name = defaultClientName
	}
	if options.ClientInfo.Version == "" {
		options.ClientInfo.Version = defaultClientVersion
	}

	return &Client{
		transport: transport,
		options:   options,
		nextID:    1,
	}
}

// Initialize performs MCP initialize negotiation and sends initialized notification.
func (c *Client) Initialize(ctx context.Context) (InitializeResult, error) {
	if c == nil {
		return InitializeResult{}, errors.New("mcp: client is nil")
	}

	c.mu.Lock()
	alreadyInitialized := c.initialized
	cachedResult := c.initResult
	c.mu.Unlock()
	if alreadyInitialized {
		return cachedResult, nil
	}

	params := InitializeParams{
		ProtocolVersion: c.options.ProtocolVersion,
		Capabilities:    cloneMap(c.options.Capabilities),
		ClientInfo:      c.options.ClientInfo,
	}

	var result InitializeResult
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return InitializeResult{}, err
	}

	if err := c.notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return InitializeResult{}, err
	}

	c.mu.Lock()
	c.initialized = true
	c.initResult = result
	c.mu.Unlock()

	return result, nil
}

// ListTools returns server tools from tools/list.
func (c *Client) ListTools(ctx context.Context) (ToolsListResult, error) {
	var result ToolsListResult
	if err := c.call(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return ToolsListResult{}, err
	}
	return result, nil
}

// CallTool executes an MCP tool by name with arguments.
func (c *Client) CallTool(ctx context.Context, params ToolsCallParams) (ToolsCallResult, error) {
	var result ToolsCallResult
	if err := c.call(ctx, "tools/call", params, &result); err != nil {
		return ToolsCallResult{}, err
	}
	return result, nil
}

// Close sends an MCP close notification and closes the transport.
func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.transport == nil {
		return nil
	}

	_ = c.notify(ctx, "close", map[string]any{})
	return c.transport.Close(ctx)
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	if c == nil || c.transport == nil {
		return &RequestError{Method: method, Err: errors.New("transport is nil")}
	}

	paramsRaw, err := marshalParams(params)
	if err != nil {
		return &RequestError{Method: method, Err: err}
	}

	id := c.nextRequestID()
	request := Message{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Method:  method,
		Params:  paramsRaw,
	}
	if err := c.transport.Send(ctx, request); err != nil {
		return &RequestError{Method: method, Err: err}
	}

	for {
		response, err := c.transport.Receive(ctx)
		if err != nil {
			return &RequestError{Method: method, Err: err}
		}
		if response.JSONRPC != "" && response.JSONRPC != jsonRPCVersion {
			return &RequestError{Method: method, Err: fmt.Errorf("unsupported jsonrpc version %q", response.JSONRPC)}
		}

		// Ignore non-response messages and responses for other request IDs.
		if response.ID == 0 || response.ID != id {
			continue
		}

		if response.Error != nil {
			return &RequestError{Method: method, Err: response.Error}
		}
		if out == nil || len(response.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(response.Result, out); err != nil {
			return &RequestError{Method: method, Err: fmt.Errorf("decode result: %w", err)}
		}
		return nil
	}
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	if c == nil || c.transport == nil {
		return nil
	}
	paramsRaw, err := marshalParams(params)
	if err != nil {
		return &RequestError{Method: method, Err: err}
	}
	return c.transport.Send(ctx, Message{
		JSONRPC: jsonRPCVersion,
		Method:  method,
		Params:  paramsRaw,
	})
}

func (c *Client) nextRequestID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	return id
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	data, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode params: %w", err)
	}
	return data, nil
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
