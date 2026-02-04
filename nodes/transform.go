package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/petal-labs/petalflow/core"
)

// TransformType specifies the type of transformation to apply.
type TransformType string

const (
	// TransformPick extracts specific fields from the input.
	TransformPick TransformType = "pick"

	// TransformOmit removes specific fields from the input.
	TransformOmit TransformType = "omit"

	// TransformRename renames fields in the input.
	TransformRename TransformType = "rename"

	// TransformFlatten flattens a nested structure.
	TransformFlatten TransformType = "flatten"

	// TransformMerge merges multiple variables into one.
	TransformMerge TransformType = "merge"

	// TransformTemplate renders a Go text template.
	TransformTemplate TransformType = "template"

	// TransformStringify converts the input to a string representation.
	TransformStringify TransformType = "stringify"

	// TransformParse parses a string into structured data.
	TransformParse TransformType = "parse"

	// TransformMap applies a transformation to each item in a list.
	TransformMap TransformType = "map"

	// TransformCustom uses a custom transformation function.
	TransformCustom TransformType = "custom"
)

// TransformNodeConfig configures a TransformNode.
type TransformNodeConfig struct {
	// Transform specifies the transformation type.
	Transform TransformType

	// InputVar specifies the source variable (dot notation supported).
	// For TransformMerge, use InputVars instead.
	InputVar string

	// InputVars specifies multiple source variables (for TransformMerge).
	InputVars []string

	// OutputVar specifies where to store the result.
	OutputVar string

	// Fields lists field paths for pick/omit operations.
	// Supports dot notation (e.g., "meta.score", "user.name").
	Fields []string

	// Mapping provides old->new field name mappings for rename.
	Mapping map[string]string

	// Template is the Go text template string for template transform.
	// Uses {{.varname}} syntax to access envelope variables.
	Template string

	// Format specifies the format for stringify/parse ("json" or "yaml").
	// Defaults to "json".
	Format string

	// Separator is used for flatten (default: ".").
	Separator string

	// MaxDepth limits flatten depth (0 = unlimited).
	MaxDepth int

	// MergeStrategy specifies how to merge: "shallow" or "deep".
	// Defaults to "shallow".
	MergeStrategy string

	// ItemTransform specifies nested transformation for map operations.
	ItemTransform *TransformNodeConfig

	// CustomFunc provides custom transformation logic.
	CustomFunc func(ctx context.Context, env *core.Envelope) (any, error)
}

// TransformNode transforms data from envelope variables.
// It provides common data reshaping operations for workflow glue code.
type TransformNode struct {
	core.BaseNode
	config TransformNodeConfig
}

// NewTransformNode creates a new TransformNode with the given configuration.
func NewTransformNode(id string, config TransformNodeConfig) *TransformNode {
	// Set defaults
	if config.Format == "" {
		config.Format = "json"
	}
	if config.Separator == "" {
		config.Separator = "."
	}
	if config.MergeStrategy == "" {
		config.MergeStrategy = "shallow"
	}

	return &TransformNode{
		BaseNode: core.NewBaseNode(id, core.NodeKindTransform),
		config:   config,
	}
}

// Config returns the node's configuration.
func (n *TransformNode) Config() TransformNodeConfig {
	return n.config
}

// Run executes the transformation.
func (n *TransformNode) Run(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	result := env.Clone()

	var output any
	var err error

	switch n.config.Transform {
	case TransformPick:
		output, err = n.transformPick(env)
	case TransformOmit:
		output, err = n.transformOmit(env)
	case TransformRename:
		output, err = n.transformRename(env)
	case TransformFlatten:
		output, err = n.transformFlatten(env)
	case TransformMerge:
		output, err = n.transformMerge(env)
	case TransformTemplate:
		output, err = n.transformTemplate(env)
	case TransformStringify:
		output, err = n.transformStringify(env)
	case TransformParse:
		output, err = n.transformParse(env)
	case TransformMap:
		output, err = n.transformMap(ctx, env)
	case TransformCustom:
		output, err = n.transformCustom(ctx, env)
	default:
		return nil, fmt.Errorf("transform node %s: unknown transform type %q", n.ID(), n.config.Transform)
	}

	if err != nil {
		return nil, fmt.Errorf("transform node %s: %w", n.ID(), err)
	}

	// Store the result
	if n.config.OutputVar == "" {
		return nil, fmt.Errorf("transform node %s: OutputVar is required", n.ID())
	}
	result.SetVar(n.config.OutputVar, output)

	return result, nil
}

// transformPick extracts specified fields from the input.
func (n *TransformNode) transformPick(env *core.Envelope) (any, error) {
	if len(n.config.Fields) == 0 {
		return nil, fmt.Errorf("pick requires Fields")
	}

	input, err := n.getInput(env)
	if err != nil {
		return nil, err
	}

	inputMap, ok := toMap(input)
	if !ok {
		return nil, fmt.Errorf("pick requires map input, got %T", input)
	}

	result := make(map[string]any)
	for _, field := range n.config.Fields {
		val, found := getNestedValue(inputMap, field)
		if found {
			setNestedValue(result, field, val)
		}
	}

	return result, nil
}

// transformOmit removes specified fields from the input.
func (n *TransformNode) transformOmit(env *core.Envelope) (any, error) {
	if len(n.config.Fields) == 0 {
		return nil, fmt.Errorf("omit requires Fields")
	}

	input, err := n.getInput(env)
	if err != nil {
		return nil, err
	}

	inputMap, ok := toMap(input)
	if !ok {
		return nil, fmt.Errorf("omit requires map input, got %T", input)
	}

	// Deep copy the input
	result := deepCopyMap(inputMap)

	// Remove specified fields
	for _, field := range n.config.Fields {
		deleteNestedValue(result, field)
	}

	return result, nil
}

// transformRename renames fields in the input.
func (n *TransformNode) transformRename(env *core.Envelope) (any, error) {
	if len(n.config.Mapping) == 0 {
		return nil, fmt.Errorf("rename requires Mapping")
	}

	input, err := n.getInput(env)
	if err != nil {
		return nil, err
	}

	inputMap, ok := toMap(input)
	if !ok {
		return nil, fmt.Errorf("rename requires map input, got %T", input)
	}

	// Deep copy the input
	result := deepCopyMap(inputMap)

	// Apply renames
	for oldName, newName := range n.config.Mapping {
		val, found := getNestedValue(result, oldName)
		if found {
			deleteNestedValue(result, oldName)
			setNestedValue(result, newName, val)
		}
	}

	return result, nil
}

// transformFlatten flattens a nested map structure.
func (n *TransformNode) transformFlatten(env *core.Envelope) (any, error) {
	input, err := n.getInput(env)
	if err != nil {
		return nil, err
	}

	inputMap, ok := toMap(input)
	if !ok {
		return nil, fmt.Errorf("flatten requires map input, got %T", input)
	}

	result := make(map[string]any)
	flattenMap(inputMap, "", n.config.Separator, n.config.MaxDepth, 0, result)

	return result, nil
}

// transformMerge merges multiple variables into one.
func (n *TransformNode) transformMerge(env *core.Envelope) (any, error) {
	if len(n.config.InputVars) == 0 {
		return nil, fmt.Errorf("merge requires InputVars")
	}

	result := make(map[string]any)

	for _, varName := range n.config.InputVars {
		val, ok := env.GetVarNested(varName)
		if !ok {
			continue // Skip missing variables
		}

		valMap, ok := toMap(val)
		if !ok {
			// If not a map, store under the variable name
			result[varName] = val
			continue
		}

		if n.config.MergeStrategy == "deep" {
			deepMerge(result, valMap)
		} else {
			// Shallow merge
			for k, v := range valMap {
				result[k] = v
			}
		}
	}

	return result, nil
}

// transformTemplate renders a Go text template.
func (n *TransformNode) transformTemplate(env *core.Envelope) (any, error) {
	if n.config.Template == "" {
		return nil, fmt.Errorf("template requires Template string")
	}

	// Create template with custom functions
	tmpl, err := template.New("transform").Funcs(transformTemplateFuncs()).Parse(n.config.Template)
	if err != nil {
		return nil, fmt.Errorf("invalid template: %w", err)
	}

	// Build template data from envelope vars
	data := make(map[string]any)
	for k, v := range env.Vars {
		data[k] = v
	}
	// Also include envelope itself for access to Input, Artifacts, etc.
	data["_env"] = env
	data["_input"] = env.Input

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("template execution failed: %w", err)
	}

	return buf.String(), nil
}

// transformStringify converts input to a string.
func (n *TransformNode) transformStringify(env *core.Envelope) (any, error) {
	input, err := n.getInput(env)
	if err != nil {
		return nil, err
	}

	switch n.config.Format {
	case "json":
		data, err := json.MarshalIndent(input, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("json marshal failed: %w", err)
		}
		return string(data), nil

	case "text":
		return fmt.Sprintf("%v", input), nil

	default:
		return nil, fmt.Errorf("unsupported stringify format: %s", n.config.Format)
	}
}

// transformParse parses a string into structured data.
func (n *TransformNode) transformParse(env *core.Envelope) (any, error) {
	input, err := n.getInput(env)
	if err != nil {
		return nil, err
	}

	inputStr, ok := input.(string)
	if !ok {
		return nil, fmt.Errorf("parse requires string input, got %T", input)
	}

	switch n.config.Format {
	case "json":
		var result any
		if err := json.Unmarshal([]byte(inputStr), &result); err != nil {
			return nil, fmt.Errorf("json parse failed: %w", err)
		}
		return result, nil

	default:
		return nil, fmt.Errorf("unsupported parse format: %s", n.config.Format)
	}
}

// transformMap applies a transformation to each item in a list.
func (n *TransformNode) transformMap(ctx context.Context, env *core.Envelope) (any, error) {
	if n.config.ItemTransform == nil {
		return nil, fmt.Errorf("map requires ItemTransform")
	}

	input, err := n.getInput(env)
	if err != nil {
		return nil, err
	}

	items, err := toSlice(input)
	if err != nil {
		return nil, fmt.Errorf("map requires slice input: %w", err)
	}

	// Create a child transform node for processing items
	childNode := NewTransformNode(n.ID()+"_item", *n.config.ItemTransform)

	results := make([]any, len(items))
	for i, item := range items {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		// Create envelope with the item as the input variable
		itemEnv := env.Clone()
		itemEnv.SetVar("_item", item)

		// If ItemTransform uses InputVar="_item", it will get this item
		resultEnv, err := childNode.Run(ctx, itemEnv)
		if err != nil {
			return nil, fmt.Errorf("map item %d: %w", i, err)
		}

		// Get the transformed result
		result, ok := resultEnv.GetVar(n.config.ItemTransform.OutputVar)
		if !ok {
			results[i] = nil
		} else {
			results[i] = result
		}
	}

	return results, nil
}

// transformCustom applies a custom transformation function.
func (n *TransformNode) transformCustom(ctx context.Context, env *core.Envelope) (any, error) {
	if n.config.CustomFunc == nil {
		return nil, fmt.Errorf("custom transform requires CustomFunc")
	}

	return n.config.CustomFunc(ctx, env)
}

// getInput retrieves the input value from the envelope.
func (n *TransformNode) getInput(env *core.Envelope) (any, error) {
	if n.config.InputVar == "" {
		return nil, fmt.Errorf("InputVar is required")
	}

	val, ok := env.GetVarNested(n.config.InputVar)
	if !ok {
		return nil, fmt.Errorf("variable %q not found", n.config.InputVar)
	}

	return val, nil
}

// transformTemplateFuncs returns custom template functions for transform.
func transformTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"json": func(v any) string {
			data, err := json.Marshal(v)
			if err != nil {
				return fmt.Sprintf("error: %v", err)
			}
			return string(data)
		},
		"jsonPretty": func(v any) string {
			data, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				return fmt.Sprintf("error: %v", err)
			}
			return string(data)
		},
		"join":      strings.Join,
		"split":     strings.Split,
		"upper":     strings.ToUpper,
		"lower":     strings.ToLower,
		"trim":      strings.TrimSpace,
		"contains":  strings.Contains,
		"hasPrefix": strings.HasPrefix,
		"hasSuffix": strings.HasSuffix,
		"default": func(defaultVal, val any) any {
			if val == nil || val == "" {
				return defaultVal
			}
			return val
		},
		"coalesce": func(vals ...any) any {
			for _, v := range vals {
				if v != nil && v != "" {
					return v
				}
			}
			return nil
		},
	}
}

// Ensure interface compliance at compile time.
var _ core.Node = (*TransformNode)(nil)
