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

		// Check for streaming errors.
		select {
		case err, ok := <-stream.Err:
			if ok && err != nil {
				sendTerminal(petalflow.StreamChunk{Error: err, Done: true})
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

		sendTerminal(finalChunk)
	}()

	return out, nil
}

// Ensure interface compliance at compile time.
var _ petalflow.StreamingLLMClient = (*ProviderAdapter)(nil)
