package tool

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Payload validation errors and size limits for tool invocations. Limits
// are enforced on the invocation path before any side effect (inputs) and
// before results are handed back (outputs).
var (
	// ErrInvalidPayload marks invocation payloads that do not satisfy
	// the registered schema penalty (missing required fields, type
	// mismatches). Error messages name fields and expected types only —
	// never the payload contents.
	ErrInvalidPayload = errors.New("tool: payload does not satisfy the registered schema")
	// ErrPayloadTooLarge marks payloads over the configured size limit.
	ErrPayloadTooLarge = errors.New("tool: payload exceeds configured size limit")
)

// Additional invocation error codes.
const (
	// ToolErrorCodeInvalidPayload is returned when a payload violates the
	// registered input/output schema.
	ToolErrorCodeInvalidPayload = "INVALID_PAYLOAD"
	// ToolErrorCodePayloadTooLarge is returned when a payload exceeds the
	// configured size limit.
	ToolErrorCodePayloadTooLarge = "PAYLOAD_TOO_LARGE"
)

// PayloadLimits bounds the size of tool invocation payloads. Zero values
// apply the documented defaults; negative values are malformed.
type PayloadLimits struct {
	// MaxInputBytes caps the JSON-serialized inputs of one invocation.
	MaxInputBytes int
	// MaxOutputBytes caps the JSON-serialized outputs of one invocation.
	MaxOutputBytes int
}

// DefaultPayloadLimits are the documented out-of-the-box limits: inputs
// up to 4 MiB, outputs up to 16 MiB.
func DefaultPayloadLimits() PayloadLimits {
	return PayloadLimits{
		MaxInputBytes:  4 << 20,
		MaxOutputBytes: 16 << 20,
	}
}

// Validate rejects malformed limits.
func (p PayloadLimits) Validate() error {
	if p.MaxInputBytes < 0 || p.MaxOutputBytes < 0 {
		return errors.New("tool: payload limits are negative")
	}
	return nil
}

// resolve returns the effective limits with defaults applied for unset
// (zero) fields.
func (p PayloadLimits) resolve() PayloadLimits {
	defaults := DefaultPayloadLimits()
	if p.MaxInputBytes == 0 {
		p.MaxInputBytes = defaults.MaxInputBytes
	}
	if p.MaxOutputBytes == 0 {
		p.MaxOutputBytes = defaults.MaxOutputBytes
	}
	return p
}

const maxPayloadValidationDepth = 16

// ValidateInputs checks that the inputs satisfy the registered action
// schema (required fields present, declared-typed fields well-typed) and
// fit the input size limit before any adapter is constructed. Undeclared
// input keys are allowed: the manifest schema默认 declares what the tool
// consumes, and tools may accept extra values (e.g. template_render).
//
// Returning this error must never leak payload contents.
func ValidateInputs(reg Registration, action string, inputs map[string]any, limits PayloadLimits) error {
	specs, ok := actionSpec(reg.Manifest, action)
	if !ok {
		return fmt.Errorf("%w: action %q", ErrActionNotFound, action)
	}
	if enforced := limits.resolve(); enforced.MaxInputBytes > 0 {
		if raw, err := json.Marshal(inputs); err == nil && len(raw) > enforced.MaxInputBytes {
			return fmt.Errorf("%w: tool input is %d bytes, limit is %d", ErrPayloadTooLarge, len(raw), enforced.MaxInputBytes)
		}
	}

	var failures []string
	for _, name := range sortedFieldNames(specs.Inputs) {
		spec := specs.Inputs[name]
		value, present := inputs[name]
		if spec.Required {
			if !present || value == nil {
				failures = append(failures, fmt.Sprintf("required input %q is missing", name))
				continue
			}
		}
		if !present || value == nil {
			continue
		}
		if msg, invalid := validatePayloadValue(name, value, spec, 0); invalid {
			failures = append(failures, msg)
		}
	}
	if len(failures) > 0 {
		sortStrings(failures)
		return fmt.Errorf("%w: %s", ErrInvalidPayload, strings.Join(failures, "; "))
	}
	return nil
}

// ValidateOutputs checks tool outputs against the registered schema and
// the output size limit before they are handed to the caller. Undeclared
// output keys are allowed; declared outputs must be well-typed and
// required outputs must be present.
func ValidateOutputs(reg Registration, action string, outputs map[string]any, limits PayloadLimits) error {
	specs, ok := actionSpec(reg.Manifest, action)
	if !ok {
		return fmt.Errorf("%w: action %q", ErrActionNotFound, action)
	}
	if enforced := limits.resolve(); enforced.MaxOutputBytes > 0 {
		if raw, err := json.Marshal(outputs); err == nil && len(raw) > enforced.MaxOutputBytes {
			return fmt.Errorf("%w: tool output is %d bytes, limit is %d", ErrPayloadTooLarge, len(raw), enforced.MaxOutputBytes)
		}
	}

	var failures []string
	for _, name := range sortedFieldNames(specs.Outputs) {
		spec := specs.Outputs[name]
		value, present := outputs[name]
		if spec.Required {
			if !present || value == nil {
				failures = append(failures, fmt.Sprintf("required output %q is missing", name))
				continue
			}
		}
		if !present || value == nil {
			continue
		}
		if msg, invalid := validatePayloadValue(name, value, spec, 0); invalid {
			failures = append(failures, msg)
		}
	}
	if len(failures) > 0 {
		sortStrings(failures)
		return fmt.Errorf("%w: %s", ErrInvalidPayload, strings.Join(failures, "; "))
	}
	return nil
}

func actionSpec(manifest Manifest, action string) (ActionSpec, bool) {
	if manifest.Actions == nil {
		return ActionSpec{}, false
	}
	spec, ok := manifest.Actions[action]
	return spec, ok
}

// validatePayloadValue checks one declared field (recursively, with a
// depth cap so malformed nested values cannot drive unbounded
// traversal). The returned message names the field and expected type
// only — never the offending value.
func validatePayloadValue(name string, value any, spec FieldSpec, depth int) (string, bool) {
	if depth > maxPayloadValidationDepth {
		return fmt.Sprintf("field %q exceeds the nesting limit", name), true
	}
	declared := spec.Type
	if declared == "" {
		declared = TypeAny
	}
	switch declared {
	case TypeAny:
		return "", false
	case TypeString:
		if _, ok := value.(string); !ok {
			return typeMismatch(name, declared), true
		}
	case TypeBoolean:
		if _, ok := value.(bool); !ok {
			return typeMismatch(name, declared), true
		}
	case TypeInteger:
		if number, ok := value.(float64); ok {
			if number != float64(int64(number)) {
				return typeMismatch(name, declared), true
			}
			return "", false
		}
		if number, ok := value.(int); ok {
			_ = number
			return "", false
		}
		if number, ok := value.(int64); ok {
			_ = number
			return "", false
		}
		return typeMismatch(name, declared), true
	case TypeFloat:
		switch value.(type) {
		case float64, int, int64:
			return "", false
		}
		return typeMismatch(name, declared), true
	case TypeBytes:
		switch value.(type) {
		case []byte, string:
			return "", false
		}
		return typeMismatch(name, declared), true
	case TypeArray:
		items, ok := value.([]any)
		if !ok {
			return typeMismatch(name, declared), true
		}
		if spec.Items != nil {
			for i, item := range items {
				if msg, invalid := validatePayloadValue(fmt.Sprintf("%s[%d]", name, i), item, *spec.Items, depth+1); invalid {
					return msg, true
				}
			}
		}
	case TypeObject:
		properties, ok := value.(map[string]any)
		if !ok {
			return typeMismatch(name, declared), true
		}
		if len(spec.Properties) == 0 {
			return "", false
		}
		for _, propertyName := range sortedFieldNames(spec.Properties) {
			propertySpec := spec.Properties[propertyName]
			propertyValue, present := properties[propertyName]
			if propertySpec.Required {
				if !present || propertyValue == nil {
					return fmt.Sprintf("required field %q.%s is missing", name, propertyName), true
				}
			}
			if !present || propertyValue == nil {
				continue
			}
			if msg, invalid := validatePayloadValue(name+"."+propertyName, propertyValue, propertySpec, depth+1); invalid {
				return msg, true
			}
		}
	default:
		// Unknown declared types never hold against values; manifest
		// validation catches them at registration.
		return "", false
	}
	return "", false
}

func typeMismatch(name, declared string) string {
	return fmt.Sprintf("field %q must be of type %s", name, declared)
}

// InvalidPayloadErrorCode maps a payload rejection to its typed code.
func InvalidPayloadErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrPayloadTooLarge):
		return ToolErrorCodePayloadTooLarge
	case errors.Is(err, ErrInvalidPayload):
		return ToolErrorCodeInvalidPayload
	case errors.Is(err, ErrActionNotFound):
		return ToolErrorCodeActionNotFound
	default:
		return ""
	}
}

// invocationDeadline is the service-level wall clock for one tool
// invocation: the registration transport deadline plus a small grace so
// adapter-declared timeouts surface their own typed errors first. The
// service deadline is the uniform backstop for adapters without an
// internal timeout (native in-process tools, misbehaving daemons).
func invocationDeadline(reg Registration) time.Duration {
	base := timeoutFromRegistration(reg)
	if base <= 0 {
		base = defaultAdapterTimeout
	}
	return base + 5*time.Second
}

func sortStrings(values []string) {
	slices.Sort(values)
}
