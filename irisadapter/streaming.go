package irisadapter

import (
	"context"
	"fmt"
	"strings"

	"github.com/petal-labs/petalflow"
)

// CompleteStream sends a streaming completion request to the underlying provider.
// It calls provider.StreamChat() and converts Iris ChatChunks into PetalFlow StreamChunks
// on a channel. The channel is closed when streaming is complete. The final chunk
// has Done=true and includes Usage if available from the provider.
func (a *ProviderAdapter) CompleteStream(ctx context.Context, req petalflow.LLMRequest) (<-chan petalflow.StreamChunk, error) {
	// Convert LLMRequest to core.ChatRequest (reuse existing conversion)
	chatReq := a.toCoreChatRequest(req)

	// Call the provider's StreamChat
	stream, err := a.provider.StreamChat(ctx, chatReq)
	if err != nil {
		return nil, fmt.Errorf("provider stream chat failed: %w", err)
	}

	out := make(chan petalflow.StreamChunk, 1)

	go func() {
		defer close(out)

		var accumulated strings.Builder
		index := 0

		// Read text deltas from the stream's Ch channel.
		for chunk := range stream.Ch {
			accumulated.WriteString(chunk.Delta)
			sc := petalflow.StreamChunk{
				Delta:       chunk.Delta,
				Index:       index,
				Accumulated: accumulated.String(),
			}
			select {
			case out <- sc:
			case <-ctx.Done():
				out <- petalflow.StreamChunk{
					Error: ctx.Err(),
					Done:  true,
				}
				return
			}
			index++
		}

		// Check if context was canceled while reading chunks.
		if ctx.Err() != nil {
			out <- petalflow.StreamChunk{
				Error: ctx.Err(),
				Done:  true,
			}
			return
		}

		// Check for streaming errors.
		select {
		case err, ok := <-stream.Err:
			if ok && err != nil {
				out <- petalflow.StreamChunk{
					Error: err,
					Done:  true,
				}
				return
			}
		default:
		}

		// Wait for the final response to get usage and tool call info.
		var finalChunk petalflow.StreamChunk
		finalChunk.Done = true
		finalChunk.Index = index
		finalChunk.Accumulated = accumulated.String()

		select {
		case resp, ok := <-stream.Final:
			if ok && resp != nil {
				finalChunk.Usage = &petalflow.LLMTokenUsage{
					InputTokens:  resp.Usage.PromptTokens,
					OutputTokens: resp.Usage.CompletionTokens,
					TotalTokens:  resp.Usage.TotalTokens,
				}
			}
		case <-ctx.Done():
			finalChunk.Error = ctx.Err()
		}

		out <- finalChunk
	}()

	return out, nil
}

// Ensure interface compliance at compile time.
var _ petalflow.StreamingLLMClient = (*ProviderAdapter)(nil)
