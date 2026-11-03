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
	// Provider is the provider ID (used for cost resolution).
	Provider string
	// Estimator produces preflight token/cost estimates for llm.call
	// events and computed cost accounting for llm.response events. When
	// nil, events carry actual usage only.
	Estimator *CostEstimator
	// Capture optionally overrides the content capture policy for
	// emitters wired outside a runtime run. When the client runs inside
	// a runtime run, the run-scoped policy from the node context takes
	// precedence. Nil means the default metadata-only policy: no
	// prompts, messages, completions, or tool calls in events.
	Capture *runtime.ContentCapture
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
		c.emitLLMCallEvent(ctx, req, startTime)
	}

	// Execute the actual LLM call
	resp, err := c.client.Complete(ctx, req)

	endTime := time.Now()
	latencyMs := endTime.Sub(startTime).Milliseconds()

	// Emit LLM response event after the request
	if c.emitter != nil {
		c.emitLLMResponseEvent(ctx, req, resp, err, latencyMs, endTime)
	}

	return resp, err
}

// resolveCapture returns the content capture policy for one call: the
// run-scoped policy from the node context when the client runs inside a
// runtime run, else the client's configured override, else the default
// metadata-only policy.
func (c *InstrumentedClient) resolveCapture(ctx context.Context) runtime.ContentCapture {
	if capture, ok := runtime.ContentCaptureFromContext(ctx); ok {
		return capture
	}
	if c.ctx.Capture != nil {
		return *c.ctx.Capture
	}
	return runtime.ContentCapture{}
}

// emitLLMCallEvent emits an event before the LLM call. Content fields
// (system prompt, instructions, messages, input text, JSON schema) are
// classified per the capture policy: shape descriptors by default,
// bounded redacted content when capture is opted in.
func (c *InstrumentedClient) emitLLMCallEvent(ctx context.Context, req core.LLMRequest, startTime time.Time) {
	capture := c.resolveCapture(ctx)
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
	event = event.WithContentPayload("system_prompt", req.System, capture)

	if req.Instructions != "" {
		event = event.WithContentPayload("instructions", req.Instructions, capture)
	}

	// Serialize messages
	if len(req.Messages) > 0 {
		if messagesJSON, err := json.Marshal(req.Messages); err == nil {
			event = event.WithContentPayload("messages", string(messagesJSON), capture)
		}
	}

	if req.InputText != "" {
		event = event.WithContentPayload("input_text", req.InputText, capture)
	}

	if req.Temperature != nil {
		event.Payload["temperature"] = *req.Temperature
	}

	if req.MaxTokens != nil {
		event.Payload["max_tokens"] = *req.MaxTokens
	}

	if c.ctx.Estimator != nil {
		for key, value := range c.ctx.Estimator.estimateEventFields(c.ctx.Provider, req) {
			event.Payload[key] = value
		}
	}

	if req.JSONSchema != nil {
		if schemaJSON, err := json.Marshal(req.JSONSchema); err == nil {
			event = event.WithContentPayload("json_schema", string(schemaJSON), capture)
		}
	}

	c.emitter(event)
}

// emitLLMResponseEvent emits an event after the LLM response. The
// completion, tool calls, and reasoning summary are content-classified;
// failures carry a typed error code and a scrubbed message.
func (c *InstrumentedClient) emitLLMResponseEvent(ctx context.Context, req core.LLMRequest, resp core.LLMResponse, err error, latencyMs int64, endTime time.Time) {
	capture := c.resolveCapture(ctx)
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
		event.Payload["error"] = runtime.RedactText(err.Error())
		event.Payload["error_code"] = runtime.ErrorCodeFor(err)
		event.Payload["status"] = "error"
	} else {
		event.Payload["status"] = "success"
		event.Payload["provider"] = resp.Provider
		event.Payload["response_model"] = resp.Model
		event = event.WithContentPayload("completion", resp.Text, capture)
		event.Payload["stop_reason"] = resp.Status

		// Token usage
		event.Payload["input_tokens"] = resp.Usage.InputTokens
		event.Payload["output_tokens"] = resp.Usage.OutputTokens
		event.Payload["total_tokens"] = resp.Usage.TotalTokens

		if resp.Usage.CacheReadTokens > 0 {
			event.Payload["cache_read_tokens"] = resp.Usage.CacheReadTokens
		}
		if resp.Usage.CacheCreationTokens > 0 {
			event.Payload["cache_creation_tokens"] = resp.Usage.CacheCreationTokens
		}

		if resp.Usage.CostUSD > 0 {
			event.Payload["cost_usd"] = resp.Usage.CostUSD
		}

		// Tool calls
		if len(resp.ToolCalls) > 0 {
			if toolCallsJSON, err := json.Marshal(resp.ToolCalls); err == nil {
				event = event.WithContentPayload("tool_calls", string(toolCallsJSON), capture)
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
			event = event.WithContentPayload("reasoning_summary", resp.Reasoning.Summary, capture)
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
	capture := c.resolveCapture(ctx)

	// Emit LLM call event before the request
	if c.emitter != nil {
		c.emitLLMCallEvent(ctx, req, startTime)
	}

	// Execute the actual streaming LLM call
	stream, err := c.streamingClient.CompleteStream(ctx, req)
	if err != nil {
		// Emit error response event
		if c.emitter != nil {
			endTime := time.Now()
			latencyMs := endTime.Sub(startTime).Milliseconds()
			c.emitLLMResponseEvent(ctx, req, core.LLMResponse{}, err, latencyMs, endTime)
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
					event = event.WithContentPayload("completion", chunk.Accumulated, capture)

					if ttftMs > 0 {
						event.Payload["ttft_ms"] = ttftMs
					}

					if chunk.Error != nil {
						event.Payload["error"] = runtime.RedactText(chunk.Error.Error())
						event.Payload["error_code"] = runtime.ErrorCodeFor(chunk.Error)
						event.Payload["status"] = "error"
					} else {
						event.Payload["status"] = "success"
						if finalUsage != nil {
							event.Payload["input_tokens"] = finalUsage.InputTokens
							event.Payload["output_tokens"] = finalUsage.OutputTokens
							event.Payload["total_tokens"] = finalUsage.TotalTokens
							if finalUsage.CacheReadTokens > 0 {
								event.Payload["cache_read_tokens"] = finalUsage.CacheReadTokens
							}
							if finalUsage.CacheCreationTokens > 0 {
								event.Payload["cache_creation_tokens"] = finalUsage.CacheCreationTokens
							}
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
