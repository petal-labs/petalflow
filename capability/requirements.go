package capability

import "github.com/petal-labs/petalflow/core"

// RequirementsFromRequest derives the capabilities a request needs from its
// shape: tool definitions, structured output constraints, reasoning effort,
// multimodal content parts, and built-in tools all imply capabilities.
// Nodes do not declare requirements separately — the request is the
// requirement.
func RequirementsFromRequest(req core.LLMRequest) Set {
	required := Set{}

	if len(req.Tools) > 0 || len(req.BuiltInTools) > 0 {
		required.Add(CapabilityTools)
	}
	if req.StructuredOutput != nil || req.JSONSchema != nil ||
		req.ResponseFormat == core.LLMResponseFormatJSON ||
		req.ResponseFormat == core.LLMResponseFormatJSONSchema {
		required.Add(CapabilityStructuredOutput)
	}
	if req.ReasoningEffort != "" {
		required.Add(CapabilityReasoning)
	}
	for _, m := range req.Messages {
		for _, part := range m.Parts {
			switch part.Type {
			case "image", "input_image", "file", "input_file", "audio", "input_audio":
				required.Add(CapabilityMultimodalInput)
			}
		}
	}
	return required
}

// RequirementsFromConfig derives capabilities implied by node-level LLM
// configuration (the hydrate-side view, before a request is built).
func RequirementsFromConfig(jsonSchemaSet bool) Set {
	required := Set{}
	if jsonSchemaSet {
		required.Add(CapabilityStructuredOutput)
	}
	return required
}
