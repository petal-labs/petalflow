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

	if outputsRaw, hasOutputs := obj["outputs"]; hasOutputs {
		outputs, ok := outputsRaw.(map[string]any)
		if !ok {
			return InvokeResponse{}, fmt.Errorf("tool: invoke response outputs must be an object")
		}
		resp.Outputs = outputs
	} else {
		resp.Outputs = obj
	}

	if metadataRaw, ok := obj["metadata"]; ok {
		metadata, ok := metadataRaw.(map[string]any)
		if !ok {
			return InvokeResponse{}, fmt.Errorf("tool: invoke response metadata must be an object")
		}
		resp.Metadata = metadata
	}

	if durationRaw, ok := obj["duration_ms"]; ok {
		duration, ok := asInteger(durationRaw)
		if !ok || duration < 0 {
			return InvokeResponse{}, fmt.Errorf("tool: invoke response duration_ms must be a non-negative integer")
		}
		resp.DurationMS = duration
	}

	return resp, nil
}
