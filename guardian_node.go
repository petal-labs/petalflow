package petalflow

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// GuardianCheckType specifies the type of validation check.
type GuardianCheckType string

const (
	// GuardianCheckRequired validates that required fields are present and non-empty.
	GuardianCheckRequired GuardianCheckType = "required"

	// GuardianCheckMaxLength validates maximum string/slice length.
	GuardianCheckMaxLength GuardianCheckType = "max_length"

	// GuardianCheckMinLength validates minimum string/slice length.
	GuardianCheckMinLength GuardianCheckType = "min_length"

	// GuardianCheckPattern validates string matches a regex pattern.
	GuardianCheckPattern GuardianCheckType = "pattern"

	// GuardianCheckEnum validates value is in an allowed set.
	GuardianCheckEnum GuardianCheckType = "enum"

	// GuardianCheckType_ validates value is of expected type.
	GuardianCheckType_ GuardianCheckType = "type"

	// GuardianCheckRange validates numeric value is within range.
	GuardianCheckRange GuardianCheckType = "range"

	// GuardianCheckPII detects potential PII (SSN, email, phone, etc.).
	GuardianCheckPII GuardianCheckType = "pii"

	// GuardianCheckSchema validates against a JSON schema (simplified).
	GuardianCheckSchema GuardianCheckType = "schema"

	// GuardianCheckCustom uses a custom validation function.
	GuardianCheckCustom GuardianCheckType = "custom"
)

// GuardianAction defines what happens when validation fails.
type GuardianAction string

const (
	// GuardianActionFail stops execution with an error.
	GuardianActionFail GuardianAction = "fail"

	// GuardianActionSkip passes through without modification.
	GuardianActionSkip GuardianAction = "skip"

	// GuardianActionRedirect routes to a specific node on failure.
	GuardianActionRedirect GuardianAction = "redirect"
)

// PIIType identifies types of personally identifiable information.
type PIIType string

const (
	PIITypeSSN         PIIType = "ssn"
	PIITypeEmail       PIIType = "email"
	PIITypePhone       PIIType = "phone"
	PIITypeCreditCard  PIIType = "credit_card"
	PIITypeIPAddress   PIIType = "ip_address"
	PIITypeDateOfBirth PIIType = "date_of_birth"
)

// GuardianCheck defines a single validation check.
type GuardianCheck struct {
	// Name identifies this check in error messages.
	Name string

	// Type specifies the check type.
	Type GuardianCheckType

	// Field is the field path to validate (dot notation supported).
	// If empty, validates the entire input variable.
	Field string

	// Required fields for GuardianCheckRequired.
	RequiredFields []string

	// MaxLength for GuardianCheckMaxLength.
	MaxLength int

	// MinLength for GuardianCheckMinLength.
	MinLength int

	// Pattern is a regex for GuardianCheckPattern.
	Pattern string

	// AllowedValues for GuardianCheckEnum.
	AllowedValues []any

	// ExpectedType for GuardianCheckType_ ("string", "number", "bool", "array", "object").
	ExpectedType string

	// Min/Max for GuardianCheckRange.
	Min *float64
	Max *float64

	// PIITypes to detect for GuardianCheckPII.
	// If empty, checks all PII types.
	PIITypes []PIIType

	// BlockPII determines if PII detection should fail (true) or just warn (false).
	BlockPII bool

	// Schema for GuardianCheckSchema (simplified JSON schema).
	Schema map[string]any

	// CustomFunc for GuardianCheckCustom.
	// Returns (passed, message, error).
	CustomFunc func(ctx context.Context, value any, env *Envelope) (bool, string, error)

	// Message is a custom error message for this check.
	Message string
}

// GuardianFailure describes a validation failure.
type GuardianFailure struct {
	CheckName string `json:"check_name"`
	CheckType string `json:"check_type"`
	Field     string `json:"field,omitempty"`
	Message   string `json:"message"`
	Expected  any    `json:"expected,omitempty"`
	Actual    any    `json:"actual,omitempty"`
	PIIType   string `json:"pii_type,omitempty"`
}

// GuardianResult is stored in the envelope when ResultVar is set.
type GuardianResult struct {
	Passed   bool              `json:"passed"`
	Failures []GuardianFailure `json:"failures,omitempty"`
}

// GuardianNodeConfig configures a GuardianNode.
type GuardianNodeConfig struct {
	// InputVar specifies the variable to validate (dot notation supported).
	// If empty, validates the entire Vars map.
	InputVar string

	// Checks is a list of validation checks to perform (executed in order).
	Checks []GuardianCheck

	// OnFail determines behavior when validation fails.
	// Defaults to GuardianActionFail.
	OnFail GuardianAction

	// FailMessage is the error message when OnFail is GuardianActionFail.
	FailMessage string

	// RedirectNodeID is the target when OnFail is GuardianActionRedirect.
	RedirectNodeID string

	// ResultVar stores the validation result (GuardianResult).
	ResultVar string

	// StopOnFirstFailure stops checking after the first failure.
	// Default is false (run all checks).
	StopOnFirstFailure bool
}

// GuardianNode enforces invariants and validates data.
// It acts as "CI checks" inside a workflow - validating schemas,
// required fields, patterns, and detecting PII.
type GuardianNode struct {
	BaseNode
	config GuardianNodeConfig
}

// NewGuardianNode creates a new GuardianNode with the given configuration.
func NewGuardianNode(id string, config GuardianNodeConfig) *GuardianNode {
	// Set defaults
	if config.OnFail == "" {
		config.OnFail = GuardianActionFail
	}
	if config.FailMessage == "" {
		config.FailMessage = "validation failed"
	}

	return &GuardianNode{
		BaseNode: NewBaseNode(id, NodeKindGuardian),
		config:   config,
	}
}

// Config returns the node's configuration.
func (n *GuardianNode) Config() GuardianNodeConfig {
	return n.config
}

// Run executes the validation checks.
func (n *GuardianNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	result := env.Clone()

	// Get the input to validate
	var input any
	if n.config.InputVar != "" {
		val, ok := env.GetVarNested(n.config.InputVar)
		if !ok {
			return nil, fmt.Errorf("guardian node %s: variable %q not found", n.id, n.config.InputVar)
		}
		input = val
	} else {
		input = env.Vars
	}

	// Run all checks
	var failures []GuardianFailure
	for _, check := range n.config.Checks {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		checkFailures, err := n.runCheck(ctx, check, input, env)
		if err != nil {
			return nil, fmt.Errorf("guardian node %s: check %q error: %w", n.id, check.Name, err)
		}

		failures = append(failures, checkFailures...)

		if n.config.StopOnFirstFailure && len(failures) > 0 {
			break
		}
	}

	passed := len(failures) == 0

	// Store result if configured
	if n.config.ResultVar != "" {
		result.SetVar(n.config.ResultVar, GuardianResult{
			Passed:   passed,
			Failures: failures,
		})
	}

	// Handle pass case
	if passed {
		return result, nil
	}

	// Handle failure based on configured action
	switch n.config.OnFail {
	case GuardianActionFail:
		// Build detailed error message
		var msgs []string
		for _, f := range failures {
			msgs = append(msgs, f.Message)
		}
		return nil, fmt.Errorf("guardian node %s: %s: %s", n.id, n.config.FailMessage, strings.Join(msgs, "; "))

	case GuardianActionSkip:
		return result, nil

	case GuardianActionRedirect:
		if n.config.RedirectNodeID == "" {
			return nil, fmt.Errorf("guardian node %s: redirect action requires RedirectNodeID", n.id)
		}
		result.SetVar("__guardian_redirect__", n.config.RedirectNodeID)
		return result, nil

	default:
		return nil, fmt.Errorf("guardian node %s: unknown action %q", n.id, n.config.OnFail)
	}
}

// runCheck executes a single validation check.
func (n *GuardianNode) runCheck(ctx context.Context, check GuardianCheck, input any, env *Envelope) ([]GuardianFailure, error) {
	// Get the value to check
	var value any
	if check.Field != "" {
		inputMap, ok := toMap(input)
		if !ok {
			return []GuardianFailure{{
				CheckName: check.Name,
				CheckType: string(check.Type),
				Field:     check.Field,
				Message:   fmt.Sprintf("cannot access field %q on non-map input", check.Field),
			}}, nil
		}
		val, found := getNestedValue(inputMap, check.Field)
		if !found {
			// Field not found - may be okay for some checks
			if check.Type == GuardianCheckRequired {
				return []GuardianFailure{{
					CheckName: check.Name,
					CheckType: string(check.Type),
					Field:     check.Field,
					Message:   n.getMessage(check, fmt.Sprintf("required field %q is missing", check.Field)),
				}}, nil
			}
			// For other checks, missing field means nothing to validate
			return nil, nil
		}
		value = val
	} else {
		value = input
	}

	switch check.Type {
	case GuardianCheckRequired:
		return n.checkRequired(check, value, input)
	case GuardianCheckMaxLength:
		return n.checkMaxLength(check, value)
	case GuardianCheckMinLength:
		return n.checkMinLength(check, value)
	case GuardianCheckPattern:
		return n.checkPattern(check, value)
	case GuardianCheckEnum:
		return n.checkEnum(check, value)
	case GuardianCheckType_:
		return n.checkType(check, value)
	case GuardianCheckRange:
		return n.checkRange(check, value)
	case GuardianCheckPII:
		return n.checkPII(check, value)
	case GuardianCheckSchema:
		return n.checkSchema(check, value)
	case GuardianCheckCustom:
		return n.checkCustom(ctx, check, value, env)
	default:
		return nil, fmt.Errorf("unknown check type: %s", check.Type)
	}
}

// checkRequired validates required fields are present.
func (n *GuardianNode) checkRequired(check GuardianCheck, value any, input any) ([]GuardianFailure, error) {
	var failures []GuardianFailure

	// If RequiredFields is specified, check those fields
	if len(check.RequiredFields) > 0 {
		inputMap, ok := toMap(input)
		if !ok {
			return []GuardianFailure{{
				CheckName: check.Name,
				CheckType: string(check.Type),
				Message:   "required fields check needs map input",
			}}, nil
		}

		for _, field := range check.RequiredFields {
			val, found := getNestedValue(inputMap, field)
			if !found || isEmpty(val) {
				failures = append(failures, GuardianFailure{
					CheckName: check.Name,
					CheckType: string(check.Type),
					Field:     field,
					Message:   n.getMessage(check, fmt.Sprintf("required field %q is missing or empty", field)),
				})
			}
		}
		return failures, nil
	}

	// Otherwise check if the value itself is empty
	if isEmpty(value) {
		return []GuardianFailure{{
			CheckName: check.Name,
			CheckType: string(check.Type),
			Field:     check.Field,
			Message:   n.getMessage(check, fmt.Sprintf("field %q is required but empty", check.Field)),
		}}, nil
	}

	return nil, nil
}

// checkMaxLength validates maximum length.
func (n *GuardianNode) checkMaxLength(check GuardianCheck, value any) ([]GuardianFailure, error) {
	length := getLength(value)
	if length > check.MaxLength {
		return []GuardianFailure{{
			CheckName: check.Name,
			CheckType: string(check.Type),
			Field:     check.Field,
			Message:   n.getMessage(check, fmt.Sprintf("length %d exceeds maximum %d", length, check.MaxLength)),
			Expected:  check.MaxLength,
			Actual:    length,
		}}, nil
	}
	return nil, nil
}

// checkMinLength validates minimum length.
func (n *GuardianNode) checkMinLength(check GuardianCheck, value any) ([]GuardianFailure, error) {
	length := getLength(value)
	if length < check.MinLength {
		return []GuardianFailure{{
			CheckName: check.Name,
			CheckType: string(check.Type),
			Field:     check.Field,
			Message:   n.getMessage(check, fmt.Sprintf("length %d is below minimum %d", length, check.MinLength)),
			Expected:  check.MinLength,
			Actual:    length,
		}}, nil
	}
	return nil, nil
}

// checkPattern validates string matches pattern.
func (n *GuardianNode) checkPattern(check GuardianCheck, value any) ([]GuardianFailure, error) {
	if check.Pattern == "" {
		return nil, fmt.Errorf("pattern check requires Pattern")
	}

	re, err := regexp.Compile(check.Pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern %q: %w", check.Pattern, err)
	}

	str := fmt.Sprintf("%v", value)
	if !re.MatchString(str) {
		return []GuardianFailure{{
			CheckName: check.Name,
			CheckType: string(check.Type),
			Field:     check.Field,
			Message:   n.getMessage(check, fmt.Sprintf("value %q does not match pattern %q", str, check.Pattern)),
			Expected:  check.Pattern,
			Actual:    str,
		}}, nil
	}
	return nil, nil
}

// checkEnum validates value is in allowed set.
func (n *GuardianNode) checkEnum(check GuardianCheck, value any) ([]GuardianFailure, error) {
	if len(check.AllowedValues) == 0 {
		return nil, fmt.Errorf("enum check requires AllowedValues")
	}

	for _, allowed := range check.AllowedValues {
		if valuesEqual(value, allowed) {
			return nil, nil
		}
	}

	return []GuardianFailure{{
		CheckName: check.Name,
		CheckType: string(check.Type),
		Field:     check.Field,
		Message:   n.getMessage(check, fmt.Sprintf("value %v is not in allowed values %v", value, check.AllowedValues)),
		Expected:  check.AllowedValues,
		Actual:    value,
	}}, nil
}

// checkType validates value is of expected type.
func (n *GuardianNode) checkType(check GuardianCheck, value any) ([]GuardianFailure, error) {
	if check.ExpectedType == "" {
		return nil, fmt.Errorf("type check requires ExpectedType")
	}

	actualType := getTypeString(value)
	if actualType != check.ExpectedType {
		return []GuardianFailure{{
			CheckName: check.Name,
			CheckType: string(check.Type),
			Field:     check.Field,
			Message:   n.getMessage(check, fmt.Sprintf("expected type %q, got %q", check.ExpectedType, actualType)),
			Expected:  check.ExpectedType,
			Actual:    actualType,
		}}, nil
	}
	return nil, nil
}

// checkRange validates numeric value is within range.
func (n *GuardianNode) checkRange(check GuardianCheck, value any) ([]GuardianFailure, error) {
	num, ok := toFloat64(value)
	if !ok {
		return []GuardianFailure{{
			CheckName: check.Name,
			CheckType: string(check.Type),
			Field:     check.Field,
			Message:   n.getMessage(check, fmt.Sprintf("range check requires numeric value, got %T", value)),
		}}, nil
	}

	if check.Min != nil && num < *check.Min {
		return []GuardianFailure{{
			CheckName: check.Name,
			CheckType: string(check.Type),
			Field:     check.Field,
			Message:   n.getMessage(check, fmt.Sprintf("value %v is below minimum %v", num, *check.Min)),
			Expected:  *check.Min,
			Actual:    num,
		}}, nil
	}

	if check.Max != nil && num > *check.Max {
		return []GuardianFailure{{
			CheckName: check.Name,
			CheckType: string(check.Type),
			Field:     check.Field,
			Message:   n.getMessage(check, fmt.Sprintf("value %v exceeds maximum %v", num, *check.Max)),
			Expected:  *check.Max,
			Actual:    num,
		}}, nil
	}

	return nil, nil
}

// PII detection patterns
var piiPatterns = map[PIIType]*regexp.Regexp{
	PIITypeSSN:         regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
	PIITypeEmail:       regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`),
	PIITypePhone:       regexp.MustCompile(`\b(?:\+?1[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}\b`),
	PIITypeCreditCard:  regexp.MustCompile(`\b(?:\d{4}[-\s]?){3}\d{4}\b`),
	PIITypeIPAddress:   regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
	PIITypeDateOfBirth: regexp.MustCompile(`\b(?:0[1-9]|1[0-2])[/-](?:0[1-9]|[12]\d|3[01])[/-](?:19|20)\d{2}\b`),
}

// checkPII detects potential PII in the value.
func (n *GuardianNode) checkPII(check GuardianCheck, value any) ([]GuardianFailure, error) {
	// Convert value to string for scanning
	str := stringify(value)

	// Determine which PII types to check
	typesToCheck := check.PIITypes
	if len(typesToCheck) == 0 {
		// Check all types
		typesToCheck = []PIIType{
			PIITypeSSN,
			PIITypeEmail,
			PIITypePhone,
			PIITypeCreditCard,
			PIITypeIPAddress,
			PIITypeDateOfBirth,
		}
	}

	var failures []GuardianFailure
	for _, piiType := range typesToCheck {
		pattern, ok := piiPatterns[piiType]
		if !ok {
			continue
		}

		if pattern.MatchString(str) {
			if check.BlockPII {
				failures = append(failures, GuardianFailure{
					CheckName: check.Name,
					CheckType: string(check.Type),
					Field:     check.Field,
					Message:   n.getMessage(check, fmt.Sprintf("potential %s PII detected", piiType)),
					PIIType:   string(piiType),
				})
			}
		}
	}

	return failures, nil
}

// checkSchema validates against a simplified JSON schema.
func (n *GuardianNode) checkSchema(check GuardianCheck, value any) ([]GuardianFailure, error) {
	if check.Schema == nil {
		return nil, fmt.Errorf("schema check requires Schema")
	}

	failures := validateSchema(check.Schema, value, check.Field, check.Name)
	return failures, nil
}

// validateSchema performs simplified JSON schema validation.
func validateSchema(schema map[string]any, value any, path string, checkName string) []GuardianFailure {
	var failures []GuardianFailure

	// Check type
	if expectedType, ok := schema["type"].(string); ok {
		actualType := getTypeString(value)
		if actualType != expectedType {
			failures = append(failures, GuardianFailure{
				CheckName: checkName,
				CheckType: string(GuardianCheckSchema),
				Field:     path,
				Message:   fmt.Sprintf("expected type %q, got %q", expectedType, actualType),
				Expected:  expectedType,
				Actual:    actualType,
			})
			return failures // Type mismatch, skip other checks
		}
	}

	// Check required properties (for objects)
	if required, ok := schema["required"].([]any); ok {
		if objMap, ok := toMap(value); ok {
			for _, r := range required {
				reqField := fmt.Sprintf("%v", r)
				if _, found := objMap[reqField]; !found {
					fieldPath := reqField
					if path != "" {
						fieldPath = path + "." + reqField
					}
					failures = append(failures, GuardianFailure{
						CheckName: checkName,
						CheckType: string(GuardianCheckSchema),
						Field:     fieldPath,
						Message:   fmt.Sprintf("required property %q is missing", reqField),
					})
				}
			}
		}
	}

	// Check properties (for objects)
	if properties, ok := schema["properties"].(map[string]any); ok {
		if objMap, ok := toMap(value); ok {
			for propName, propSchema := range properties {
				propSchemaMap, ok := propSchema.(map[string]any)
				if !ok {
					continue
				}
				if propValue, found := objMap[propName]; found {
					propPath := propName
					if path != "" {
						propPath = path + "." + propName
					}
					failures = append(failures, validateSchema(propSchemaMap, propValue, propPath, checkName)...)
				}
			}
		}
	}

	// Check minLength/maxLength (for strings)
	if str, ok := value.(string); ok {
		if minLen, ok := schema["minLength"].(float64); ok {
			if len(str) < int(minLen) {
				failures = append(failures, GuardianFailure{
					CheckName: checkName,
					CheckType: string(GuardianCheckSchema),
					Field:     path,
					Message:   fmt.Sprintf("string length %d is below minimum %d", len(str), int(minLen)),
				})
			}
		}
		if maxLen, ok := schema["maxLength"].(float64); ok {
			if len(str) > int(maxLen) {
				failures = append(failures, GuardianFailure{
					CheckName: checkName,
					CheckType: string(GuardianCheckSchema),
					Field:     path,
					Message:   fmt.Sprintf("string length %d exceeds maximum %d", len(str), int(maxLen)),
				})
			}
		}
		if pattern, ok := schema["pattern"].(string); ok {
			re, err := regexp.Compile(pattern)
			if err == nil && !re.MatchString(str) {
				failures = append(failures, GuardianFailure{
					CheckName: checkName,
					CheckType: string(GuardianCheckSchema),
					Field:     path,
					Message:   fmt.Sprintf("string does not match pattern %q", pattern),
				})
			}
		}
	}

	// Check minimum/maximum (for numbers)
	if num, ok := toFloat64(value); ok {
		if min, ok := schema["minimum"].(float64); ok {
			if num < min {
				failures = append(failures, GuardianFailure{
					CheckName: checkName,
					CheckType: string(GuardianCheckSchema),
					Field:     path,
					Message:   fmt.Sprintf("value %v is below minimum %v", num, min),
				})
			}
		}
		if max, ok := schema["maximum"].(float64); ok {
			if num > max {
				failures = append(failures, GuardianFailure{
					CheckName: checkName,
					CheckType: string(GuardianCheckSchema),
					Field:     path,
					Message:   fmt.Sprintf("value %v exceeds maximum %v", num, max),
				})
			}
		}
	}

	// Check enum
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, allowed := range enum {
			if valuesEqual(value, allowed) {
				found = true
				break
			}
		}
		if !found {
			failures = append(failures, GuardianFailure{
				CheckName: checkName,
				CheckType: string(GuardianCheckSchema),
				Field:     path,
				Message:   fmt.Sprintf("value %v is not in enum %v", value, enum),
			})
		}
	}

	return failures
}

// checkCustom runs a custom validation function.
func (n *GuardianNode) checkCustom(ctx context.Context, check GuardianCheck, value any, env *Envelope) ([]GuardianFailure, error) {
	if check.CustomFunc == nil {
		return nil, fmt.Errorf("custom check requires CustomFunc")
	}

	passed, message, err := check.CustomFunc(ctx, value, env)
	if err != nil {
		return nil, err
	}

	if !passed {
		if message == "" {
			message = "custom validation failed"
		}
		return []GuardianFailure{{
			CheckName: check.Name,
			CheckType: string(check.Type),
			Field:     check.Field,
			Message:   n.getMessage(check, message),
		}}, nil
	}

	return nil, nil
}

// getMessage returns the check's custom message or the default.
func (n *GuardianNode) getMessage(check GuardianCheck, defaultMsg string) string {
	if check.Message != "" {
		return check.Message
	}
	return defaultMsg
}

// isEmpty checks if a value is empty/nil/zero.
func isEmpty(v any) bool {
	if v == nil {
		return true
	}

	switch val := v.(type) {
	case string:
		return val == ""
	case []any:
		return len(val) == 0
	case map[string]any:
		return len(val) == 0
	case bool:
		return false // bools are never "empty"
	case int, int64, float64:
		return false // numbers are never "empty"
	default:
		// Use reflection for other types
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Slice, reflect.Map, reflect.Array:
			return rv.Len() == 0
		case reflect.Ptr, reflect.Interface:
			return rv.IsNil()
		default:
			return false
		}
	}
}

// getLength returns the length of a string or slice.
func getLength(v any) int {
	switch val := v.(type) {
	case string:
		return len(val)
	case []any:
		return len(val)
	case []string:
		return len(val)
	case []int:
		return len(val)
	case map[string]any:
		return len(val)
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
			return rv.Len()
		default:
			return 0
		}
	}
}

// getTypeString returns a JSON-schema-like type string.
func getTypeString(v any) string {
	if v == nil {
		return "null"
	}

	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case int, int32, int64, float32, float64:
		return "number"
	case []any, []string, []int, []float64:
		return "array"
	case map[string]any:
		return "object"
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.String:
			return "string"
		case reflect.Bool:
			return "bool"
		case reflect.Int, reflect.Int32, reflect.Int64, reflect.Float32, reflect.Float64:
			return "number"
		case reflect.Slice, reflect.Array:
			return "array"
		case reflect.Map:
			return "object"
		default:
			return "unknown"
		}
	}
}

// stringify converts a value to a string for scanning.
func stringify(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	}
}

// Ensure interface compliance at compile time.
var _ Node = (*GuardianNode)(nil)
