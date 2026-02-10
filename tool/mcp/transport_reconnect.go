package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// TransportDialer creates a new MCP transport connection.
type TransportDialer func(ctx context.Context) (Transport, error)

// ReconnectConfig configures transport reconnection behavior.
type ReconnectConfig struct {
	MaxAttempts int
	BaseBackoff time.Duration
}

// ReconnectingTransport wraps a transport and reconnects on I/O failures.
type ReconnectingTransport struct {
	mu      sync.Mutex
	dialer  TransportDialer
	config  ReconnectConfig
	current Transport
	closed  bool
}

// NewReconnectingTransport builds a reconnect-capable transport wrapper.
func NewReconnectingTransport(ctx context.Context, dialer TransportDialer, cfg ReconnectConfig) (*ReconnectingTransport, error) {
	if dialer == nil {
		return nil, errors.New("mcp: reconnect dialer is nil")
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 200 * time.Millisecond
	}

	initial, err := dialer(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp: initial dial failed: %w", err)
	}

	return &ReconnectingTransport{
		dialer:  dialer,
		config:  cfg,
		current: initial,
	}, nil
}

// Send forwards message sending and reconnects on failure.
func (t *ReconnectingTransport) Send(ctx context.Context, message Message) error {
	for attempt := 0; attempt < t.config.MaxAttempts; attempt++ {
		current, err := t.getCurrent()
		if err != nil {
			return err
		}

		if err := current.Send(ctx, message); err == nil {
			return nil
		}

		if reconnectErr := t.reconnect(ctx, attempt); reconnectErr != nil {
			return reconnectErr
		}
	}
	return errors.New("mcp: send failed after reconnect attempts")
}

// Receive waits for messages and reconnects on failure.
func (t *ReconnectingTransport) Receive(ctx context.Context) (Message, error) {
	for attempt := 0; attempt < t.config.MaxAttempts; attempt++ {
		current, err := t.getCurrent()
		if err != nil {
			return Message{}, err
		}

		msg, err := current.Receive(ctx)
		if err == nil {
			return msg, nil
		}

		if reconnectErr := t.reconnect(ctx, attempt); reconnectErr != nil {
			return Message{}, reconnectErr
		}
	}
	return Message{}, errors.New("mcp: receive failed after reconnect attempts")
}

// Close closes the current transport and disables reconnection.
func (t *ReconnectingTransport) Close(ctx context.Context) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	current := t.current
	t.current = nil
	t.mu.Unlock()

	if current != nil {
		return current.Close(ctx)
	}
	return nil
}

func (t *ReconnectingTransport) getCurrent() (Transport, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errors.New("mcp: reconnecting transport is closed")
	}
	if t.current == nil {
		return nil, errors.New("mcp: reconnecting transport has no active connection")
	}
	return t.current, nil
}

func (t *ReconnectingTransport) reconnect(ctx context.Context, attempt int) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errors.New("mcp: reconnecting transport is closed")
	}
	current := t.current
	t.current = nil
	t.mu.Unlock()

	if current != nil {
		_ = current.Close(ctx)
	}

	backoff := t.config.BaseBackoff * time.Duration(1<<attempt)
	timer := time.NewTimer(backoff)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
	}

	next, err := t.dialer(ctx)
	if err != nil {
		return fmt.Errorf("mcp: reconnect attempt %d failed: %w", attempt+1, err)
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = next.Close(ctx)
		return errors.New("mcp: reconnecting transport is closed")
	}
	t.current = next
	t.mu.Unlock()
	return nil
}
