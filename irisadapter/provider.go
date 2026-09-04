// Package irisadapter provides integration adapters between PetalFlow and Iris components.
package irisadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/petal-labs/iris/core"
	"github.com/petal-labs/petalflow"
)

// ProviderAdapter adapts a core.Provider to the petalflow.LLMClient interface.
type ProviderAdapter struct {
	provider core.Provider
}

// ErrUnsupportedCapability indicates that a request asks Iris for a feature
// the selected provider/model cannot advertise.
var ErrUnsupportedCapability = errors.New("unsupported Iris capability")

// CapabilityError identifies an unsupported request feature without exposing
// provider credentials or request content.
type CapabilityError struct {
	Capability core.Feature
	Provider   string
	Model      string
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("%s: %s is not supported by provider %s for model %s", ErrUnsupportedCapability, e.Capability, e.Provider, e.Model)
}

func (e *CapabilityError) Unwrap() error { return ErrUnsupportedCapability }

// NewProviderAdapter creates a new adapter for the given provider.
func NewProviderAdapter(provider core.Provider) *ProviderAdapter {
	return &ProviderAdapter{provider: provider}
}

// Complete sends a completion request to the underlying provider.
func (a *ProviderAdapter) Complete(ctx context.Context, req petalflow.LLMRequest) (petalflow.LLMResponse, error) {
	if err := a.validateRequest(req); err != nil {
		return petalflow.LLMResponse{}, err
	}

	// Convert LLMRequest to core.ChatRequest
	chatReq, err := a.toCoreChatRequest(req)
	if err != nil {
		return petalflow.LLMResponse{}, err
	}

	// Call the provider
	chatResp, err := a.provider.Chat(ctx, chatReq)
	if err != nil {
		return petalflow.LLMResponse{}, fmt.Errorf("provider chat failed: %w", err)
	}
	if chatResp == nil {
		return petalflow.LLMResponse{}, errors.New("provider chat returned a nil response")
	}

	// Convert core.ChatResponse to LLMResponse
	return a.fromCoreChatResponse(chatResp, req)
}

// toCoreChatRequest converts a petalflow.LLMRequest to core.ChatRequest.
func (a *ProviderAdapter) toCoreChatRequest(req petalflow.LLMRequest) (*core.ChatRequest, error) {
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
		role, err := toRole(m.Role)
		if err != nil {
			return nil, err
		}
		msg := core.Message{
			Role:    role,
			Content: m.Content,
		}
		if len(m.Parts) > 0 || len(m.ArtifactRefs) > 0 {
			parts, err := toContentParts(m)
			if err != nil {
				return nil, err
			}
			msg.Parts = parts
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
		Model:              core.ModelID(req.Model),
		Messages:           messages,
		Instructions:       req.Instructions,
		ReasoningEffort:    core.ReasoningEffort(req.ReasoningEffort),
		PreviousResponseID: req.PreviousResponseID,
		Truncation:         req.Truncation,
	}

	// Set optional parameters
	if req.Temperature != nil {
		temp := float32(*req.Temperature)
		chatReq.Temperature = &temp
	}
	if req.MaxTokens != nil {
		chatReq.MaxTokens = req.MaxTokens
	}

	switch req.ResponseFormat {
	case petalflow.LLMResponseFormatText:
		chatReq.ResponseFormat = core.ResponseFormatText
	case petalflow.LLMResponseFormatJSON:
		chatReq.ResponseFormat = core.ResponseFormatJSON
	}
	if req.StructuredOutput != nil || req.JSONSchema != nil {
		structured := req.StructuredOutput
		if structured == nil {
			structured = &petalflow.LLMStructuredOutput{
				Name:        req.JSONSchemaName,
				Description: req.JSONSchemaDescription,
				Schema:      req.JSONSchema,
				Strict:      req.JSONSchemaStrict,
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
		schema, err := json.Marshal(structured.Schema)
		if err != nil {
			return nil, fmt.Errorf("invalid structured output schema: %w", err)
		}
		chatReq.ResponseFormat = core.ResponseFormatJSONSchema
		chatReq.JSONSchema = &core.JSONSchemaDefinition{
			Name:        name,
			Description: structured.Description,
			Schema:      schema,
			Strict:      strict,
		}
	}

	if len(req.Tools) > 0 {
		chatReq.Tools = make([]core.Tool, len(req.Tools))
		for i, tool := range req.Tools {
			chatReq.Tools[i] = requestTool{definition: tool}
		}
	}
	for _, tool := range req.BuiltInTools {
		chatReq.BuiltInTools = append(chatReq.BuiltInTools, core.BuiltInTool{Type: tool.Type})
	}
	if req.ToolResources != nil {
		chatReq.ToolResources = &core.ToolResources{FileSearch: &core.FileSearchResources{
			VectorStoreIDs: append([]string(nil), req.ToolResources.FileSearchVectorStoreIDs...),
		}}
	}
	if req.SearchOptions != nil {
		chatReq.SearchOptions = &core.SearchOptions{
			SearchDomainFilter: append([]string(nil), req.SearchOptions.DomainFilter...),
			Recency:            core.SearchRecencyFilter(req.SearchOptions.Recency),
			Mode:               core.SearchMode(req.SearchOptions.Mode),
		}
	}

	return chatReq, nil
}

// fromCoreChatResponse converts a core.ChatResponse to petalflow.LLMResponse.
func (a *ProviderAdapter) fromCoreChatResponse(resp *core.ChatResponse, req petalflow.LLMRequest) (petalflow.LLMResponse, error) {
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
		Meta:       make(map[string]any),
		ResponseID: resp.ID,
		Citations:  append([]string(nil), resp.Citations...),
	}

	// Store response ID if available
	if resp.ID != "" {
		result.Meta["response_id"] = resp.ID
	}
	if len(resp.Citations) > 0 {
		result.Meta["citations"] = append([]string(nil), resp.Citations...)
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
				if err := json.Unmarshal(tc.Arguments, &args); err != nil {
					return petalflow.LLMResponse{}, fmt.Errorf("tool call %q (%s) has invalid JSON arguments: %w", tc.ID, tc.Name, err)
				}
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
		var jsonOutput any
		if err := json.Unmarshal([]byte(resp.Output), &jsonOutput); err == nil {
			result.JSONValue = jsonOutput
			if object, ok := jsonOutput.(map[string]any); ok {
				result.JSON = object
			}
		}
	}
	if req.StructuredOutput != nil || req.ResponseFormat == petalflow.LLMResponseFormatJSON || req.ResponseFormat == petalflow.LLMResponseFormatJSONSchema {
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

	return result, nil
}

func (a *ProviderAdapter) validateRequest(req petalflow.LLMRequest) error {
	if a.provider == nil {
		return errors.New("iris adapter: provider is required")
	}
	if req.Model == "" {
		return errors.New("iris adapter: model is required")
	}
	switch req.ResponseFormat {
	case "", petalflow.LLMResponseFormatText, petalflow.LLMResponseFormatJSON, petalflow.LLMResponseFormatJSONSchema:
	default:
		return fmt.Errorf("iris adapter: unsupported response format %q", req.ResponseFormat)
	}
	if req.ResponseFormat == petalflow.LLMResponseFormatJSONSchema && req.JSONSchema == nil && req.StructuredOutput == nil {
		return errors.New("iris adapter: json_schema response format requires a structured output schema")
	}
	if req.ResponseFormat == petalflow.LLMResponseFormatText && (req.JSONSchema != nil || req.StructuredOutput != nil) {
		return errors.New("iris adapter: text response format conflicts with a structured output schema")
	}
	if req.ResponseFormat == petalflow.LLMResponseFormatJSON && (req.JSONSchema != nil || req.StructuredOutput != nil) {
		return errors.New("iris adapter: json_object response format conflicts with a JSON Schema")
	}
	if req.JSONSchema != nil && req.StructuredOutput != nil {
		return errors.New("iris adapter: JSONSchema and StructuredOutput are mutually exclusive")
	}
	if !a.supports(req.Model, core.FeatureChat) {
		return a.capabilityError(req.Model, core.FeatureChat)
	}
	if req.JSONSchema != nil || req.StructuredOutput != nil || req.ResponseFormat == petalflow.LLMResponseFormatJSON || req.ResponseFormat == petalflow.LLMResponseFormatJSONSchema {
		if err := a.requireCapability(req.Model, core.FeatureStructuredOutput); err != nil {
			return err
		}
	}
	if req.ReasoningEffort != "" {
		if err := a.requireCapability(req.Model, core.FeatureReasoning); err != nil {
			return err
		}
	}
	if req.PreviousResponseID != "" {
		if err := a.requireCapability(req.Model, core.FeatureResponseChain); err != nil {
			return err
		}
	}
	if len(req.Tools) > 0 || hasToolMessages(req.Messages) {
		if err := a.requireCapability(req.Model, core.FeatureToolCalling); err != nil {
			return err
		}
	}
	for _, tool := range req.Tools {
		if tool.Name == "" {
			return errors.New("iris adapter: tool name is required")
		}
		if _, err := json.Marshal(tool.Parameters); err != nil {
			return fmt.Errorf("iris adapter: tool %q has invalid parameters schema: %w", tool.Name, err)
		}
		if tool.Strict != nil {
			return fmt.Errorf("%w: strict tool schemas are not representable by Iris core.Tool for provider %s model %s", ErrUnsupportedCapability, a.provider.ID(), req.Model)
		}
	}
	if len(req.BuiltInTools) > 0 || req.ToolResources != nil {
		if err := a.requireCapability(req.Model, core.FeatureBuiltInTools); err != nil {
			return err
		}
	}
	if req.SearchOptions != nil {
		if err := a.requireCapability(req.Model, core.FeatureWebSearch); err != nil {
			return err
		}
	}
	for _, message := range req.Messages {
		if message.Name != "" {
			return fmt.Errorf("%w: message name is not representable by Iris core.Message for provider %s model %s", ErrUnsupportedCapability, a.provider.ID(), req.Model)
		}
		if len(message.Parts) == 0 && len(message.ArtifactRefs) == 0 {
			continue
		}
		supporter, ok := core.AsContentPartSupporter(a.provider)
		if !ok {
			return fmt.Errorf("%w: multimodal content is not supported by provider %s for model %s", ErrUnsupportedCapability, a.provider.ID(), req.Model)
		}
		parts, err := toContentParts(message)
		if err != nil {
			return err
		}
		role, err := toRole(message.Role)
		if err != nil {
			return err
		}
		for _, part := range parts {
			if !supporter.SupportsContentPart(core.ModelID(req.Model), role, part) {
				return fmt.Errorf("%w: content part %s is not supported by provider %s for model %s", ErrUnsupportedCapability, part.ContentType(), a.provider.ID(), req.Model)
			}
		}
	}
	return nil
}

func (a *ProviderAdapter) requireCapability(model string, feature core.Feature) error {
	if !a.supports(model, feature) {
		return a.capabilityError(model, feature)
	}
	return nil
}

func (a *ProviderAdapter) supports(model string, feature core.Feature) bool {
	if !a.provider.Supports(feature) {
		return false
	}
	for _, info := range a.provider.Models() {
		if info.ID == core.ModelID(model) && len(info.Capabilities) > 0 {
			return info.HasCapability(feature)
		}
	}
	return true
}

func (a *ProviderAdapter) capabilityError(model string, feature core.Feature) error {
	return &CapabilityError{Capability: feature, Provider: a.provider.ID(), Model: model}
}

func hasToolMessages(messages []petalflow.LLMMessage) bool {
	for _, message := range messages {
		if len(message.ToolCalls) > 0 || len(message.ToolResults) > 0 {
			return true
		}
	}
	return false
}

func toContentParts(message petalflow.LLMMessage) ([]core.ContentPart, error) {
	parts := make([]core.ContentPart, 0, len(message.Parts)+len(message.ArtifactRefs))
	for _, part := range message.Parts {
		mapped, err := toContentPart(part)
		if err != nil {
			return nil, err
		}
		parts = append(parts, mapped)
	}
	for _, artifact := range message.ArtifactRefs {
		if (artifact.ID == "") == (artifact.URI == "") {
			return nil, fmt.Errorf("invalid artifact reference: exactly one of id or uri is required")
		}
		file := core.InputFile{FileID: artifact.ID, FileURL: artifact.URI, Filename: artifact.Filename}
		parts = append(parts, file)
	}
	return parts, nil
}

func toContentPart(part petalflow.LLMContentPart) (core.ContentPart, error) {
	switch part.Type {
	case "text", "input_text":
		return core.InputText{Text: part.Text}, nil
	case "image", "input_image":
		if (part.URL == "") == (part.FileID == "") {
			return nil, fmt.Errorf("invalid image content part: exactly one of url or file_id is required")
		}
		return core.InputImage{ImageURL: part.URL, FileID: part.FileID, Detail: core.ImageDetail(part.Detail)}, nil
	case "file", "input_file":
		sources := 0
		for _, source := range []string{part.URL, part.FileID, part.Data} {
			if source != "" {
				sources++
			}
		}
		if sources != 1 {
			return nil, fmt.Errorf("invalid file content part: exactly one of url, file_id, or data is required")
		}
		return core.InputFile{FileURL: part.URL, FileID: part.FileID, FileData: part.Data, Filename: part.Filename}, nil
	default:
		return nil, fmt.Errorf("unsupported content part type %q", part.Type)
	}
}

type requestTool struct {
	definition petalflow.LLMToolDefinition
}

func (t requestTool) Name() string        { return t.definition.Name }
func (t requestTool) Description() string { return t.definition.Description }
func (t requestTool) Schema() core.ToolSchema {
	data, _ := json.Marshal(t.definition.Parameters)
	return core.ToolSchema{JSONSchema: data}
}

// toRole converts a string role to core.Role, returning an error for an
// unrecognized role rather than silently coercing it to "user".
func toRole(role string) (core.Role, error) {
	switch role {
	case "system":
		return core.RoleSystem, nil
	case "user":
		return core.RoleUser, nil
	case "assistant":
		return core.RoleAssistant, nil
	case "tool":
		return core.RoleTool, nil
	default:
		return "", fmt.Errorf("unknown message role %q", role)
	}
}

// ProviderID returns the underlying provider's ID.
func (a *ProviderAdapter) ProviderID() string {
	return a.provider.ID()
}

// Ensure interface compliance at compile time.
var _ petalflow.LLMClient = (*ProviderAdapter)(nil)
