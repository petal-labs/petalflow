package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
)

// StdioTransportConfig configures a stdio MCP transport.
type StdioTransportConfig struct {
	Command string
	Args    []string
	Env     map[string]string
}

// StdioTransport implements MCP transport over a subprocess stdin/stdout pipe.
type StdioTransport struct {
	mu     sync.Mutex
	cfg    StdioTransportConfig
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	recvCh chan Message
	errCh  chan error
	waitCh chan struct{}
	closed bool
}

// NewStdioTransport starts a stdio MCP subprocess transport.
func NewStdioTransport(ctx context.Context, cfg StdioTransportConfig) (*StdioTransport, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, errors.New("mcp: stdio command is required")
	}

	t := &StdioTransport{
		cfg:    cfg,
		recvCh: make(chan Message, 64),
		errCh:  make(chan error, 1),
		waitCh: make(chan struct{}),
	}
	if err := t.start(ctx); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *StdioTransport) start(ctx context.Context) error {
	args := slices.Clone(t.cfg.Args)
	// #nosec G204 -- command/args are configured by trusted registration input.
	cmd := exec.CommandContext(ctx, t.cfg.Command, args...)
	if len(t.cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), flattenEnv(t.cfg.Env)...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp: stdio open stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp: stdio open stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("mcp: stdio open stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mcp: stdio start: %w", err)
	}

	t.cmd = cmd
	t.stdin = stdin

	go t.readLoop(stdout)
	go t.waitLoop(stderr)

	return nil
}

func (t *StdioTransport) readLoop(stdout io.Reader) {
	decoder := json.NewDecoder(bufio.NewReader(stdout))
	for {
		var message Message
		if err := decoder.Decode(&message); err != nil {
			t.sendErr(fmt.Errorf("mcp: stdio decode response: %w", err))
			return
		}
		select {
		case t.recvCh <- message:
		default:
			t.sendErr(errors.New("mcp: stdio receive queue is full"))
			return
		}
	}
}

func (t *StdioTransport) waitLoop(stderr io.Reader) {
	defer close(t.waitCh)

	_, _ = io.Copy(io.Discard, stderr)

	t.mu.Lock()
	cmd := t.cmd
	t.mu.Unlock()

	if cmd == nil {
		return
	}
	err := cmd.Wait()

	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()

	if err != nil && !closed {
		t.sendErr(fmt.Errorf("mcp: stdio process exited: %w", err))
	}
}

// Send writes a JSON-RPC message to the subprocess stdin.
func (t *StdioTransport) Send(ctx context.Context, message Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("mcp: stdio transport is closed")
	}
	if t.stdin == nil {
		return errors.New("mcp: stdio stdin is not available")
	}

	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("mcp: encode request: %w", err)
	}
	data = append(data, '\n')

	if _, err := t.stdin.Write(data); err != nil {
		return fmt.Errorf("mcp: write request: %w", err)
	}
	return nil
}

// Receive reads the next JSON-RPC message from subprocess stdout.
func (t *StdioTransport) Receive(ctx context.Context) (Message, error) {
	select {
	case <-ctx.Done():
		return Message{}, ctx.Err()
	case err := <-t.errCh:
		return Message{}, err
	case message := <-t.recvCh:
		return message, nil
	}
}

// Close terminates the subprocess and closes resources.
func (t *StdioTransport) Close(ctx context.Context) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	stdin := t.stdin
	cmd := t.cmd
	waitCh := t.waitCh
	t.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if waitCh != nil {
		select {
		case <-waitCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (t *StdioTransport) sendErr(err error) {
	select {
	case t.errCh <- err:
	default:
	}
}

func flattenEnv(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	out := make([]string, 0, len(values))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}
