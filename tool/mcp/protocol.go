package mcp

import (
	"encoding/json"
	"fmt"
)

const (
	jsonRPCVersion = "2.0"
)

// Message is a JSON-RPC 2.0 envelope.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("mcp: rpc error %d: %s", e.Code, e.Message)
}

// RequestError wraps transport/protocol failures in request flow.
type RequestError struct {
	Method string
	Err    error
}

func (e *RequestError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("mcp: request %q failed: %v", e.Method, e.Err)
}

func (e *RequestError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ClientInfo identifies PetalFlow when opening an MCP session.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ServerInfo describes the connected MCP server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// InitializeParams is sent in the MCP initialize request.
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
	ClientInfo      ClientInfo     `json:"clientInfo"`
}

// InitializeResult is returned by the MCP initialize request.
type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
	ServerInfo      ServerInfo     `json:"serverInfo"`
}

// Tool describes one discovered MCP tool from tools/list.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// ToolsListResult is returned by the MCP tools/list request.
type ToolsListResult struct {
	Tools []Tool `json:"tools"`
}

// ToolsCallParams is sent in the MCP tools/call request.
type ToolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ContentBlock is an MCP content item returned by tools/call.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// ToolsCallResult is returned by the MCP tools/call request.
type ToolsCallResult struct {
	Content           []ContentBlock `json:"content,omitempty"`
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
}
