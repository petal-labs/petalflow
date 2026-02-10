package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const defaultAdapterTimeout = 30 * time.Second

func timeoutFromRegistration(reg Registration) time.Duration {
	if reg.Manifest.Transport.TimeoutMS <= 0 {
		return defaultAdapterTimeout
	}
	return time.Duration(reg.Manifest.Transport.TimeoutMS) * time.Millisecond
}

func decodeInvokeResponse(raw []byte, fallbackDuration int64) (InvokeResponse, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return InvokeResponse{DurationMS: fallbackDuration}, nil
	}

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return InvokeResponse{}, fmt.Errorf("tool: decode invoke response: %w", err)
	}

	resp := InvokeResponse{
		DurationMS: fallbackDuration,
	}

	if errorRaw, hasError := obj["error"]; hasError {
		errorObj, ok := errorRaw.(map[string]any)
		if !ok {
			return InvokeResponse{}, newToolError(
				ToolErrorCodeDecodeFailure,
				"tool: invoke response error must be an object",
				false,
				nil,
			)
		}
		return InvokeResponse{}, decodeToolError(errorObj)
	}

	if outputsRaw, hasOutputs := obj["outputs"]; hasOutputs {
		outputs, ok := outputsRaw.(map[string]any)
		if !ok {
			return InvokeResponse{}, newToolError(
				ToolErrorCodeDecodeFailure,
				"tool: invoke response outputs must be an object",
				false,
				nil,
			)
		}
		resp.Outputs = outputs
	} else {
		resp.Outputs = obj
	}

	if metadataRaw, ok := obj["metadata"]; ok {
		metadata, ok := metadataRaw.(map[string]any)
		if !ok {
			return InvokeResponse{}, newToolError(
				ToolErrorCodeDecodeFailure,
				"tool: invoke response metadata must be an object",
				false,
				nil,
			)
		}
		resp.Metadata = metadata
	}

	if durationRaw, ok := obj["duration_ms"]; ok {
		duration, ok := asInteger(durationRaw)
		if !ok || duration < 0 {
			return InvokeResponse{}, newToolError(
				ToolErrorCodeDecodeFailure,
				"tool: invoke response duration_ms must be a non-negative integer",
				false,
				nil,
			)
		}
		resp.DurationMS = duration
	}

	return resp, nil
}

func decodeToolError(obj map[string]any) error {
	code, _ := obj["code"].(string)
	message, _ := obj["message"].(string)
	retryable, _ := obj["retryable"].(bool)

	details := map[string]any{}
	if rawDetails, ok := obj["details"]; ok {
		if cast, ok := rawDetails.(map[string]any); ok {
			for key, value := range cast {
				details[key] = value
			}
		} else {
			details["details"] = rawDetails
		}
	}

	err := newToolError(code, message, retryable, nil)
	if len(details) > 0 {
		err.Details = details
	}
	return err
}
