// Package llmprovider bridges iris LLM providers to petalflow's core.LLMClient interface.
// It replicates the adapter logic from irisadapter/ but lives within the main module,
// avoiding the circular dependency that prevents the main module from importing irisadapter.
package llmprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
		for _, part := range m.Parts {
			if mapped, ok := toIrisContentPart(part); ok {
				msg.Parts = append(msg.Parts, mapped)
			}
		}
		for _, artifact := range m.ArtifactRefs {
			if (artifact.ID == "") == (artifact.URI == "") {
				continue
			}
			msg.Parts = append(msg.Parts, iriscore.InputFile{FileID: artifact.ID, FileURL: artifact.URI, Filename: artifact.Filename})
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
		Model:              iriscore.ModelID(req.Model),
		Messages:           messages,
		Instructions:       req.Instructions,
		ReasoningEffort:    iriscore.ReasoningEffort(req.ReasoningEffort),
		PreviousResponseID: req.PreviousResponseID,
		Truncation:         req.Truncation,
	}

	if req.Temperature != nil {
		temp := float32(*req.Temperature)
		chatReq.Temperature = &temp
	}
	if req.MaxTokens != nil {
		chatReq.MaxTokens = req.MaxTokens
	}

	if req.ResponseFormat == core.LLMResponseFormatJSON {
		chatReq.ResponseFormat = iriscore.ResponseFormatJSON
	}
	if req.StructuredOutput != nil || req.JSONSchema != nil {
		structured := req.StructuredOutput
		if structured == nil {
			structured = &core.LLMStructuredOutput{
				Name: req.JSONSchemaName, Description: req.JSONSchemaDescription,
				Schema: req.JSONSchema, Strict: req.JSONSchemaStrict,
			}
		}
		name := structured.Name
		if name == "" {
			name = "petalflow_output"
		}
		strict := false
		if structured.Strict != nil {
			strict = *structured.Strict
		}
		schema, _ := json.Marshal(structured.Schema)
		chatReq.ResponseFormat = iriscore.ResponseFormatJSONSchema
		chatReq.JSONSchema = &iriscore.JSONSchemaDefinition{
			Name: name, Description: structured.Description, Schema: schema, Strict: strict,
		}
	}
	for _, tool := range req.Tools {
		chatReq.Tools = append(chatReq.Tools, requestTool{definition: tool})
	}
	for _, tool := range req.BuiltInTools {
		chatReq.BuiltInTools = append(chatReq.BuiltInTools, iriscore.BuiltInTool{Type: tool.Type})
	}
	if req.ToolResources != nil {
		chatReq.ToolResources = &iriscore.ToolResources{FileSearch: &iriscore.FileSearchResources{
			VectorStoreIDs: append([]string(nil), req.ToolResources.FileSearchVectorStoreIDs...),
		}}
	}
	if req.SearchOptions != nil {
		chatReq.SearchOptions = &iriscore.SearchOptions{
			SearchDomainFilter: append([]string(nil), req.SearchOptions.DomainFilter...),
			Recency:            iriscore.SearchRecencyFilter(req.SearchOptions.Recency),
			Mode:               iriscore.SearchMode(req.SearchOptions.Mode),
		}
	}

	return chatReq
}

func toIrisContentPart(part core.LLMContentPart) (iriscore.ContentPart, bool) {
	switch part.Type {
	case "text", "input_text":
		return iriscore.InputText{Text: part.Text}, true
	case "image", "input_image":
		if (part.URL == "") == (part.FileID == "") {
			return nil, false
		}
		return iriscore.InputImage{ImageURL: part.URL, FileID: part.FileID, Detail: iriscore.ImageDetail(part.Detail)}, true
	case "file", "input_file":
		sources := 0
		for _, source := range []string{part.URL, part.FileID, part.Data} {
			if source != "" {
				sources++
			}
		}
		if sources != 1 {
			return nil, false
		}
		return iriscore.InputFile{FileURL: part.URL, FileID: part.FileID, FileData: part.Data, Filename: part.Filename}, true
	default:
		return nil, false
	}
}

type requestTool struct{ definition core.LLMToolDefinition }

func (t requestTool) Name() string        { return t.definition.Name }
func (t requestTool) Description() string { return t.definition.Description }
func (t requestTool) Schema() iriscore.ToolSchema {
	data, _ := json.Marshal(t.definition.Parameters)
	return iriscore.ToolSchema{JSONSchema: data}
}

// fromResponse converts an iris ChatResponse to a core.LLMResponse.
func (a *irisAdapter) fromResponse(resp *iriscore.ChatResponse, req core.LLMRequest) core.LLMResponse {
	result := core.LLMResponse{
		Text:       resp.Output,
		Provider:   a.provider.ID(),
		Model:      string(resp.Model),
		Status:     resp.Status,
		ResponseID: resp.ID,
		Citations:  append([]string(nil), resp.Citations...),
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
	if len(resp.Citations) > 0 {
		result.Meta["citations"] = append([]string(nil), resp.Citations...)
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
		var jsonOutput any
		if err := json.Unmarshal([]byte(resp.Output), &jsonOutput); err == nil {
			result.JSONValue = jsonOutput
			if object, ok := jsonOutput.(map[string]any); ok {
				result.JSON = object
			}
		}
	}
	if req.StructuredOutput != nil || req.ResponseFormat == core.LLMResponseFormatJSON || req.ResponseFormat == core.LLMResponseFormatJSONSchema {
		if result.JSONValue == nil && resp.Output != "" {
			var jsonOutput any
			if err := json.Unmarshal([]byte(resp.Output), &jsonOutput); err == nil {
				result.JSONValue = jsonOutput
				if object, ok := jsonOutput.(map[string]any); ok {
					result.JSON = object
				}
			}
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

// CompleteStream sends a streaming completion request via the iris provider.
// It calls provider.StreamChat() and converts Iris ChatChunks into core.StreamChunks
// on a channel. The channel is closed when streaming is complete. The final chunk
// has Done=true and includes Usage if available from the provider.
func (a *irisAdapter) CompleteStream(ctx context.Context, req core.LLMRequest) (<-chan core.StreamChunk, error) {
	chatReq := a.toRequest(req)

	stream, err := a.provider.StreamChat(ctx, chatReq)
	if err != nil {
		return nil, fmt.Errorf("provider stream chat failed: %w", err)
	}
	if stream == nil {
		return nil, fmt.Errorf("provider stream chat returned a nil stream")
	}

	out := make(chan core.StreamChunk, 1)

	go func() {
		defer close(out)

		var accumulated strings.Builder
		index := 0

		for chunk := range stream.Ch {
			accumulated.WriteString(chunk.Delta)
			sc := core.StreamChunk{
				Delta:       chunk.Delta,
				Index:       index,
				Accumulated: accumulated.String(),
			}
			select {
			case out <- sc:
			case <-ctx.Done():
				out <- core.StreamChunk{
					Error: ctx.Err(),
					Done:  true,
				}
				return
			}
			index++
		}

		if ctx.Err() != nil {
			out <- core.StreamChunk{
				Error: ctx.Err(),
				Done:  true,
			}
			return
		}

		streamErr, finalResp := waitForMetadata(ctx, stream)
		if streamErr != nil {
			out <- core.StreamChunk{Error: streamErr, Done: true, Index: index, Accumulated: accumulated.String()}
			return
		}

		finalChunk := streamChunkFromResponse(finalResp, a.provider.ID(), req)
		finalChunk.Done = true
		finalChunk.Index = index
		finalChunk.Accumulated = accumulated.String()

		out <- finalChunk
	}()

	return out, nil
}

func waitForMetadata(ctx context.Context, stream *iriscore.ChatStream) (error, *iriscore.ChatResponse) {
	errResolved := false
	finalResolved := false
	var streamErr error
	var response *iriscore.ChatResponse
	for !errResolved || !finalResolved {
		var errCh <-chan error
		if !errResolved {
			errCh = stream.Err
		}
		var finalCh <-chan *iriscore.ChatResponse
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
				response = resp
			}
		}
	}
	return streamErr, response
}

func streamChunkFromResponse(resp *iriscore.ChatResponse, provider string, req core.LLMRequest) core.StreamChunk {
	chunk := core.StreamChunk{Provider: provider}
	if resp == nil {
		return chunk
	}
	converted := (&irisAdapter{provider: &responseProvider{response: resp, id: provider}}).fromResponse(resp, req)
	chunk.Response = &converted
	chunk.ResponseID = resp.ID
	chunk.Model = string(resp.Model)
	chunk.Status = resp.Status
	chunk.Citations = append([]string(nil), resp.Citations...)
	chunk.Reasoning = converted.Reasoning
	chunk.ToolCalls = converted.ToolCalls
	if resp.ID != "" || len(resp.Citations) > 0 {
		chunk.Meta = make(map[string]any)
		if resp.ID != "" {
			chunk.Meta["response_id"] = resp.ID
		}
		if len(resp.Citations) > 0 {
			chunk.Meta["citations"] = append([]string(nil), resp.Citations...)
		}
	}
	if resp.Usage != (iriscore.TokenUsage{}) {
		chunk.Usage = &core.LLMTokenUsage{InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens, TotalTokens: resp.Usage.TotalTokens}
	}
	return chunk
}

type responseProvider struct {
	response *iriscore.ChatResponse
	id       string
}

func (p *responseProvider) ID() string                     { return p.id }
func (p *responseProvider) Models() []iriscore.ModelInfo   { return nil }
func (p *responseProvider) Supports(iriscore.Feature) bool { return true }
func (p *responseProvider) Chat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatResponse, error) {
	return p.response, nil
}
func (p *responseProvider) StreamChat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	return nil, nil
}

// Compile-time interface check.
var _ core.StreamingLLMClient = (*irisAdapter)(nil)
