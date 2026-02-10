package tool

import (
	"fmt"
	"slices"
	"strings"
)

// MergeMCPOverlay applies an overlay to a discovered MCP manifest.
// The overlay wins on conflicts by design.
func MergeMCPOverlay(base Manifest, overlay MCPOverlay) (Manifest, []Diagnostic, error) {
	merged := cloneManifest(base)
	diags := ValidateMCPOverlay(overlay)
	if hasValidationErrors(diags) {
		return merged, diags, &RegistrationValidationError{
			Code:    RegistrationValidationFailedCode,
			Message: "Tool registration failed validation",
			Details: diags,
		}
	}

	for actionName, action := range merged.Actions {
		if strings.TrimSpace(action.MCPToolName) == "" {
			action.MCPToolName = actionName
			merged.Actions[actionName] = action
		}
	}

	if len(overlay.GroupActions) > 0 {
		grouped := make(map[string]ActionSpec, len(merged.Actions))
		for name, action := range merged.Actions {
			grouped[name] = action
		}

		keys := make([]string, 0, len(overlay.GroupActions))
		for key := range overlay.GroupActions {
			keys = append(keys, key)
		}
		slices.Sort(keys)

		for _, alias := range keys {
			target := overlay.GroupActions[alias]
			sourceAction, ok := merged.Actions[target]
			if !ok {
				diags = append(diags, Diagnostic{
					Field:    "group_actions." + alias,
					Code:     "UNKNOWN_ACTION",
					Severity: SeverityError,
					Message:  fmt.Sprintf("MCP tool %q was not discovered", target),
				})
				continue
			}
			sourceAction.MCPToolName = target
			grouped[alias] = sourceAction
			if alias != target {
				delete(grouped, target)
			}
		}
		merged.Actions = grouped
	}

	for action, override := range overlay.DescriptionOverrides {
		spec, ok := merged.Actions[action]
		if !ok {
			continue
		}
		spec.Description = override
		merged.Actions[action] = spec
	}

	for action, inputOverride := range overlay.InputOverrides {
		spec, ok := merged.Actions[action]
		if !ok {
			continue
		}
		if spec.Inputs == nil {
			spec.Inputs = map[string]FieldSpec{}
		}
		for key, value := range inputOverride {
			spec.Inputs[key] = value
		}
		merged.Actions[action] = spec
	}

	for action, outputSchema := range overlay.OutputSchemas {
		spec, ok := merged.Actions[action]
		if !ok {
			continue
		}
		spec.Outputs = cloneFieldMap(outputSchema)
		merged.Actions[action] = spec
	}

	for action, mode := range overlay.ActionModes {
		spec, ok := merged.Actions[action]
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "llm_callable":
			value := true
			spec.LLMCallable = &value
		case "standalone":
			value := false
			spec.LLMCallable = &value
		}
		merged.Actions[action] = spec
	}

	if merged.Config == nil {
		merged.Config = map[string]FieldSpec{}
	}
	for key, spec := range overlay.Config {
		merged.Config[key] = spec.FieldSpec
	}

	if strings.TrimSpace(overlay.Metadata.Author) != "" {
		merged.Tool.Author = overlay.Metadata.Author
	}
	if strings.TrimSpace(overlay.Metadata.Version) != "" {
		merged.Tool.Version = overlay.Metadata.Version
	}
	if strings.TrimSpace(overlay.Metadata.Homepage) != "" {
		merged.Tool.Homepage = overlay.Metadata.Homepage
	}
	if len(overlay.Metadata.Tags) > 0 {
		merged.Tool.Tags = slices.Clone(overlay.Metadata.Tags)
	}

	if strings.TrimSpace(overlay.Health.Strategy) != "" || strings.TrimSpace(overlay.Health.Endpoint) != "" {
		if merged.Health == nil {
			merged.Health = &HealthConfig{}
		}
		if strings.TrimSpace(overlay.Health.Endpoint) != "" {
			merged.Health.Endpoint = overlay.Health.Endpoint
		}
		if strings.TrimSpace(overlay.Health.Method) != "" {
			merged.Health.Method = overlay.Health.Method
		}
		if overlay.Health.IntervalSeconds > 0 {
			merged.Health.IntervalSeconds = overlay.Health.IntervalSeconds
		}
		if overlay.Health.TimeoutMS > 0 {
			merged.Health.TimeoutMS = overlay.Health.TimeoutMS
		}
		if overlay.Health.UnhealthyThreshold > 0 {
			merged.Health.UnhealthyThreshold = overlay.Health.UnhealthyThreshold
		}
	}

	slices.SortFunc(diags, func(a, b Diagnostic) int {
		if cmp := strings.Compare(a.Field, b.Field); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Code, b.Code)
	})
	if hasValidationErrors(diags) {
		return merged, diags, &RegistrationValidationError{
			Code:    RegistrationValidationFailedCode,
			Message: "Tool registration failed validation",
			Details: diags,
		}
	}
	return merged, diags, nil
}

func cloneManifest(in Manifest) Manifest {
	out := in
	out.Actions = make(map[string]ActionSpec, len(in.Actions))
	for key, action := range in.Actions {
		out.Actions[key] = cloneActionSpec(action)
	}
	out.Config = cloneFieldMap(in.Config)
	if in.Health != nil {
		health := *in.Health
		out.Health = &health
	}
	out.Tool.Tags = slices.Clone(in.Tool.Tags)
	return out
}

func cloneActionSpec(in ActionSpec) ActionSpec {
	out := in
	out.Inputs = cloneFieldMap(in.Inputs)
	out.Outputs = cloneFieldMap(in.Outputs)
	if in.LLMCallable != nil {
		value := *in.LLMCallable
		out.LLMCallable = &value
	}
	return out
}

func cloneFieldMap(in map[string]FieldSpec) map[string]FieldSpec {
	if in == nil {
		return nil
	}
	out := make(map[string]FieldSpec, len(in))
	for key, field := range in {
		out[key] = cloneFieldSpec(field)
	}
	return out
}

func cloneFieldSpec(in FieldSpec) FieldSpec {
	out := in
	if in.Items != nil {
		item := cloneFieldSpec(*in.Items)
		out.Items = &item
	}
	if in.Properties != nil {
		out.Properties = cloneFieldMap(in.Properties)
	}
	return out
}
