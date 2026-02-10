package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// SSETransportConfig configures an MCP endpoint transport.
// The current implementation uses request/response JSON-RPC over HTTP while
// preserving an interface that can evolve to full SSE stream handling.
type SSETransportConfig struct {
	Endpoint string
	Headers  map[string]string
	Client   *http.Client
}

// SSETransport implements MCP transport over an HTTP endpoint.
type SSETransport struct {
	mu     sync.Mutex
	cfg    SSETransportConfig
	recvCh chan Message
	closed bool
}

// NewSSETransport creates an endpoint-backed MCP transport.
func NewSSETransport(cfg SSETransportConfig) (*SSETransport, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, errors.New("mcp: sse endpoint is required")
	}
	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
	}
	return &SSETransport{
		cfg:    cfg,
		recvCh: make(chan Message, 64),
	}, nil
}

// Send posts one JSON-RPC message and enqueues any JSON-RPC response body.
func (t *SSETransport) Send(ctx context.Context, message Message) error {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return errors.New("mcp: sse transport is closed")
	}

	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("mcp: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mcp: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for key, value := range t.cfg.Headers {
		req.Header.Set(key, value)
	}

	resp, err := t.cfg.Client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp: send request: %w", err)
	}
	defer resp.Body.Close()

	responseBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("mcp: read response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("mcp: endpoint returned status %d", resp.StatusCode)
	}
	if len(strings.TrimSpace(string(responseBytes))) == 0 {
		return nil
	}

	var response Message
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return fmt.Errorf("mcp: decode response: %w", err)
	}
	select {
	case t.recvCh <- response:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Receive waits for the next queued JSON-RPC response.
func (t *SSETransport) Receive(ctx context.Context) (Message, error) {
	select {
	case <-ctx.Done():
		return Message{}, ctx.Err()
	case message := <-t.recvCh:
		return message, nil
	}
}

// Close marks the transport closed.
func (t *SSETransport) Close(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	return nil
}
