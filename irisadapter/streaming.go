package irisadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/petal-labs/iris/core"
	"github.com/petal-labs/petalflow"
)

// CompleteStream sends a streaming completion request to the underlying provider.
// It calls provider.StreamChat() and converts Iris ChatChunks into PetalFlow StreamChunks
// on a channel. The channel is closed when streaming is complete. The final chunk
// has Done=true and includes Usage if available from the provider.
func (a *ProviderAdapter) CompleteStream(ctx context.Context, req petalflow.LLMRequest) (<-chan petalflow.StreamChunk, error) {
	if err := a.validateRequest(req); err != nil {
		return nil, err
	}

	// Convert LLMRequest to core.ChatRequest (reuse existing conversion)
	chatReq, err := a.toCoreChatRequest(req)
	if err != nil {
		return nil, err
	}

	// Call the provider's StreamChat
	stream, err := a.provider.StreamChat(ctx, chatReq)
	if err != nil {
		return nil, fmt.Errorf("provider stream chat failed: %w", err)
	}
	if stream == nil {
		return nil, fmt.Errorf("provider stream chat returned a nil stream")
	}

	out := make(chan petalflow.StreamChunk, 1)

	go func() {
		defer close(out)

		var accumulated strings.Builder
		index := 0

		// send delivers a delta to out, applying backpressure but never
		// blocking past cancellation, so a gone consumer cannot leak this
		// goroutine. It returns false if the context was canceled first.
		send := func(sc petalflow.StreamChunk) bool {
			select {
			case out <- sc:
				return true
			case <-ctx.Done():
				return false
			}
		}

		// sendTerminal delivers the final/error chunk. It blocks until the
		// consumer receives it, but if the context is already canceled it makes
		// one best-effort non-blocking attempt so the terminal chunk still lands
		// in the buffered channel (rather than being lost to a random select)
		// without risking a permanent block when the consumer is gone.
		sendTerminal := func(sc petalflow.StreamChunk) {
			select {
			case out <- sc:
			case <-ctx.Done():
				select {
				case out <- sc:
				default:
				}
			}
		}

		// Read text deltas from the stream's Ch channel. The receive selects on
		// ctx.Done() so a provider that stalls (never sends, never closes Ch)
		// cannot hang this goroutine.
		streaming := true
		for streaming {
			select {
			case <-ctx.Done():
				sendTerminal(petalflow.StreamChunk{Error: ctx.Err(), Done: true})
				return
			case chunk, ok := <-stream.Ch:
				if !ok {
					streaming = false
					break
				}
				accumulated.WriteString(chunk.Delta)
				if !send(petalflow.StreamChunk{
					Delta:       chunk.Delta,
					Index:       index,
					Accumulated: accumulated.String(),
				}) {
					sendTerminal(petalflow.StreamChunk{Error: ctx.Err(), Done: true})
					return
				}
				index++
			}
		}

		streamErr, finalResp := waitForStreamMetadata(ctx, stream)
		if streamErr != nil {
			sendTerminal(petalflow.StreamChunk{Error: streamErr, Done: true, Index: index, Accumulated: accumulated.String()})
			return
		}

		finalChunk, err := a.streamChunkFromResponse(finalResp, req)
		if err != nil {
			sendTerminal(petalflow.StreamChunk{Error: err, Done: true, Index: index, Accumulated: accumulated.String()})
			return
		}
		finalChunk.Done = true
		finalChunk.Index = index
		finalChunk.Accumulated = accumulated.String()

		sendTerminal(finalChunk)
	}()

	return out, nil
}

// waitForStreamMetadata drains both terminal channels. Iris providers may
// publish an error after the text channel closes, so checking Err with a
// non-blocking receive would lose real provider failures.
func waitForStreamMetadata(ctx context.Context, stream *core.ChatStream) (error, *core.ChatResponse) {
	errResolved := false
	finalResolved := false
	var streamErr error
	var finalResp *core.ChatResponse

	for !errResolved || !finalResolved {
		var errCh <-chan error
		if !errResolved {
			errCh = stream.Err
		}
		var finalCh <-chan *core.ChatResponse
		if !finalResolved {
			finalCh = stream.Final
		}
		select {
		case <-ctx.Done():
			return ctx.Err(), nil
		case err, ok := <-errCh:
			errResolved = true
			if ok && err != nil {
				streamErr = err
			}
		case resp, ok := <-finalCh:
			finalResolved = true
			if ok {
				finalResp = resp
			}
		}
	}
	return streamErr, finalResp
}

func (a *ProviderAdapter) streamChunkFromResponse(resp *core.ChatResponse, req petalflow.LLMRequest) (petalflow.StreamChunk, error) {
	provider := a.provider.ID()
	chunk := petalflow.StreamChunk{Provider: provider}
	if resp == nil {
		return chunk, nil
	}
	response, err := a.fromCoreChatResponse(resp, req)
	if err != nil {
		return petalflow.StreamChunk{}, err
	}
	chunk.Response = &response
	chunk.ResponseID = resp.ID
	chunk.Model = string(resp.Model)
	chunk.Status = resp.Status
	chunk.Citations = append([]string(nil), resp.Citations...)
	if resp.Reasoning != nil {
		chunk.Reasoning = &petalflow.LLMReasoningOutput{
			ID: resp.Reasoning.ID, Summary: append([]string(nil), resp.Reasoning.Summary...),
		}
	}
	if len(resp.ToolCalls) > 0 {
		chunk.ToolCalls = make([]petalflow.LLMToolCall, 0, len(resp.ToolCalls))
		for _, call := range resp.ToolCalls {
			args := make(map[string]any)
			if len(call.Arguments) > 0 && json.Unmarshal(call.Arguments, &args) != nil {
				return petalflow.StreamChunk{}, fmt.Errorf("tool call %q (%s) has invalid JSON arguments", call.ID, call.Name)
			}
			chunk.ToolCalls = append(chunk.ToolCalls, petalflow.LLMToolCall{ID: call.ID, Name: call.Name, Arguments: args})
		}
	}
	if resp.ID != "" {
		chunk.Meta = map[string]any{"response_id": resp.ID}
	}
	if len(resp.Citations) > 0 {
		if chunk.Meta == nil {
			chunk.Meta = make(map[string]any)
		}
		chunk.Meta["citations"] = append([]string(nil), resp.Citations...)
	}
	if resp.Usage != (core.TokenUsage{}) {
		chunk.Usage = &petalflow.LLMTokenUsage{
			InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens, TotalTokens: resp.Usage.TotalTokens,
		}
	}
	return chunk, nil
}

// Ensure interface compliance at compile time.
var _ petalflow.StreamingLLMClient = (*ProviderAdapter)(nil)
