package tool

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const (
	manifestSchemaV1Path = "schemas/tool-manifest-v1.json"
	maxInt64AsUint64     = ^uint64(0) >> 1
)

var (
	//go:embed schemas/tool-manifest-v1.json
	manifestSchemaFiles embed.FS

	allowedFieldTypes = map[string]struct{}{
		"string":  {},
		"integer": {},
		"float":   {},
		"boolean": {},
		"bytes":   {},
		"array":   {},
		"object":  {},
		"any":     {},
	}
)

// LoadManifestSchemaV1 returns the bundled JSON schema artifact for v1 manifests.
func LoadManifestSchemaV1() ([]byte, error) {
	data, err := manifestSchemaFiles.ReadFile(manifestSchemaV1Path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest schema: %w", err)
	}
	return slices.Clone(data), nil
}

// ValidateManifestJSON validates manifest JSON and returns field-level diagnostics.
func ValidateManifestJSON(data []byte) Result {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return Result{
			Diagnostics: []Diagnostic{{
				Field:    "$",
				Code:     "INVALID_JSON",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Invalid JSON: %v", err),
			}},
		}
	}

	root, ok := raw.(map[string]any)
	if !ok {
		return Result{
			Diagnostics: []Diagnostic{{
				Field:    "$",
				Code:     "TYPE",
				Severity: SeverityError,
				Message:  "Manifest must be a JSON object",
			}},
		}
	}

	validator := manifestSchemaValidator{}
	validator.validateRoot(root)
	return Result{Diagnostics: validator.diags}
}

type manifestSchemaValidator struct {
	diags []Diagnostic
}

func (v *manifestSchemaValidator) validateRoot(root map[string]any) {
	v.requireString(root, "manifest_version", "manifest_version")
	if version, ok := readString(root, "manifest_version"); ok && version != ManifestVersionV1 {
		v.add("manifest_version", "ENUM", fmt.Sprintf("must be %q", ManifestVersionV1))
	}

	if schema, ok := root["$schema"]; ok {
		if _, ok := schema.(string); !ok {
			v.add("$schema", "TYPE", "must be a string")
		}
	}

	toolObj, ok := v.requireObject(root, "tool", "tool")
	if ok {
		v.validateTool(toolObj)
	}

	transportObj, ok := v.requireObject(root, "transport", "transport")
	if ok {
		v.validateTransport(transportObj)
	}

	actionsObj, ok := v.requireObject(root, "actions", "actions")
	if ok {
		v.validateActions(actionsObj, "actions")
	}

	if configRaw, ok := root["config"]; ok {
		configObj, ok := configRaw.(map[string]any)
		if !ok {
			v.add("config", "TYPE", "must be an object")
		} else {
			v.validateFieldMap(configObj, "config")
		}
	}

	if healthRaw, ok := root["health"]; ok {
		healthObj, ok := healthRaw.(map[string]any)
		if !ok {
			v.add("health", "TYPE", "must be an object")
		} else {
			v.validateHealth(healthObj, "health")
		}
	}
}

func (v *manifestSchemaValidator) validateTool(toolObj map[string]any) {
	name, ok := v.requireString(toolObj, "name", "tool.name")
	if ok && strings.TrimSpace(name) == "" {
		v.add("tool.name", "MIN_LENGTH", "must not be empty")
	}

	v.optionalString(toolObj, "version", "tool.version")
	v.optionalString(toolObj, "description", "tool.description")
	v.optionalString(toolObj, "author", "tool.author")
	v.optionalString(toolObj, "license", "tool.license")
	v.optionalString(toolObj, "homepage", "tool.homepage")

	if tagsRaw, ok := toolObj["tags"]; ok {
		tags, ok := tagsRaw.([]any)
		if !ok {
			v.add("tool.tags", "TYPE", "must be an array of strings")
		} else {
			for i, tag := range tags {
				if _, ok := tag.(string); !ok {
					v.add(fmt.Sprintf("tool.tags[%d]", i), "TYPE", "must be a string")
				}
			}
		}
	}
}

func (v *manifestSchemaValidator) validateTransport(obj map[string]any) {
	transportType, ok := v.requireString(obj, "type", "transport.type")
	if !ok {
		return
	}

	switch TransportType(transportType) {
	case TransportTypeNative, TransportTypeHTTP, TransportTypeStdio, TransportTypeMCP:
	default:
		v.add("transport.type", "ENUM", "must be one of: native, http, stdio, mcp")
	}

	if timeoutRaw, ok := obj["timeout_ms"]; ok {
		timeout, ok := asNonNegativeInt(timeoutRaw)
		if !ok {
			v.add("transport.timeout_ms", "TYPE", "must be a non-negative integer")
		} else if timeout < 0 {
			v.add("transport.timeout_ms", "MIN_VALUE", "must be >= 0")
		}
	}

	if endpointRaw, ok := obj["endpoint"]; ok {
		if _, ok := endpointRaw.(string); !ok {
			v.add("transport.endpoint", "TYPE", "must be a string")
		}
	}
	if commandRaw, ok := obj["command"]; ok {
		if _, ok := commandRaw.(string); !ok {
			v.add("transport.command", "TYPE", "must be a string")
		}
	}
	if argsRaw, ok := obj["args"]; ok {
		args, ok := argsRaw.([]any)
		if !ok {
			v.add("transport.args", "TYPE", "must be an array of strings")
		} else {
			for i, arg := range args {
				if _, ok := arg.(string); !ok {
					v.add(fmt.Sprintf("transport.args[%d]", i), "TYPE", "must be a string")
				}
			}
		}
	}
	if envRaw, ok := obj["env"]; ok {
		env, ok := envRaw.(map[string]any)
		if !ok {
			v.add("transport.env", "TYPE", "must be an object of string values")
		} else {
			for key, value := range env {
				if _, ok := value.(string); !ok {
					v.add("transport.env."+key, "TYPE", "must be a string")
				}
			}
		}
	}

	if retryRaw, ok := obj["retry"]; ok {
		retryObj, ok := retryRaw.(map[string]any)
		if !ok {
			v.add("transport.retry", "TYPE", "must be an object")
		} else {
			v.validateRetry(retryObj, "transport.retry")
		}
	}

	switch TransportType(transportType) {
	case TransportTypeNative:
		// Native tools are invoked in-process and require no transport endpoint fields.
	case TransportTypeHTTP:
		endpoint, ok := v.requireString(obj, "endpoint", "transport.endpoint")
		if ok && strings.TrimSpace(endpoint) == "" {
			v.add("transport.endpoint", "MIN_LENGTH", "must not be empty for http transport")
		}
	case TransportTypeStdio:
		command, ok := v.requireString(obj, "command", "transport.command")
		if ok && strings.TrimSpace(command) == "" {
			v.add("transport.command", "MIN_LENGTH", "must not be empty for stdio transport")
		}
	case TransportTypeMCP:
		mode, ok := v.requireString(obj, "mode", "transport.mode")
		if !ok {
			return
		}
		switch MCPMode(mode) {
		case MCPModeStdio:
			command, ok := v.requireString(obj, "command", "transport.command")
			if ok && strings.TrimSpace(command) == "" {
				v.add("transport.command", "MIN_LENGTH", "must not be empty for mcp stdio mode")
			}
		case MCPModeSSE:
			endpoint, ok := v.requireString(obj, "endpoint", "transport.endpoint")
			if ok && strings.TrimSpace(endpoint) == "" {
				v.add("transport.endpoint", "MIN_LENGTH", "must not be empty for mcp sse mode")
			}
		default:
			v.add("transport.mode", "ENUM", "must be one of: stdio, sse")
		}
	}
}

func (v *manifestSchemaValidator) validateRetry(obj map[string]any, path string) {
	if value, ok := obj["max_attempts"]; ok {
		if _, ok := asNonNegativeInt(value); !ok {
			v.add(path+".max_attempts", "TYPE", "must be a non-negative integer")
		}
	}
	if value, ok := obj["backoff_ms"]; ok {
		if _, ok := asNonNegativeInt(value); !ok {
			v.add(path+".backoff_ms", "TYPE", "must be a non-negative integer")
		}
	}
	if value, ok := obj["retryable_codes"]; ok {
		codes, ok := value.([]any)
		if !ok {
			v.add(path+".retryable_codes", "TYPE", "must be an array of integers")
		} else {
			for i, code := range codes {
				if _, ok := asInteger(code); !ok {
					v.add(fmt.Sprintf("%s.retryable_codes[%d]", path, i), "TYPE", "must be an integer")
				}
			}
		}
	}
}

func (v *manifestSchemaValidator) validateActions(actions map[string]any, path string) {
	if len(actions) == 0 {
		v.add(path, "REQUIRED", "must define at least one action")
	}
	for actionName, actionRaw := range actions {
		actionPath := path + "." + actionName
		actionObj, ok := actionRaw.(map[string]any)
		if !ok {
			v.add(actionPath, "TYPE", "must be an object")
			continue
		}
		v.validateAction(actionObj, actionPath)
	}
}

func (v *manifestSchemaValidator) validateAction(action map[string]any, path string) {
	if value, ok := action["description"]; ok {
		if _, ok := value.(string); !ok {
			v.add(path+".description", "TYPE", "must be a string")
		}
	}
	if value, ok := action["idempotent"]; ok {
		if _, ok := value.(bool); !ok {
			v.add(path+".idempotent", "TYPE", "must be a boolean")
		}
	}

	if value, ok := action["inputs"]; ok {
		inputs, ok := value.(map[string]any)
		if !ok {
			v.add(path+".inputs", "TYPE", "must be an object")
		} else {
			v.validateFieldMap(inputs, path+".inputs")
		}
	}
	if value, ok := action["outputs"]; ok {
		outputs, ok := value.(map[string]any)
		if !ok {
			v.add(path+".outputs", "TYPE", "must be an object")
		} else {
			v.validateFieldMap(outputs, path+".outputs")
		}
	}
}

func (v *manifestSchemaValidator) validateFieldMap(fields map[string]any, path string) {
	for key, rawSpec := range fields {
		specPath := path + "." + key
		specObj, ok := rawSpec.(map[string]any)
		if !ok {
			v.add(specPath, "TYPE", "must be an object")
			continue
		}
		v.validateFieldSpec(specObj, specPath)
	}
}

func (v *manifestSchemaValidator) validateFieldSpec(spec map[string]any, path string) {
	fieldType, ok := v.requireString(spec, "type", path+".type")
	if !ok {
		return
	}
	if _, ok := allowedFieldTypes[fieldType]; !ok {
		v.add(path+".type", "ENUM", "must be one of: string, integer, float, boolean, bytes, array, object, any")
	}

	if value, ok := spec["required"]; ok {
		if _, ok := value.(bool); !ok {
			v.add(path+".required", "TYPE", "must be a boolean")
		}
	}
	if value, ok := spec["description"]; ok {
		if _, ok := value.(string); !ok {
			v.add(path+".description", "TYPE", "must be a string")
		}
	}
	if value, ok := spec["sensitive"]; ok {
		if _, ok := value.(bool); !ok {
			v.add(path+".sensitive", "TYPE", "must be a boolean")
		}
	}

	if value, ok := spec["items"]; ok {
		itemObj, ok := value.(map[string]any)
		if !ok {
			v.add(path+".items", "TYPE", "must be an object")
		} else {
			v.validateFieldSpec(itemObj, path+".items")
		}
	}
	if fieldType == "array" {
		if _, ok := spec["items"]; !ok {
			v.add(path+".items", "REQUIRED", "is required when type is array")
		}
	}

	if value, ok := spec["properties"]; ok {
		propsObj, ok := value.(map[string]any)
		if !ok {
			v.add(path+".properties", "TYPE", "must be an object")
		} else {
			v.validateFieldMap(propsObj, path+".properties")
		}
	}
}

func (v *manifestSchemaValidator) validateHealth(health map[string]any, path string) {
	if value, ok := health["endpoint"]; ok {
		if _, ok := value.(string); !ok {
			v.add(path+".endpoint", "TYPE", "must be a string")
		}
	}
	if value, ok := health["method"]; ok {
		if _, ok := value.(string); !ok {
			v.add(path+".method", "TYPE", "must be a string")
		}
	}

	intFields := []string{"interval_seconds", "timeout_ms", "unhealthy_threshold"}
	for _, field := range intFields {
		if value, ok := health[field]; ok {
			if _, ok := asNonNegativeInt(value); !ok {
				v.add(path+"."+field, "TYPE", "must be a non-negative integer")
			}
		}
	}
}

func (v *manifestSchemaValidator) requireObject(obj map[string]any, key, path string) (map[string]any, bool) {
	value, ok := obj[key]
	if !ok {
		v.add(path, "REQUIRED", "is required")
		return nil, false
	}
	next, ok := value.(map[string]any)
	if !ok {
		v.add(path, "TYPE", "must be an object")
		return nil, false
	}
	return next, true
}

func (v *manifestSchemaValidator) requireString(obj map[string]any, key, path string) (string, bool) {
	value, ok := obj[key]
	if !ok {
		v.add(path, "REQUIRED", "is required")
		return "", false
	}
	str, ok := value.(string)
	if !ok {
		v.add(path, "TYPE", "must be a string")
		return "", false
	}
	return str, true
}

func (v *manifestSchemaValidator) optionalString(obj map[string]any, key, path string) {
	value, ok := obj[key]
	if !ok {
		return
	}
	if _, ok := value.(string); !ok {
		v.add(path, "TYPE", "must be a string")
	}
}

func (v *manifestSchemaValidator) add(field, code, message string) {
	v.diags = append(v.diags, Diagnostic{
		Field:    field,
		Code:     code,
		Severity: SeverityError,
		Message:  message,
	})
}

func readString(obj map[string]any, key string) (string, bool) {
	value, ok := obj[key]
	if !ok {
		return "", false
	}
	str, ok := value.(string)
	return str, ok
}

func asNonNegativeInt(value any) (int64, bool) {
	i, ok := asInteger(value)
	if !ok || i < 0 {
		return 0, false
	}
	return i, true
}

func asInteger(value any) (int64, bool) {
	switch n := value.(type) {
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return i, true
		}
		// Fall back to float parse to reject non-integers deterministically.
		f, err := strconv.ParseFloat(string(n), 64)
		if err != nil {
			return 0, false
		}
		if f != float64(int64(f)) {
			return 0, false
		}
		return int64(f), true
	case float64:
		if n != float64(int64(n)) {
			return 0, false
		}
		return int64(n), true
	case float32:
		if n != float32(int64(n)) {
			return 0, false
		}
		return int64(n), true
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return asInteger(uint64(n))
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		if n > maxInt64AsUint64 {
			return 0, false
		}
		return int64(n), true
	default:
		return 0, false
	}
}

// SchemaManifestValidator validates manifests using the v1 schema helper.
type SchemaManifestValidator struct{}

// ValidateManifest validates a manifest and returns schema diagnostics.
func (SchemaManifestValidator) ValidateManifest(manifest Manifest) []Diagnostic {
	data, err := json.Marshal(manifest)
	if err != nil {
		return []Diagnostic{{
			Field:    "$",
			Code:     "INVALID_MANIFEST",
			Severity: SeverityError,
			Message:  fmt.Sprintf("Failed to serialize manifest: %v", err),
		}}
	}
	return ValidateManifestJSON(data).Diagnostics
}
