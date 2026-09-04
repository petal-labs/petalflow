package irisadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/petal-labs/iris/core"
	"github.com/petal-labs/petalflow"
)

type compatibilityProvider struct {
	features map[core.Feature]bool
	model    core.ModelInfo
	response *core.ChatResponse
	request  *core.ChatRequest
	calls    int
}

func (p *compatibilityProvider) ID() string { return "compatibility" }

func (p *compatibilityProvider) Models() []core.ModelInfo { return []core.ModelInfo{p.model} }

func (p *compatibilityProvider) Supports(feature core.Feature) bool {
	return p.features[feature]
}

func (p *compatibilityProvider) Chat(_ context.Context, request *core.ChatRequest) (*core.ChatResponse, error) {
	p.calls++
	p.request = request
	return p.response, nil
}

func (p *compatibilityProvider) StreamChat(context.Context, *core.ChatRequest) (*core.ChatStream, error) {
	return nil, nil
}

func (p *compatibilityProvider) SupportsContentPart(core.ModelID, core.Role, core.ContentPart) bool {
	return true
}

func TestProviderAdapter_CompatibilityContractRoundTrip(t *testing.T) {
	strict := true
	provider := &compatibilityProvider{
		features: map[core.Feature]bool{
			core.FeatureChat:             true,
			core.FeatureStructuredOutput: true,
			core.FeatureToolCalling:      true,
			core.FeatureBuiltInTools:     true,
			core.FeatureReasoning:        true,
			core.FeatureResponseChain:    true,
			core.FeatureWebSearch:        true,
		},
		model: core.ModelInfo{ID: "model", Capabilities: []core.Feature{
			core.FeatureChat, core.FeatureStructuredOutput, core.FeatureToolCalling,
			core.FeatureBuiltInTools, core.FeatureReasoning,
			core.FeatureResponseChain, core.FeatureWebSearch,
		}},
		response: &core.ChatResponse{
			ID: "resp-1", Model: "model", Output: `{"ok":true}`,
			Status: "completed", Citations: []string{"https://example.test/source"},
			Reasoning: &core.ReasoningOutput{ID: "reason-1", Summary: []string{"summary"}},
		},
	}
	adapter := NewProviderAdapter(provider)

	response, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model: "model", Instructions: "Follow the contract.",
		ResponseFormat: petalflow.LLMResponseFormatJSONSchema,
		StructuredOutput: &petalflow.LLMStructuredOutput{
			Name: "result", Description: "A result", Schema: map[string]any{"type": "object"}, Strict: &strict,
		},
		Tools:           []petalflow.LLMToolDefinition{{Name: "lookup", Description: "Look up a value", Parameters: map[string]any{"type": "object"}}},
		BuiltInTools:    []petalflow.LLMBuiltInTool{{Type: "web_search"}},
		ToolResources:   &petalflow.LLMToolResources{FileSearchVectorStoreIDs: []string{"vs-1"}},
		ReasoningEffort: "high", PreviousResponseID: "resp-previous", Truncation: "auto",
		SearchOptions: &petalflow.LLMSearchOptions{DomainFilter: []string{"example.test"}, Recency: "day", Mode: "web"},
		Messages:      []petalflow.LLMMessage{{Role: "user", Parts: []petalflow.LLMContentPart{{Type: "input_text", Text: "Find it"}}}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	request := provider.request
	if request == nil {
		t.Fatal("provider did not receive a request")
	}
	if request.ResponseFormat != core.ResponseFormatJSONSchema || request.JSONSchema == nil || !request.JSONSchema.Strict {
		t.Fatalf("structured output was not mapped: %+v", request.JSONSchema)
	}
	if request.JSONSchema.Name != "result" || request.JSONSchema.Description != "A result" {
		t.Fatalf("schema metadata was not preserved: %+v", request.JSONSchema)
	}
	if len(request.Tools) != 1 || request.Tools[0].Name() != "lookup" {
		t.Fatalf("tools were not mapped: %#v", request.Tools)
	}
	if len(request.BuiltInTools) != 1 || request.BuiltInTools[0].Type != "web_search" {
		t.Fatalf("built-in tools were not mapped: %#v", request.BuiltInTools)
	}
	if request.PreviousResponseID != "resp-previous" || request.ReasoningEffort != core.ReasoningEffortHigh {
		t.Fatalf("Responses fields were not mapped: %+v", request)
	}
	if request.SearchOptions == nil || request.SearchOptions.Recency != core.SearchRecencyDay {
		t.Fatalf("search options were not mapped: %+v", request.SearchOptions)
	}
	if len(request.Messages[0].Parts) != 1 {
		t.Fatalf("content parts were not mapped: %+v", request.Messages[0])
	}

	if response.ResponseID != "resp-1" || response.Status != "completed" || len(response.Citations) != 1 {
		t.Fatalf("response metadata was not preserved: %+v", response)
	}
	if response.Reasoning == nil || response.Reasoning.ID != "reason-1" {
		t.Fatalf("reasoning was not preserved: %+v", response.Reasoning)
	}
	if response.JSONValue == nil || response.JSON["ok"] != true {
		t.Fatalf("structured response was not parsed: JSON=%v value=%v", response.JSON, response.JSONValue)
	}
}

func TestProviderAdapter_StructuredOutputSupportsAnyJSONRoot(t *testing.T) {
	provider := &compatibilityProvider{
		features: map[core.Feature]bool{core.FeatureChat: true, core.FeatureStructuredOutput: true},
		model:    core.ModelInfo{ID: "model", Capabilities: []core.Feature{core.FeatureChat, core.FeatureStructuredOutput}},
		response: &core.ChatResponse{Output: `["a", 2, true]`},
	}
	adapter := NewProviderAdapter(provider)

	response, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model: "model", JSONSchema: map[string]any{"type": "array"},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	values, ok := response.JSONValue.([]any)
	if !ok || len(values) != 3 || values[1] != float64(2) {
		t.Fatalf("JSONValue = %#v, want decoded array", response.JSONValue)
	}
	if response.JSON != nil {
		t.Fatalf("legacy object JSON should be nil for array output: %#v", response.JSON)
	}
}

func TestProviderAdapter_UnsupportedCapabilityFailsBeforeProviderCall(t *testing.T) {
	provider := &compatibilityProvider{
		features: map[core.Feature]bool{core.FeatureChat: true},
		model:    core.ModelInfo{ID: "model"},
		response: &core.ChatResponse{Output: "unexpected"},
	}
	adapter := NewProviderAdapter(provider)

	_, err := adapter.Complete(context.Background(), petalflow.LLMRequest{
		Model: "model", InputText: "reason", ReasoningEffort: "high",
	})
	if err == nil || !strings.Contains(err.Error(), "reasoning") || !strings.Contains(err.Error(), "model") {
		t.Fatalf("error = %v, want actionable reasoning capability error", err)
	}
	if provider.calls != 0 {
		t.Fatalf("provider was called %d times after capability validation", provider.calls)
	}
}

func TestProviderAdapter_CompleteStream_PreservesFinalMetadata(t *testing.T) {
	provider := &streamingMockProvider{
		mockProvider: mockProvider{id: "mock"},
		streamFn: func(context.Context, *core.ChatRequest) (*core.ChatStream, error) {
			return newMockStream([]string{"answer"}, &core.ChatResponse{
				ID: "resp-2", Model: "model", Status: "completed",
				Citations: []string{"https://example.test/source"},
				Reasoning: &core.ReasoningOutput{ID: "reason-2", Summary: []string{"stream summary"}},
				ToolCalls: []core.ToolCall{{ID: "call-1", Name: "lookup", Arguments: []byte(`{"q":"x"}`)}},
			}, nil), nil
		},
	}
	adapter := NewProviderAdapter(provider)
	chunks, err := adapter.CompleteStream(context.Background(), petalflow.LLMRequest{Model: "model", InputText: "answer"})
	if err != nil {
		t.Fatalf("CompleteStream() error = %v", err)
	}
	var final petalflow.StreamChunk
	for chunk := range chunks {
		if chunk.Done {
			final = chunk
		}
	}
	if final.ResponseID != "resp-2" || final.Status != "completed" || final.Model != "model" || final.Provider != "mock" {
		t.Fatalf("stream metadata = %+v", final)
	}
	if final.Reasoning == nil || len(final.ToolCalls) != 1 || len(final.Citations) != 1 {
		t.Fatalf("stream response fields = %+v", final)
	}
	if final.Response == nil || final.Response.ResponseID != final.ResponseID || final.Response.Status != final.Status {
		t.Fatalf("stream complete response = %+v", final.Response)
	}
}
