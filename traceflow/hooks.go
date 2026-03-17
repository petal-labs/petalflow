package traceflow

import (
	"encoding/json"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

// HookContext provides context for hook callbacks.
type HookContext struct {
	RunID    string
	NodeID   string
	NodeKind core.NodeKind
}

// Hooks provides callback hooks for PetalTrace capture.
// These hooks can be attached to PetalFlow's runtime to capture
// rich observability data.
type Hooks struct {
	adapter *Adapter
}

// NewHooks creates a new Hooks instance for the given adapter.
func NewHooks(adapter *Adapter) *Hooks {
	return &Hooks{adapter: adapter}
}

// CaptureNodeStart captures node start metadata.
func (h *Hooks) CaptureNodeStart(ctx HookContext, config map[string]any) map[string]any {
	payload := make(map[string]any)

	payload["petalflow.node.id"] = ctx.NodeID
	payload["petalflow.node.kind"] = string(ctx.NodeKind)

	if h.adapter.CaptureMode >= CaptureFull && config != nil {
		if configJSON, err := json.Marshal(config); err == nil {
			payload["petalflow.node.config"] = string(configJSON)
		}
	}

	return payload
}

// CaptureNodeComplete captures node completion metadata.
func (h *Hooks) CaptureNodeComplete(ctx HookContext, outputs map[string]any) map[string]any {
	payload := make(map[string]any)

	if h.adapter.CaptureMode >= CaptureFull && outputs != nil {
		if outputsJSON, err := json.Marshal(outputs); err == nil {
			payload["petalflow.node.outputs"] = string(outputsJSON)
		}
	}

	return payload
}

// CaptureLLMRequest captures LLM request details.
func (h *Hooks) CaptureLLMRequest(ctx HookContext, req core.LLMRequest) map[string]any {
	payload := make(map[string]any)

	payload["model"] = req.Model

	if h.adapter.ShouldCaptureLLMContent() {
		payload["system_prompt"] = req.System

		if req.Instructions != "" {
			payload["instructions"] = req.Instructions
		}

		if len(req.Messages) > 0 {
			if messagesJSON, err := json.Marshal(req.Messages); err == nil {
				payload["messages"] = string(messagesJSON)
			}
		}

		if req.InputText != "" {
			payload["input_text"] = req.InputText
		}

		if req.JSONSchema != nil {
			if schemaJSON, err := json.Marshal(req.JSONSchema); err == nil {
				payload["json_schema"] = string(schemaJSON)
			}
		}
	}

	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}

	if req.MaxTokens != nil {
		payload["max_tokens"] = *req.MaxTokens
	}

	return payload
}

// CaptureLLMResponse captures LLM response details.
func (h *Hooks) CaptureLLMResponse(ctx HookContext, resp core.LLMResponse, latencyMs int64) map[string]any {
	payload := make(map[string]any)

	payload["provider"] = resp.Provider
	payload["response_model"] = resp.Model
	payload["status"] = resp.Status
	payload["latency_ms"] = latencyMs

	// Token usage (always captured)
	payload["input_tokens"] = resp.Usage.InputTokens
	payload["output_tokens"] = resp.Usage.OutputTokens
	payload["total_tokens"] = resp.Usage.TotalTokens

	if resp.Usage.CostUSD > 0 {
		payload["cost_usd"] = resp.Usage.CostUSD
	}

	if h.adapter.ShouldCaptureLLMContent() {
		payload["completion"] = resp.Text

		if len(resp.ToolCalls) > 0 {
			if toolCallsJSON, err := json.Marshal(resp.ToolCalls); err == nil {
				payload["tool_calls"] = string(toolCallsJSON)
			}
		}

		if resp.Reasoning != nil {
			payload["reasoning_id"] = resp.Reasoning.ID
			payload["reasoning_summary"] = resp.Reasoning.Summary
		}
	}

	if len(resp.Meta) > 0 {
		if responseID, ok := resp.Meta["response_id"]; ok {
			payload["request_id"] = responseID
		}
	}

	return payload
}

// CaptureToolCall captures tool invocation details.
func (h *Hooks) CaptureToolCall(ctx HookContext, toolName, actionName string, args map[string]any) map[string]any {
	payload := make(map[string]any)

	payload["tool"] = toolName
	payload["action"] = actionName

	if h.adapter.ShouldCaptureLLMContent() && args != nil {
		if argsJSON, err := json.Marshal(args); err == nil {
			payload["arguments"] = string(argsJSON)
		}
	}

	return payload
}

// CaptureToolResult captures tool result details.
func (h *Hooks) CaptureToolResult(ctx HookContext, toolName string, result any, isError bool) map[string]any {
	payload := make(map[string]any)

	payload["tool"] = toolName
	payload["is_error"] = isError

	if h.adapter.ShouldCaptureLLMContent() && result != nil {
		if resultJSON, err := json.Marshal(result); err == nil {
			payload["result"] = string(resultJSON)
		}
	}

	return payload
}

// CaptureEdgeTransfer captures data transfer between nodes.
func (h *Hooks) CaptureEdgeTransfer(sourceNode, sourcePort, targetNode, targetPort string, data any) map[string]any {
	payload := make(map[string]any)

	payload["source_node"] = sourceNode
	payload["source_port"] = sourcePort
	payload["target_node"] = targetNode
	payload["target_port"] = targetPort

	if h.adapter.ShouldCaptureEdgeData() && data != nil {
		// Calculate data size
		if jsonData, err := json.Marshal(data); err == nil {
			payload["data_size_bytes"] = int64(len(jsonData))

			// Capture preview (first 500 chars)
			preview := string(jsonData)
			if len(preview) > 500 {
				preview = preview[:500]
			}
			payload["data_preview"] = preview
		}
	}

	return payload
}

// CaptureWorkflowStart captures initial workflow state for replay.
func (h *Hooks) CaptureWorkflowStart(runID string, graphDef, inputs, config any) map[string]any {
	payload := make(map[string]any)

	if h.adapter.ShouldCaptureSnapshots() {
		if graphDef != nil {
			payload["graph_definition"] = graphDef
		}
		if inputs != nil {
			payload["inputs"] = inputs
		}
		if config != nil {
			payload["config"] = config
		}
	}

	return payload
}

// CaptureWorkflowComplete captures final workflow state.
func (h *Hooks) CaptureWorkflowComplete(runID, status string, totalTokens, errorCount int) map[string]any {
	payload := make(map[string]any)

	payload["status"] = status
	payload["total_tokens"] = totalTokens
	payload["error_count"] = errorCount

	return payload
}

// EmitEvent is a helper that creates and emits an event with the given payload.
func (h *Hooks) EmitEvent(emitter runtime.EventEmitter, kind runtime.EventKind, ctx HookContext, payload map[string]any) {
	event := runtime.NewEvent(kind, ctx.RunID)
	event = event.WithNode(ctx.NodeID, ctx.NodeKind)

	for k, v := range payload {
		event = event.WithPayload(k, v)
	}

	emitter(event)
}
