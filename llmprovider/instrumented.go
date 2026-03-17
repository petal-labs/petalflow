// Package llmprovider bridges iris LLM providers to petalflow's core.LLMClient interface.
package llmprovider

import (
	"context"
	"encoding/json"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

// LLMEventContext provides context for LLM event emission.
type LLMEventContext struct {
	RunID    string
	NodeID   string
	NodeKind core.NodeKind
}

// InstrumentedClient wraps an LLMClient to emit runtime events for observability.
type InstrumentedClient struct {
	client  core.LLMClient
	emitter runtime.EventEmitter
	ctx     LLMEventContext
}

// NewInstrumentedClient creates a new instrumented LLM client.
// The emitter is called for each LLM call and response event.
// If emitter is nil, no events are emitted and the client behaves normally.
func NewInstrumentedClient(client core.LLMClient, emitter runtime.EventEmitter, ctx LLMEventContext) *InstrumentedClient {
	return &InstrumentedClient{
		client:  client,
		emitter: emitter,
		ctx:     ctx,
	}
}

// Complete sends a completion request and emits observability events.
func (c *InstrumentedClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	startTime := time.Now()

	// Emit LLM call event before the request
	if c.emitter != nil {
		c.emitLLMCallEvent(req, startTime)
	}

	// Execute the actual LLM call
	resp, err := c.client.Complete(ctx, req)

	endTime := time.Now()
	latencyMs := endTime.Sub(startTime).Milliseconds()

	// Emit LLM response event after the request
	if c.emitter != nil {
		c.emitLLMResponseEvent(req, resp, err, latencyMs, endTime)
	}

	return resp, err
}

// emitLLMCallEvent emits an event before the LLM call.
func (c *InstrumentedClient) emitLLMCallEvent(req core.LLMRequest, startTime time.Time) {
	event := runtime.Event{
		Kind:     runtime.EventLLMCall,
		RunID:    c.ctx.RunID,
		NodeID:   c.ctx.NodeID,
		NodeKind: c.ctx.NodeKind,
		Time:     startTime,
		Payload:  make(map[string]any),
	}

	// Add request details to payload
	event.Payload["model"] = req.Model
	event.Payload["system_prompt"] = req.System

	if req.Instructions != "" {
		event.Payload["instructions"] = req.Instructions
	}

	// Serialize messages
	if len(req.Messages) > 0 {
		if messagesJSON, err := json.Marshal(req.Messages); err == nil {
			event.Payload["messages"] = string(messagesJSON)
		}
	}

	if req.InputText != "" {
		event.Payload["input_text"] = req.InputText
	}

	if req.Temperature != nil {
		event.Payload["temperature"] = *req.Temperature
	}

	if req.MaxTokens != nil {
		event.Payload["max_tokens"] = *req.MaxTokens
	}

	if req.JSONSchema != nil {
		if schemaJSON, err := json.Marshal(req.JSONSchema); err == nil {
			event.Payload["json_schema"] = string(schemaJSON)
		}
	}

	c.emitter(event)
}

// emitLLMResponseEvent emits an event after the LLM response.
func (c *InstrumentedClient) emitLLMResponseEvent(req core.LLMRequest, resp core.LLMResponse, err error, latencyMs int64, endTime time.Time) {
	event := runtime.Event{
		Kind:     runtime.EventLLMResponse,
		RunID:    c.ctx.RunID,
		NodeID:   c.ctx.NodeID,
		NodeKind: c.ctx.NodeKind,
		Time:     endTime,
		Payload:  make(map[string]any),
	}

	event.Payload["latency_ms"] = latencyMs
	event.Payload["model"] = req.Model

	if err != nil {
		event.Payload["error"] = err.Error()
		event.Payload["status"] = "error"
	} else {
		event.Payload["status"] = "success"
		event.Payload["provider"] = resp.Provider
		event.Payload["response_model"] = resp.Model
		event.Payload["completion"] = resp.Text
		event.Payload["stop_reason"] = resp.Status

		// Token usage
		event.Payload["input_tokens"] = resp.Usage.InputTokens
		event.Payload["output_tokens"] = resp.Usage.OutputTokens
		event.Payload["total_tokens"] = resp.Usage.TotalTokens

		if resp.Usage.CostUSD > 0 {
			event.Payload["cost_usd"] = resp.Usage.CostUSD
		}

		// Tool calls
		if len(resp.ToolCalls) > 0 {
			if toolCallsJSON, err := json.Marshal(resp.ToolCalls); err == nil {
				event.Payload["tool_calls"] = string(toolCallsJSON)
			}
		}

		// Response metadata
		if len(resp.Meta) > 0 {
			if responseID, ok := resp.Meta["response_id"]; ok {
				event.Payload["request_id"] = responseID
			}
		}

		// Reasoning output
		if resp.Reasoning != nil {
			event.Payload["reasoning_id"] = resp.Reasoning.ID
			event.Payload["reasoning_summary"] = resp.Reasoning.Summary
		}
	}

	c.emitter(event)
}

// InstrumentedStreamingClient wraps a StreamingLLMClient to emit runtime events.
type InstrumentedStreamingClient struct {
	*InstrumentedClient
	streamingClient core.StreamingLLMClient
}

// NewInstrumentedStreamingClient creates a new instrumented streaming LLM client.
func NewInstrumentedStreamingClient(client core.StreamingLLMClient, emitter runtime.EventEmitter, ctx LLMEventContext) *InstrumentedStreamingClient {
	return &InstrumentedStreamingClient{
		InstrumentedClient: NewInstrumentedClient(client, emitter, ctx),
		streamingClient:    client,
	}
}

// CompleteStream sends a streaming completion request and emits observability events.
func (c *InstrumentedStreamingClient) CompleteStream(ctx context.Context, req core.LLMRequest) (<-chan core.StreamChunk, error) {
	startTime := time.Now()

	// Emit LLM call event before the request
	if c.emitter != nil {
		c.emitLLMCallEvent(req, startTime)
	}

	// Execute the actual streaming LLM call
	stream, err := c.streamingClient.CompleteStream(ctx, req)
	if err != nil {
		// Emit error response event
		if c.emitter != nil {
			endTime := time.Now()
			latencyMs := endTime.Sub(startTime).Milliseconds()
			c.emitLLMResponseEvent(req, core.LLMResponse{}, err, latencyMs, endTime)
		}
		return nil, err
	}

	// Wrap the stream to capture the final response
	out := make(chan core.StreamChunk, 1)
	go func() {
		defer close(out)

		var finalUsage *core.LLMTokenUsage
		var ttftMs int64
		firstChunk := true

		for chunk := range stream {
			if firstChunk && chunk.Delta != "" {
				ttftMs = time.Since(startTime).Milliseconds()
				firstChunk = false
			}

			if chunk.Usage != nil {
				finalUsage = chunk.Usage
			}

			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}

			if chunk.Done {
				// Emit final response event
				if c.emitter != nil {
					endTime := time.Now()
					latencyMs := endTime.Sub(startTime).Milliseconds()

					resp := core.LLMResponse{
						Text:  chunk.Accumulated,
						Model: req.Model,
					}
					if finalUsage != nil {
						resp.Usage = *finalUsage
					}

					event := runtime.Event{
						Kind:     runtime.EventLLMResponse,
						RunID:    c.ctx.RunID,
						NodeID:   c.ctx.NodeID,
						NodeKind: c.ctx.NodeKind,
						Time:     endTime,
						Payload:  make(map[string]any),
					}

					event.Payload["latency_ms"] = latencyMs
					event.Payload["model"] = req.Model
					event.Payload["completion"] = chunk.Accumulated

					if ttftMs > 0 {
						event.Payload["ttft_ms"] = ttftMs
					}

					if chunk.Error != nil {
						event.Payload["error"] = chunk.Error.Error()
						event.Payload["status"] = "error"
					} else {
						event.Payload["status"] = "success"
						if finalUsage != nil {
							event.Payload["input_tokens"] = finalUsage.InputTokens
							event.Payload["output_tokens"] = finalUsage.OutputTokens
							event.Payload["total_tokens"] = finalUsage.TotalTokens
						}
					}

					c.emitter(event)
				}
				return
			}
		}
	}()

	return out, nil
}

// Compile-time interface checks.
var (
	_ core.LLMClient          = (*InstrumentedClient)(nil)
	_ core.StreamingLLMClient = (*InstrumentedStreamingClient)(nil)
)
