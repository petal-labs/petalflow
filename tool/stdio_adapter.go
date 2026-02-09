package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// StdioAdapter is the runtime adapter for subprocess-backed tools.
type StdioAdapter struct {
	reg Registration
}

// NewStdioAdapter creates a stdio adapter from a registration.
func NewStdioAdapter(reg Registration) *StdioAdapter {
	return &StdioAdapter{reg: reg}
}

// Invoke executes an action through a long-running subprocess transport.
func (a *StdioAdapter) Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error) {
	if a == nil {
		return InvokeResponse{}, fmt.Errorf("tool: stdio adapter is nil")
	}
	command := strings.TrimSpace(a.reg.Manifest.Transport.Command)
	if command == "" {
		return InvokeResponse{}, fmt.Errorf("tool: stdio adapter command is empty")
	}
	if strings.TrimSpace(req.Action) == "" {
		return InvokeResponse{}, fmt.Errorf("%w: empty action", ErrActionNotFound)
	}

	execCtx := ctx
	timeout := timeoutFromRegistration(a.reg)
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := a.reg.Manifest.Transport.Args
	// #nosec G204 -- command/args are explicitly configured by tool registration.
	cmd := exec.CommandContext(execCtx, command, args...)
	if len(a.reg.Manifest.Transport.Env) > 0 {
		cmd.Env = append(os.Environ(), flattenEnv(a.reg.Manifest.Transport.Env)...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return InvokeResponse{}, fmt.Errorf("tool: stdio open stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return InvokeResponse{}, fmt.Errorf("tool: stdio open stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return InvokeResponse{}, fmt.Errorf("tool: stdio open stderr: %w", err)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return InvokeResponse{}, fmt.Errorf("tool: stdio start command: %w", err)
	}

	payload := InvokeRequest{
		ToolName:  req.ToolName,
		Action:    req.Action,
		Inputs:    req.Inputs,
		Config:    req.Config,
		RequestID: req.RequestID,
	}
	if err := json.NewEncoder(stdin).Encode(payload); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return InvokeResponse{}, fmt.Errorf("tool: stdio encode request: %w", err)
	}
	if err := stdin.Close(); err != nil {
		_ = cmd.Wait()
		return InvokeResponse{}, fmt.Errorf("tool: stdio close stdin: %w", err)
	}

	stdoutBytes, stdoutErr := io.ReadAll(stdout)
	stderrBytes, _ := io.ReadAll(stderr)
	waitErr := cmd.Wait()

	if stdoutErr != nil {
		return InvokeResponse{}, fmt.Errorf("tool: stdio read stdout: %w", stdoutErr)
	}
	if waitErr != nil {
		message := strings.TrimSpace(string(stderrBytes))
		if message == "" {
			message = waitErr.Error()
		}
		return InvokeResponse{}, fmt.Errorf("tool: stdio invoke failed: %s", message)
	}

	return decodeInvokeResponse(stdoutBytes, elapsedMS(start))
}

// Close releases any adapter resources.
func (a *StdioAdapter) Close(ctx context.Context) error {
	return nil
}

func flattenEnv(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	out := make([]string, 0, len(values))
	for _, key := range keys {
		value := values[key]
		out = append(out, key+"="+value)
	}
	return out
}
