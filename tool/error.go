package tool

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// ToolErrorCodeActionNotFound is returned when an action name is missing or unknown.
	ToolErrorCodeActionNotFound = "ACTION_NOT_FOUND"
	// ToolErrorCodeInvalidRequest is returned when adapter request construction fails.
	ToolErrorCodeInvalidRequest = "INVALID_REQUEST"
	// ToolErrorCodeTransportFailure is returned when transport I/O fails.
	ToolErrorCodeTransportFailure = "TRANSPORT_FAILURE"
	// ToolErrorCodeTimeout is returned when invocation times out.
	ToolErrorCodeTimeout = "TIMEOUT"
	// ToolErrorCodeUpstreamFailure is returned for non-success upstream responses.
	ToolErrorCodeUpstreamFailure = "UPSTREAM_FAILURE"
	// ToolErrorCodeDecodeFailure is returned when adapter response decoding fails.
	ToolErrorCodeDecodeFailure = "DECODE_FAILURE"
	// ToolErrorCodeInvocationFailed is a generic fallback for tool invocation failures.
	ToolErrorCodeInvocationFailed = "INVOCATION_FAILED"
	// ToolErrorCodeMCPFailure is returned when MCP request/response handling fails.
	ToolErrorCodeMCPFailure = "MCP_FAILURE"
)

// ToolError is a structured invocation error that can flow across adapters, APIs,
// and runtime events without losing retryability or machine-readable codes.
type ToolError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
	Cause     error          `json:"-"`
}

func (e *ToolError) Error() string {
	if e == nil {
		return ""
	}
	code := strings.TrimSpace(e.Code)
	msg := strings.TrimSpace(e.Message)
	switch {
	case code == "" && msg == "":
		return ToolErrorCodeInvocationFailed
	case code == "":
		return msg
	case msg == "":
		return code
	default:
		return fmt.Sprintf("%s: %s", code, msg)
	}
}

// Unwrap exposes the wrapped cause for errors.Is/errors.As.
func (e *ToolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func newToolError(code, message string, retryable bool, cause error) *ToolError {
	cleanCode := strings.TrimSpace(code)
	if cleanCode == "" {
		cleanCode = ToolErrorCodeInvocationFailed
	}
	cleanMsg := strings.TrimSpace(message)
	if cleanMsg == "" && cause != nil {
		cleanMsg = cause.Error()
	}
	return &ToolError{
		Code:      cleanCode,
		Message:   cleanMsg,
		Retryable: retryable,
		Cause:     cause,
	}
}

func withToolErrorDetails(err *ToolError, details map[string]any) *ToolError {
	if err == nil {
		return nil
	}
	if len(details) == 0 {
		return err
	}
	if err.Details == nil {
		err.Details = make(map[string]any, len(details))
	}
	for key, value := range details {
		err.Details[key] = value
	}
	return err
}

func toolErrorFrom(err error) (*ToolError, bool) {
	if err == nil {
		return nil, false
	}
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		return toolErr, true
	}
	return nil, false
}

func toolErrorCode(err error) string {
	if toolErr, ok := toolErrorFrom(err); ok && toolErr != nil {
		return toolErr.Code
	}
	return ""
}

func toolErrorCodeOrDefault(err error, fallback string) string {
	if code := toolErrorCode(err); strings.TrimSpace(code) != "" {
		return code
	}
	if strings.TrimSpace(fallback) == "" {
		return ToolErrorCodeInvocationFailed
	}
	return fallback
}
