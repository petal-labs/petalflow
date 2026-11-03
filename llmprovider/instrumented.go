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

// NewContextInstrumentedClient wraps an LLMClient so llm.call and
// llm.response observability events are emitted through the runtime node
// context: the emitter, run/node identity, and content capture policy are
// resolved per call from the call context and the request metadata nodes
// attach (run_id, node_id). Clients constructed this way are safe to share
// across runs and providers; outside a runtime run (no context emitter)
// no events are emitted. Streaming clients keep their streaming path.
// Use it in hydrate.ClientFactory implementations to get LLM telemetry
// from hydrated workflow runs.
func NewContextInstrumentedClient(client core.LLMClient) core.LLMClient {
	base := &InstrumentedClient{client: client}
	if streaming, ok := client.(core.StreamingLLMClient); ok {
		return &InstrumentedStreamingClient{
			InstrumentedClient: base,
			streamingClient:    streaming,
		}
	}
	return base
}

// emission carries the resolved emitter, event identity, and capture
// policy for one LLM call.
type emission struct {
	emitter  runtime.EventEmitter
	identity LLMEventContext
	capture  runtime.ContentCapture
}

// resolveEmission determines how one call emits events: a fixed emitter
// and identity for NewInstrumentedClient, or the runtime node context
// and request metadata for NewContextInstrumentedClient.
func (c *InstrumentedClient) resolveEmission(ctx context.Context, req core.LLMRequest) emission {
	if c.emitter != nil {
		return emission{emitter: c.emitter, identity: c.ctx, capture: c.resolveCapture(ctx)}
	}

	// Context-resolved emission: inside a runtime run the node context
	// carries the emitter and capture policy, and the request metadata
	// carries run/node identity.
	if !runtime.EmitterInContext(ctx) {
		return emission{}
	}
	identity := LLMEventContext{
		RunID:    requestMetaString(req, "run_id"),
		NodeID:   requestMetaString(req, "node_id"),
		NodeKind: core.NodeKindLLM,
		Provider: c.ctx.Provider,
	}
	capture, _ := runtime.ContentCaptureFromContext(ctx)
	return emission{emitter: runtime.EmitterFromContext(ctx), identity: identity, capture: capture}
}

// requestMetaString extracts a string from the request metadata nodes
// attach for attribution.
func requestMetaString(req core.LLMRequest, key string) string {
	if req.Meta == nil {
		return ""
	}
	s, _ := req.Meta[key].(string)
	return s
}

// Complete sends a completion request and emits observability events.
func (c *InstrumentedClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	startTime := time.Now()
	em := c.resolveEmission(ctx, req)

	// Emit LLM call event before the request
	if em.emitter != nil {
		c.emitLLMCallEvent(em, req, startTime)
	}

	// Execute the actual LLM call
	resp, err := c.client.Complete(ctx, req)

	endTime := time.Now()
	latencyMs := endTime.Sub(startTime).Milliseconds()

	// Emit LLM response event after the request
	if em.emitter != nil {
		c.emitLLMResponseEvent(em, req, resp, err, latencyMs, endTime)
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
func (c *InstrumentedClient) emitLLMCallEvent(em emission, req core.LLMRequest, startTime time.Time) {
	event := runtime.Event{
		Kind:     runtime.EventLLMCall,
		RunID:    em.identity.RunID,
		NodeID:   em.identity.NodeID,
		NodeKind: em.identity.NodeKind,
		Time:     startTime,
		Payload:  make(map[string]any),
	}

	// Add request details to payload
	event.Payload["model"] = req.Model
	event = event.WithContentPayload("system_prompt", req.System, em.capture)

	if req.Instructions != "" {
		event = event.WithContentPayload("instructions", req.Instructions, em.capture)
	}

	// Serialize messages
	if len(req.Messages) > 0 {
		if messagesJSON, err := json.Marshal(req.Messages); err == nil {
			event = event.WithContentPayload("messages", string(messagesJSON), em.capture)
		}
	}

	if req.InputText != "" {
		event = event.WithContentPayload("input_text", req.InputText, em.capture)
	}

	if req.Temperature != nil {
		event.Payload["temperature"] = *req.Temperature
	}

	if req.MaxTokens != nil {
		event.Payload["max_tokens"] = *req.MaxTokens
	}

	if em.identity.Estimator != nil {
		for key, value := range em.identity.Estimator.estimateEventFields(em.identity.Provider, req) {
			event.Payload[key] = value
		}
	}

	if req.JSONSchema != nil {
		if schemaJSON, err := json.Marshal(req.JSONSchema); err == nil {
			event = event.WithContentPayload("json_schema", string(schemaJSON), em.capture)
		}
	}

	em.emitter(event)
}

// emitLLMResponseEvent emits an event after the LLM response. The
// completion, tool calls, and reasoning summary are content-classified;
// failures carry a typed error code and a scrubbed message.
func (c *InstrumentedClient) emitLLMResponseEvent(em emission, req core.LLMRequest, resp core.LLMResponse, err error, latencyMs int64, endTime time.Time) {
	event := runtime.Event{
		Kind:     runtime.EventLLMResponse,
		RunID:    em.identity.RunID,
		NodeID:   em.identity.NodeID,
		NodeKind: em.identity.NodeKind,
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
		event = event.WithContentPayload("completion", resp.Text, em.capture)
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
				event = event.WithContentPayload("tool_calls", string(toolCallsJSON), em.capture)
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
			event = event.WithContentPayload("reasoning_summary", resp.Reasoning.Summary, em.capture)
		}
	}

	em.emitter(event)
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
	em := c.resolveEmission(ctx, req)

	// Emit LLM call event before the request
	if em.emitter != nil {
		c.emitLLMCallEvent(em, req, startTime)
	}

	// Execute the actual streaming LLM call
	stream, err := c.streamingClient.CompleteStream(ctx, req)
	if err != nil {
		// Emit error response event
		if em.emitter != nil {
			endTime := time.Now()
			latencyMs := endTime.Sub(startTime).Milliseconds()
			c.emitLLMResponseEvent(em, req, core.LLMResponse{}, err, latencyMs, endTime)
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
				if em.emitter != nil {
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
						RunID:    em.identity.RunID,
						NodeID:   em.identity.NodeID,
						NodeKind: em.identity.NodeKind,
						Time:     endTime,
						Payload:  make(map[string]any),
					}

					event.Payload["latency_ms"] = latencyMs
					event.Payload["model"] = req.Model
					event = event.WithContentPayload("completion", chunk.Accumulated, em.capture)

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

					em.emitter(event)
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
