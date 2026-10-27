package capability

import (
	"testing"

	"github.com/petal-labs/petalflow/core"
)

func TestRequirementsFromRequest(t *testing.T) {
	tests := []struct {
		name string
		req  core.LLMRequest
		want []Capability
	}{
		{
			name: "plain text request",
			req:  core.LLMRequest{Model: "m", InputText: "hi"},
			want: nil,
		},
		{
			name: "tools",
			req:  core.LLMRequest{Model: "m", Tools: []core.LLMToolDefinition{{Name: "search"}}},
			want: []Capability{CapabilityTools},
		},
		{
			name: "built-in tools",
			req:  core.LLMRequest{Model: "m", BuiltInTools: []core.LLMBuiltInTool{{Type: "web_search"}}},
			want: []Capability{CapabilityTools},
		},
		{
			name: "structured output",
			req: core.LLMRequest{Model: "m", StructuredOutput: &core.LLMStructuredOutput{
				Name: "result", Schema: map[string]any{"type": "object"},
			}},
			want: []Capability{CapabilityStructuredOutput},
		},
		{
			name: "legacy json schema",
			req:  core.LLMRequest{Model: "m", JSONSchema: map[string]any{"type": "object"}},
			want: []Capability{CapabilityStructuredOutput},
		},
		{
			name: "json response format",
			req:  core.LLMRequest{Model: "m", ResponseFormat: core.LLMResponseFormatJSON},
			want: []Capability{CapabilityStructuredOutput},
		},
		{
			name: "reasoning effort",
			req:  core.LLMRequest{Model: "m", ReasoningEffort: "high"},
			want: []Capability{CapabilityReasoning},
		},
		{
			name: "multimodal image",
			req: core.LLMRequest{Model: "m", Messages: []core.LLMMessage{{
				Role: "user",
				Parts: []core.LLMContentPart{
					{Type: "text", Text: "what is this"},
					{Type: "input_image", URL: "https://example.com/img.png"},
				},
			}}},
			want: []Capability{CapabilityMultimodalInput},
		},
		{
			name: "text-only parts are not multimodal",
			req: core.LLMRequest{Model: "m", Messages: []core.LLMMessage{{
				Role:  "user",
				Parts: []core.LLMContentPart{{Type: "input_text", Text: "hello"}},
			}}},
			want: nil,
		},
		{
			name: "combined",
			req: core.LLMRequest{
				Model:            "m",
				Tools:            []core.LLMToolDefinition{{Name: "search"}},
				ReasoningEffort:  "low",
				StructuredOutput: &core.LLMStructuredOutput{Schema: map[string]any{}},
			},
			want: []Capability{CapabilityReasoning, CapabilityStructuredOutput, CapabilityTools},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RequirementsFromRequest(tt.req)
			if len(got) != len(tt.want) {
				t.Fatalf("requirements = %v, want %v", got.Names(), tt.want)
			}
			for _, c := range tt.want {
				if !got.Has(c) {
					t.Errorf("missing capability %q in %v", c, got.Names())
				}
			}
		})
	}
}

func TestRequirementsFromConfig(t *testing.T) {
	if got := RequirementsFromConfig(false); len(got) != 0 {
		t.Errorf("no schema should imply nothing, got %v", got.Names())
	}
	got := RequirementsFromConfig(true)
	if len(got) != 1 || !got.Has(CapabilityStructuredOutput) {
		t.Errorf("json schema should imply structured_output, got %v", got.Names())
	}
}
