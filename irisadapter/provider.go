// Package irisadapter provides integration adapters between PetalFlow and Iris components.
package irisadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/petal-labs/iris/core"
	"github.com/petal-labs/petalflow"
)

// ProviderAdapter adapts a core.Provider to the petalflow.LLMClient interface.
type ProviderAdapter struct {
	provider core.Provider
}

// NewProviderAdapter creates a new adapter for the given provider.
func NewProviderAdapter(provider core.Provider) *ProviderAdapter {
	return &ProviderAdapter{provider: provider}
}

// Complete sends a completion request to the underlying provider.
func (a *ProviderAdapter) Complete(ctx context.Context, req petalflow.LLMRequest) (petalflow.LLMResponse, error) {
	// Convert LLMRequest to core.ChatRequest
	chatReq := a.toCoreChatRequest(req)

	// Call the provider
	chatResp, err := a.provider.Chat(ctx, chatReq)
	if err != nil {
		return petalflow.LLMResponse{}, fmt.Errorf("provider chat failed: %w", err)
	}

	// Convert core.ChatResponse to LLMResponse
	return a.fromCoreChatResponse(chatResp, req), nil
}

// toCoreChatRequest converts a petalflow.LLMRequest to core.ChatRequest.
func (a *ProviderAdapter) toCoreChatRequest(req petalflow.LLMRequest) *core.ChatRequest {
	messages := make([]core.Message, 0, len(req.Messages)+2)

	// Add system message if provided
	if req.System != "" {
		messages = append(messages, core.Message{
			Role:    core.RoleSystem,
			Content: req.System,
		})
	}

	// Add conversation messages
	for _, m := range req.Messages {
		msg := core.Message{
			Role:    toRole(m.Role),
			Content: m.Content,
		}

		// Handle assistant messages with tool calls
		if len(m.ToolCalls) > 0 {
			msg.ToolCalls = make([]core.ToolCall, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				args, _ := json.Marshal(tc.Arguments)
				msg.ToolCalls[i] = core.ToolCall{
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: args,
				}
			}
		}

		// Handle tool result messages
		if len(m.ToolResults) > 0 {
			msg.ToolResults = make([]core.ToolResult, len(m.ToolResults))
			for i, tr := range m.ToolResults {
				msg.ToolResults[i] = core.ToolResult{
					CallID:  tr.CallID,
					Content: tr.Content,
					IsError: tr.IsError,
				}
			}
		}

		messages = append(messages, msg)
	}

	// Add InputText as user message if provided (simple prompt mode)
	if req.InputText != "" {
		messages = append(messages, core.Message{
			Role:    core.RoleUser,
			Content: req.InputText,
		})
	}

	chatReq := &core.ChatRequest{
		Model:        core.ModelID(req.Model),
		Messages:     messages,
		Instructions: req.Instructions,
	}

	// Set optional parameters
	if req.Temperature != nil {
		temp := float32(*req.Temperature)
		chatReq.Temperature = &temp
	}
	if req.MaxTokens != nil {
		chatReq.MaxTokens = req.MaxTokens
	}

	return chatReq
}

// fromCoreChatResponse converts a core.ChatResponse to petalflow.LLMResponse.
func (a *ProviderAdapter) fromCoreChatResponse(resp *core.ChatResponse, req petalflow.LLMRequest) petalflow.LLMResponse {
	result := petalflow.LLMResponse{
		Text:     resp.Output,
		Provider: a.provider.ID(),
		Model:    string(resp.Model),
		Status:   resp.Status,
		Usage: petalflow.LLMTokenUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		},
		Meta: make(map[string]any),
	}

	// Store response ID if available
	if resp.ID != "" {
		result.Meta["response_id"] = resp.ID
	}

	// Map reasoning output if available
	if resp.Reasoning != nil {
		result.Reasoning = &petalflow.LLMReasoningOutput{
			ID:      resp.Reasoning.ID,
			Summary: resp.Reasoning.Summary,
		}
	}

	// Convert tool calls
	if len(resp.ToolCalls) > 0 {
		result.ToolCalls = make([]petalflow.LLMToolCall, len(resp.ToolCalls))
		for i, tc := range resp.ToolCalls {
			args := make(map[string]any)
			if len(tc.Arguments) > 0 {
				_ = json.Unmarshal(tc.Arguments, &args) // Best effort parsing
			}
			result.ToolCalls[i] = petalflow.LLMToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: args,
			}
		}
	}

	// Try to parse JSON if structured output was requested
	if req.JSONSchema != nil && resp.Output != "" {
		var jsonOutput map[string]any
		if err := json.Unmarshal([]byte(resp.Output), &jsonOutput); err == nil {
			result.JSON = jsonOutput
		}
	}

	// Build messages including the assistant response
	result.Messages = make([]petalflow.LLMMessage, 0, len(req.Messages)+1)
	result.Messages = append(result.Messages, req.Messages...)

	// Include tool calls in the assistant message
	assistantMsg := petalflow.LLMMessage{
		Role:      "assistant",
		Content:   resp.Output,
		ToolCalls: result.ToolCalls,
	}
	result.Messages = append(result.Messages, assistantMsg)

	return result
}

// toRole converts a string role to core.Role.
func toRole(role string) core.Role {
	switch role {
	case "system":
		return core.RoleSystem
	case "user":
		return core.RoleUser
	case "assistant":
		return core.RoleAssistant
	case "tool":
		return core.RoleTool
	default:
		return core.RoleUser
	}
}

// ProviderID returns the underlying provider's ID.
func (a *ProviderAdapter) ProviderID() string {
	return a.provider.ID()
}

// Ensure interface compliance at compile time.
var _ petalflow.LLMClient = (*ProviderAdapter)(nil)
