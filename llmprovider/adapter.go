// Package llmprovider bridges iris LLM providers to petalflow's core.LLMClient interface.
// It replicates the adapter logic from irisadapter/ but lives within the main module,
// avoiding the circular dependency that prevents the main module from importing irisadapter.
package llmprovider

import (
	"context"
	"encoding/json"
	"fmt"

	iriscore "github.com/petal-labs/iris/core"

	"github.com/petal-labs/petalflow/core"
)

// irisAdapter wraps an iris Provider to implement core.LLMClient.
type irisAdapter struct {
	provider iriscore.Provider
}

// Complete sends a synchronous completion request via the iris provider.
func (a *irisAdapter) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	chatReq := a.toRequest(req)

	chatResp, err := a.provider.Chat(ctx, chatReq)
	if err != nil {
		return core.LLMResponse{}, fmt.Errorf("provider chat failed: %w", err)
	}

	return a.fromResponse(chatResp, req), nil
}

// toRequest converts a core.LLMRequest to an iris ChatRequest.
func (a *irisAdapter) toRequest(req core.LLMRequest) *iriscore.ChatRequest {
	messages := make([]iriscore.Message, 0, len(req.Messages)+2)

	if req.System != "" {
		messages = append(messages, iriscore.Message{
			Role:    iriscore.RoleSystem,
			Content: req.System,
		})
	}

	for _, m := range req.Messages {
		msg := iriscore.Message{
			Role:    toIrisRole(m.Role),
			Content: m.Content,
		}

		if len(m.ToolCalls) > 0 {
			msg.ToolCalls = make([]iriscore.ToolCall, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				args, _ := json.Marshal(tc.Arguments)
				msg.ToolCalls[i] = iriscore.ToolCall{
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: args,
				}
			}
		}

		if len(m.ToolResults) > 0 {
			msg.ToolResults = make([]iriscore.ToolResult, len(m.ToolResults))
			for i, tr := range m.ToolResults {
				msg.ToolResults[i] = iriscore.ToolResult{
					CallID:  tr.CallID,
					Content: tr.Content,
					IsError: tr.IsError,
				}
			}
		}

		messages = append(messages, msg)
	}

	if req.InputText != "" {
		messages = append(messages, iriscore.Message{
			Role:    iriscore.RoleUser,
			Content: req.InputText,
		})
	}

	chatReq := &iriscore.ChatRequest{
		Model:        iriscore.ModelID(req.Model),
		Messages:     messages,
		Instructions: req.Instructions,
	}

	if req.Temperature != nil {
		temp := float32(*req.Temperature)
		chatReq.Temperature = &temp
	}
	if req.MaxTokens != nil {
		chatReq.MaxTokens = req.MaxTokens
	}

	return chatReq
}

// fromResponse converts an iris ChatResponse to a core.LLMResponse.
func (a *irisAdapter) fromResponse(resp *iriscore.ChatResponse, req core.LLMRequest) core.LLMResponse {
	result := core.LLMResponse{
		Text:     resp.Output,
		Provider: a.provider.ID(),
		Model:    string(resp.Model),
		Status:   resp.Status,
		Usage: core.LLMTokenUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		},
		Meta: make(map[string]any),
	}

	if resp.ID != "" {
		result.Meta["response_id"] = resp.ID
	}

	if resp.Reasoning != nil {
		result.Reasoning = &core.LLMReasoningOutput{
			ID:      resp.Reasoning.ID,
			Summary: resp.Reasoning.Summary,
		}
	}

	if len(resp.ToolCalls) > 0 {
		result.ToolCalls = make([]core.LLMToolCall, len(resp.ToolCalls))
		for i, tc := range resp.ToolCalls {
			args := make(map[string]any)
			if len(tc.Arguments) > 0 {
				_ = json.Unmarshal(tc.Arguments, &args)
			}
			result.ToolCalls[i] = core.LLMToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: args,
			}
		}
	}

	if req.JSONSchema != nil && resp.Output != "" {
		var jsonOutput map[string]any
		if err := json.Unmarshal([]byte(resp.Output), &jsonOutput); err == nil {
			result.JSON = jsonOutput
		}
	}

	result.Messages = make([]core.LLMMessage, 0, len(req.Messages)+1)
	result.Messages = append(result.Messages, req.Messages...)
	result.Messages = append(result.Messages, core.LLMMessage{
		Role:      "assistant",
		Content:   resp.Output,
		ToolCalls: result.ToolCalls,
	})

	return result
}

// toIrisRole converts a string role to an iris Role constant.
func toIrisRole(role string) iriscore.Role {
	switch role {
	case "system":
		return iriscore.RoleSystem
	case "user":
		return iriscore.RoleUser
	case "assistant":
		return iriscore.RoleAssistant
	case "tool":
		return iriscore.RoleTool
	default:
		return iriscore.RoleUser
	}
}

// Compile-time interface check.
var _ core.LLMClient = (*irisAdapter)(nil)
