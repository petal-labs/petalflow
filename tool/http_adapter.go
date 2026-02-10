package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// HTTPAdapter is the runtime adapter for HTTP-backed tools.
type HTTPAdapter struct {
	reg    Registration
	client *http.Client
}

// NewHTTPAdapter creates an HTTP adapter from a registration.
func NewHTTPAdapter(reg Registration) *HTTPAdapter {
	timeout := timeoutFromRegistration(reg)
	return &HTTPAdapter{
		reg:    reg,
		client: sharedHTTPClientPool.client(timeout),
	}
}

// Invoke executes an action over HTTP.
func (a *HTTPAdapter) Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error) {
	if a == nil {
		return InvokeResponse{}, newToolError(ToolErrorCodeInvalidRequest, "tool: http adapter is nil", false, nil)
	}
	endpoint := strings.TrimSpace(a.reg.Manifest.Transport.Endpoint)
	if endpoint == "" {
		return InvokeResponse{}, newToolError(ToolErrorCodeInvalidRequest, "tool: http adapter endpoint is empty", false, nil)
	}
	if strings.TrimSpace(req.Action) == "" {
		return InvokeResponse{}, newToolError(
			ToolErrorCodeActionNotFound,
			"tool: action is required",
			false,
			fmt.Errorf("%w: empty action", ErrActionNotFound),
		)
	}

	payload := map[string]any{
		"tool_name":   req.ToolName,
		"action":      req.Action,
		"inputs":      req.Inputs,
		"config":      req.Config,
		"request_id":  req.RequestID,
		"transport":   string(a.reg.Manifest.Transport.Type),
		"tool_origin": string(a.reg.Origin),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return InvokeResponse{}, newToolError(
			ToolErrorCodeInvalidRequest,
			"tool: encode HTTP invoke request",
			false,
			err,
		)
	}

	totalStart := time.Now()
	response, attempts, err := invokeWithRetry(ctx, a.reg.Manifest.Transport.Retry, retryObservationMeta{
		toolName:  req.ToolName,
		action:    req.Action,
		transport: a.reg.Manifest.Transport.Type,
	}, func(attemptCtx context.Context, attempt int) (InvokeResponse, error) {
		start := time.Now()
		httpReq, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return InvokeResponse{}, newToolError(
				ToolErrorCodeInvalidRequest,
				"tool: build HTTP request",
				false,
				err,
			)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "application/json")

		resp, err := a.client.Do(httpReq)
		if err != nil {
			return InvokeResponse{}, classifyHTTPInvokeError(err)
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return InvokeResponse{}, newToolError(
				ToolErrorCodeTransportFailure,
				"tool: read HTTP response",
				true,
				err,
			)
		}

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return InvokeResponse{}, decodeHTTPStatusError(resp.StatusCode, respBody, a.reg.Manifest.Transport.Retry)
		}

		decoded, err := decodeInvokeResponse(respBody, elapsedMS(start))
		if err != nil {
			return InvokeResponse{}, err
		}
		return decoded, nil
	})
	if err != nil {
		emitInvokeObservation(ToolInvokeObservation{
			ToolName:   req.ToolName,
			Action:     req.Action,
			Transport:  a.reg.Manifest.Transport.Type,
			Attempts:   attempts,
			DurationMS: elapsedMS(totalStart),
			Success:    false,
			ErrorCode:  toolErrorCode(err),
		})
		return InvokeResponse{}, withToolErrorDetails(
			newToolError(
				toolErrorCodeOrDefault(err, ToolErrorCodeInvocationFailed),
				"tool: HTTP invoke failed",
				isRetryableError(err),
				err,
			),
			map[string]any{
				"action":      req.Action,
				"tool_name":   req.ToolName,
				"transport":   string(a.reg.Manifest.Transport.Type),
				"attempts":    attempts,
				"duration_ms": elapsedMS(totalStart),
			},
		)
	}
	if response.Metadata == nil {
		response.Metadata = map[string]any{}
	}
	response.Metadata["attempts"] = attempts
	response.Metadata["retry_count"] = attempts - 1
	response.Metadata["total_duration_ms"] = elapsedMS(totalStart)
	emitInvokeObservation(ToolInvokeObservation{
		ToolName:   req.ToolName,
		Action:     req.Action,
		Transport:  a.reg.Manifest.Transport.Type,
		Attempts:   attempts,
		DurationMS: elapsedMS(totalStart),
		Success:    true,
	})
	return response, nil
}

// Close releases any adapter resources.
func (a *HTTPAdapter) Close(ctx context.Context) error {
	return nil
}

func classifyHTTPInvokeError(err error) error {
	if err == nil {
		return nil
	}
	retryable := false
	code := ToolErrorCodeTransportFailure
	message := "tool: HTTP invoke failed"

	if errors.Is(err, context.DeadlineExceeded) {
		retryable = true
		code = ToolErrorCodeTimeout
		message = "tool: HTTP invoke timed out"
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		retryable = true
		code = ToolErrorCodeTimeout
		message = "tool: HTTP invoke timed out"
	}

	return newToolError(code, message, retryable, err)
}

func decodeHTTPStatusError(statusCode int, body []byte, policy RetryPolicy) error {
	if parsed, err := decodeStructuredHTTPError(body); err == nil && parsed != nil {
		parsed.Retryable = parsed.Retryable || isRetryableStatus(statusCode, policy)
		return withToolErrorDetails(parsed, map[string]any{
			"http_status": statusCode,
		})
	}

	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(statusCode)
	}

	return withToolErrorDetails(
		newToolError(
			ToolErrorCodeUpstreamFailure,
			fmt.Sprintf("tool: HTTP invoke returned status %d: %s", statusCode, message),
			isRetryableStatus(statusCode, policy),
			nil,
		),
		map[string]any{
			"http_status": statusCode,
		},
	)
}

func decodeStructuredHTTPError(body []byte) (*ToolError, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	raw, ok := payload["error"]
	if !ok {
		return nil, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, newToolError(
			ToolErrorCodeDecodeFailure,
			"tool: HTTP error payload must be an object",
			false,
			nil,
		)
	}
	toolErr, ok := decodeToolError(obj).(*ToolError)
	if !ok || toolErr == nil {
		return nil, nil
	}
	return toolErr, nil
}

func isRetryableStatus(statusCode int, policy RetryPolicy) bool {
	for _, code := range policy.RetryableCodes {
		if code == statusCode {
			return true
		}
	}
	return false
}
