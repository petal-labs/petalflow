package tool

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	MCPOverlayVersionV1 = "1.0"
)

// MCPOverlay describes overlay fields that augment MCP discovery results.
type MCPOverlay struct {
	OverlayVersion       string                           `yaml:"overlay_version" json:"overlay_version"`
	GroupActions         map[string]string                `yaml:"group_actions,omitempty" json:"group_actions,omitempty"`
	ActionModes          map[string]string                `yaml:"action_modes,omitempty" json:"action_modes,omitempty"`
	OutputSchemas        map[string]map[string]FieldSpec  `yaml:"output_schemas,omitempty" json:"output_schemas,omitempty"`
	Config               map[string]MCPOverlayConfigField `yaml:"config,omitempty" json:"config,omitempty"`
	Health               MCPOverlayHealth                 `yaml:"health,omitempty" json:"health,omitempty"`
	DescriptionOverrides map[string]string                `yaml:"description_overrides,omitempty" json:"description_overrides,omitempty"`
	InputOverrides       map[string]map[string]FieldSpec  `yaml:"input_overrides,omitempty" json:"input_overrides,omitempty"`
	Metadata             MCPOverlayMetadata               `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// MCPOverlayConfigField extends FieldSpec with environment variable mapping.
type MCPOverlayConfigField struct {
	FieldSpec `yaml:",inline" json:",inline"`
	EnvVar    string `yaml:"env_var,omitempty" json:"env_var,omitempty"`
}

// MCPOverlayHealth defines overlay health behavior.
type MCPOverlayHealth struct {
	Strategy           string `yaml:"strategy,omitempty" json:"strategy,omitempty"`
	Endpoint           string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	Method             string `yaml:"method,omitempty" json:"method,omitempty"`
	IntervalSeconds    int    `yaml:"interval_seconds,omitempty" json:"interval_seconds,omitempty"`
	TimeoutMS          int    `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
	UnhealthyThreshold int    `yaml:"unhealthy_threshold,omitempty" json:"unhealthy_threshold,omitempty"`
}

// MCPOverlayMetadata stores optional manifest metadata overrides.
type MCPOverlayMetadata struct {
	Author   string   `yaml:"author,omitempty" json:"author,omitempty"`
	Version  string   `yaml:"version,omitempty" json:"version,omitempty"`
	Homepage string   `yaml:"homepage,omitempty" json:"homepage,omitempty"`
	Tags     []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// ParseMCPOverlayFile parses and validates an overlay file from disk.
func ParseMCPOverlayFile(path string) (MCPOverlay, []Diagnostic, error) {
	// #nosec G304 -- overlay path comes from explicit user CLI input.
	data, err := os.ReadFile(path)
	if err != nil {
		return MCPOverlay{}, nil, err
	}
	return ParseMCPOverlayYAML(data)
}

// ParseMCPOverlayYAML parses and validates an overlay payload.
func ParseMCPOverlayYAML(data []byte) (MCPOverlay, []Diagnostic, error) {
	var overlay MCPOverlay
	if err := yaml.Unmarshal(data, &overlay); err != nil {
		return MCPOverlay{}, nil, fmt.Errorf("tool: parse overlay yaml: %w", err)
	}

	diags := ValidateMCPOverlay(overlay)
	return overlay, diags, nil
}

// ValidateMCPOverlay validates overlay syntax and type declarations.
func ValidateMCPOverlay(overlay MCPOverlay) []Diagnostic {
	diags := make([]Diagnostic, 0)

	if strings.TrimSpace(overlay.OverlayVersion) == "" {
		diags = append(diags, Diagnostic{
			Field:    "overlay_version",
			Code:     "REQUIRED_FIELD",
			Severity: SeverityError,
			Message:  "overlay_version is required",
		})
	} else if overlay.OverlayVersion != MCPOverlayVersionV1 {
		diags = append(diags, Diagnostic{
			Field:    "overlay_version",
			Code:     "ENUM",
			Severity: SeverityError,
			Message:  fmt.Sprintf("overlay_version must be %q", MCPOverlayVersionV1),
		})
	}

	for action, target := range overlay.GroupActions {
		if strings.TrimSpace(action) == "" {
			diags = append(diags, Diagnostic{
				Field:    "group_actions",
				Code:     "INVALID_ACTION_NAME",
				Severity: SeverityError,
				Message:  "group_actions keys must not be empty",
			})
		}
		if strings.TrimSpace(target) == "" {
			diags = append(diags, Diagnostic{
				Field:    "group_actions." + action,
				Code:     "REQUIRED_FIELD",
				Severity: SeverityError,
				Message:  "group_actions value must map to an MCP tool name",
			})
		}
	}

	for action, mode := range overlay.ActionModes {
		trimmedMode := strings.TrimSpace(strings.ToLower(mode))
		switch trimmedMode {
		case "llm_callable", "standalone":
			// valid
		default:
			diags = append(diags, Diagnostic{
				Field:    "action_modes." + action,
				Code:     "ENUM",
				Severity: SeverityError,
				Message:  `action mode must be "llm_callable" or "standalone"`,
			})
		}
	}

	for key, spec := range overlay.Config {
		if strings.TrimSpace(spec.Type) == "" {
			diags = append(diags, Diagnostic{
				Field:    "config." + key + ".type",
				Code:     "REQUIRED_FIELD",
				Severity: SeverityError,
				Message:  "config field type is required",
			})
		}
	}

	healthStrategy := strings.TrimSpace(strings.ToLower(overlay.Health.Strategy))
	switch healthStrategy {
	case "", "process", "connection", "ping", "endpoint":
	default:
		diags = append(diags, Diagnostic{
			Field:    "health.strategy",
			Code:     "ENUM",
			Severity: SeverityError,
			Message:  "health.strategy must be one of: process, connection, ping, endpoint",
		})
	}
	if healthStrategy == "endpoint" && strings.TrimSpace(overlay.Health.Endpoint) == "" {
		diags = append(diags, Diagnostic{
			Field:    "health.endpoint",
			Code:     "REQUIRED_FIELD",
			Severity: SeverityError,
			Message:  "health.endpoint is required when strategy is endpoint",
		})
	}

	typeManifest := NewManifest("overlay_type_validation")
	typeManifest.Actions = map[string]ActionSpec{}
	typeManifest.Config = map[string]FieldSpec{}

	for action, outputs := range overlay.OutputSchemas {
		typeManifest.Actions[action] = ActionSpec{
			Outputs: outputs,
		}
	}
	for action, inputs := range overlay.InputOverrides {
		spec := typeManifest.Actions[action]
		spec.Inputs = inputs
		typeManifest.Actions[action] = spec
	}
	for key, field := range overlay.Config {
		typeManifest.Config[key] = field.FieldSpec
	}

	validator := V1TypeSystemValidator{}
	diags = append(diags, validator.ValidateManifest(typeManifest)...)

	slices.SortFunc(diags, func(a, b Diagnostic) int {
		if cmp := strings.Compare(a.Field, b.Field); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Code, b.Code)
	})
	return diags
}
