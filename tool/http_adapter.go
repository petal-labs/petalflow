package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	return &HTTPAdapter{
		reg:    reg,
		client: &http.Client{Timeout: timeoutFromRegistration(reg)},
	}
}

// Invoke executes an action over HTTP.
func (a *HTTPAdapter) Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error) {
	if a == nil {
		return InvokeResponse{}, fmt.Errorf("tool: http adapter is nil")
	}
	endpoint := strings.TrimSpace(a.reg.Manifest.Transport.Endpoint)
	if endpoint == "" {
		return InvokeResponse{}, fmt.Errorf("tool: http adapter endpoint is empty")
	}
	if strings.TrimSpace(req.Action) == "" {
		return InvokeResponse{}, fmt.Errorf("%w: empty action", ErrActionNotFound)
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
		return InvokeResponse{}, fmt.Errorf("tool: encode HTTP invoke request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return InvokeResponse{}, fmt.Errorf("tool: build HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := a.client.Do(httpReq)
	if err != nil {
		return InvokeResponse{}, fmt.Errorf("tool: HTTP invoke failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return InvokeResponse{}, fmt.Errorf("tool: read HTTP response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(respBody))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return InvokeResponse{}, fmt.Errorf("tool: HTTP invoke returned status %d: %s", resp.StatusCode, message)
	}

	return decodeInvokeResponse(respBody, elapsedMS(start))
}

// Close releases any adapter resources.
func (a *HTTPAdapter) Close(ctx context.Context) error {
	return nil
}
