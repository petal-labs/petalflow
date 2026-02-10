package tool

import (
	"context"
	"encoding/json"
	"errors"
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
		return InvokeResponse{}, newToolError(ToolErrorCodeInvalidRequest, "tool: stdio adapter is nil", false, nil)
	}
	command := strings.TrimSpace(a.reg.Manifest.Transport.Command)
	if command == "" {
		return InvokeResponse{}, newToolError(ToolErrorCodeInvalidRequest, "tool: stdio adapter command is empty", false, nil)
	}
	if strings.TrimSpace(req.Action) == "" {
		return InvokeResponse{}, newToolError(
			ToolErrorCodeActionNotFound,
			"tool: action is required",
			false,
			fmt.Errorf("%w: empty action", ErrActionNotFound),
		)
	}

	timeout := timeoutFromRegistration(a.reg)
	response, attempts, err := invokeWithRetry(ctx, a.reg.Manifest.Transport.Retry, retryObservationMeta{
		toolName:  req.ToolName,
		action:    req.Action,
		transport: a.reg.Manifest.Transport.Type,
	}, func(parent context.Context, attempt int) (InvokeResponse, error) {
		execCtx := parent
		cancel := func() {}
		if _, hasDeadline := parent.Deadline(); !hasDeadline && timeout > 0 {
			execCtx, cancel = context.WithTimeout(parent, timeout)
		}
		defer cancel()

		args := a.reg.Manifest.Transport.Args
		// #nosec G204 -- command/args are explicitly configured by tool registration.
		cmd := exec.CommandContext(execCtx, command, args...)
		if len(a.reg.Manifest.Transport.Env) > 0 {
			cmd.Env = append(os.Environ(), flattenEnv(a.reg.Manifest.Transport.Env)...)
		}

		stdin, err := cmd.StdinPipe()
		if err != nil {
			return InvokeResponse{}, newToolError(ToolErrorCodeInvalidRequest, "tool: stdio open stdin", false, err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return InvokeResponse{}, newToolError(ToolErrorCodeInvalidRequest, "tool: stdio open stdout", false, err)
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return InvokeResponse{}, newToolError(ToolErrorCodeInvalidRequest, "tool: stdio open stderr", false, err)
		}

		start := time.Now()
		if err := cmd.Start(); err != nil {
			return InvokeResponse{}, newToolError(ToolErrorCodeTransportFailure, "tool: stdio start command", true, err)
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
			return InvokeResponse{}, newToolError(ToolErrorCodeInvalidRequest, "tool: stdio encode request", false, err)
		}
		if err := stdin.Close(); err != nil {
			_ = cmd.Wait()
			return InvokeResponse{}, newToolError(ToolErrorCodeTransportFailure, "tool: stdio close stdin", true, err)
		}

		stdoutBytes, stdoutErr := io.ReadAll(stdout)
		stderrBytes, _ := io.ReadAll(stderr)
		waitErr := cmd.Wait()

		if stdoutErr != nil {
			return InvokeResponse{}, newToolError(ToolErrorCodeTransportFailure, "tool: stdio read stdout", true, stdoutErr)
		}

		if execCtx.Err() != nil {
			if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
				return InvokeResponse{}, newToolError(ToolErrorCodeTimeout, "tool: stdio invoke timed out", true, execCtx.Err())
			}
			return InvokeResponse{}, newToolError(ToolErrorCodeTransportFailure, "tool: stdio invoke canceled", false, execCtx.Err())
		}

		if waitErr != nil {
			message := strings.TrimSpace(string(stderrBytes))
			if message == "" {
				message = waitErr.Error()
			}
			return InvokeResponse{}, withToolErrorDetails(
				newToolError(ToolErrorCodeUpstreamFailure, "tool: stdio invoke failed: "+message, false, waitErr),
				map[string]any{
					"stderr": message,
				},
			)
		}

		decoded, err := decodeInvokeResponse(stdoutBytes, elapsedMS(start))
		if err != nil {
			return InvokeResponse{}, err
		}
		return decoded, nil
	})
	if err != nil {
		emitInvokeObservation(ToolInvokeObservation{
			ToolName:  req.ToolName,
			Action:    req.Action,
			Transport: a.reg.Manifest.Transport.Type,
			Attempts:  attempts,
			Success:   false,
			ErrorCode: toolErrorCode(err),
		})
		return InvokeResponse{}, withToolErrorDetails(
			newToolError(
				toolErrorCodeOrDefault(err, ToolErrorCodeInvocationFailed),
				"tool: stdio invoke failed",
				isRetryableError(err),
				err,
			),
			map[string]any{
				"attempts": attempts,
				"action":   req.Action,
			},
		)
	}
	if response.Metadata == nil {
		response.Metadata = map[string]any{}
	}
	response.Metadata["attempts"] = attempts
	response.Metadata["retry_count"] = attempts - 1
	emitInvokeObservation(ToolInvokeObservation{
		ToolName:   req.ToolName,
		Action:     req.Action,
		Transport:  a.reg.Manifest.Transport.Type,
		Attempts:   attempts,
		DurationMS: response.DurationMS,
		Success:    true,
	})
	return response, nil
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
