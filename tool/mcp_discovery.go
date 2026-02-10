package tool

import (
	"context"
	"fmt"
	"slices"
	"strings"

	mcpclient "github.com/petal-labs/petalflow/tool/mcp"
)

// MCPDiscoveryConfig configures MCP manifest discovery.
type MCPDiscoveryConfig struct {
	Name      string
	Transport MCPTransport
	Config    map[string]string
	Overlay   *MCPOverlay
}

// DiscoverMCPManifest calls MCP initialize/tools/list and maps results to a manifest.
func DiscoverMCPManifest(ctx context.Context, cfg MCPDiscoveryConfig) (Manifest, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return Manifest{}, fmt.Errorf("tool: mcp discovery requires registration name")
	}

	client, cleanup, err := newMCPClientFromConfig(ctx, cfg.Name, cfg.Transport, cfg.Config, cfg.Overlay)
	if err != nil {
		return Manifest{}, err
	}
	defer cleanup()

	if _, err := client.Initialize(ctx); err != nil {
		return Manifest{}, fmt.Errorf("tool: mcp initialize failed: %w", err)
	}

	list, err := client.ListTools(ctx)
	if err != nil {
		return Manifest{}, fmt.Errorf("tool: mcp tools/list failed: %w", err)
	}

	manifest := NewManifest(cfg.Name)
	manifest.Transport = NewMCPTransport(cfg.Transport)
	manifest.Actions = make(map[string]ActionSpec, len(list.Tools))

	for _, discovered := range list.Tools {
		actionName := sanitizeMCPActionName(discovered.Name)
		action := ActionSpec{
			MCPToolName: discovered.Name,
			Description: strings.TrimSpace(discovered.Description),
			Inputs:      mapInputSchema(discovered.InputSchema),
			Outputs:     map[string]FieldSpec{},
		}
		manifest.Actions[actionName] = action
	}

	if cfg.Overlay == nil {
		return manifest, nil
	}

	merged, _, err := MergeMCPOverlay(manifest, *cfg.Overlay)
	if err != nil {
		return Manifest{}, err
	}
	return merged, nil
}

func sanitizeMCPActionName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "action"
	}
	builder := strings.Builder{}
	for i, r := range trimmed {
		valid := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_'
		if valid {
			builder.WriteRune(r)
			continue
		}
		if i == 0 || builder.Len() == 0 || builder.String()[builder.Len()-1] == '_' {
			continue
		}
		builder.WriteRune('_')
	}
	out := strings.ToLower(strings.Trim(builder.String(), "_"))
	if out == "" {
		return "action"
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "a_" + out
	}
	return out
}

func mapInputSchema(schema map[string]any) map[string]FieldSpec {
	if len(schema) == 0 {
		return map[string]FieldSpec{}
	}

	requiredSet := make(map[string]struct{})
	if requiredRaw, ok := schema["required"].([]any); ok {
		for _, item := range requiredRaw {
			if field, ok := item.(string); ok {
				requiredSet[field] = struct{}{}
			}
		}
	}

	propertiesRaw, ok := schema["properties"].(map[string]any)
	if !ok {
		return map[string]FieldSpec{}
	}

	keys := make([]string, 0, len(propertiesRaw))
	for key := range propertiesRaw {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	inputs := make(map[string]FieldSpec, len(keys))
	for _, key := range keys {
		fieldSchema, _ := propertiesRaw[key].(map[string]any)
		spec := jsonSchemaToFieldSpec(fieldSchema)
		_, required := requiredSet[key]
		spec.Required = required
		inputs[key] = spec
	}
	return inputs
}

func jsonSchemaToFieldSpec(schema map[string]any) FieldSpec {
	spec := FieldSpec{
		Type: TypeAny,
	}

	if schema == nil {
		return spec
	}
	if desc, ok := schema["description"].(string); ok {
		spec.Description = desc
	}
	if defaultValue, ok := schema["default"]; ok {
		spec.Default = defaultValue
	}
	if fieldType, ok := schema["type"].(string); ok {
		spec.Type = mapJSONSchemaType(fieldType)
	} else if typeArray, ok := schema["type"].([]any); ok {
		for _, rawType := range typeArray {
			typeName, _ := rawType.(string)
			if strings.EqualFold(typeName, "null") {
				continue
			}
			spec.Type = mapJSONSchemaType(typeName)
			break
		}
	}

	if spec.Type == TypeArray {
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			itemSpec := jsonSchemaToFieldSpec(itemSchema)
			spec.Items = &itemSpec
		} else {
			itemSpec := FieldSpec{Type: TypeAny}
			spec.Items = &itemSpec
		}
	}

	if spec.Type == TypeObject {
		if props, ok := schema["properties"].(map[string]any); ok {
			spec.Properties = make(map[string]FieldSpec, len(props))
			keys := make([]string, 0, len(props))
			for key := range props {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				childSchema, _ := props[key].(map[string]any)
				spec.Properties[key] = jsonSchemaToFieldSpec(childSchema)
			}
		}
	}

	return spec
}

func mapJSONSchemaType(jsonType string) string {
	switch strings.ToLower(strings.TrimSpace(jsonType)) {
	case "string":
		return TypeString
	case "integer":
		return TypeInteger
	case "number":
		return TypeFloat
	case "boolean":
		return TypeBoolean
	case "array":
		return TypeArray
	case "object":
		return TypeObject
	default:
		return TypeAny
	}
}

func newMCPClientFromConfig(ctx context.Context, name string, transport MCPTransport, config map[string]string, overlay *MCPOverlay) (*mcpclient.Client, func(), error) {
	runtimeTransport, err := newMCPRuntimeTransport(ctx, name, transport, config, overlay)
	if err != nil {
		return nil, nil, err
	}

	client := mcpclient.NewClient(runtimeTransport, mcpclient.Options{
		ClientInfo: mcpclient.ClientInfo{
			Name:    "petalflow",
			Version: "dev",
		},
		Capabilities: map[string]any{
			"tools": map[string]any{},
		},
	})

	cleanup := func() {
		_ = client.Close(context.Background())
	}
	return client, cleanup, nil
}
