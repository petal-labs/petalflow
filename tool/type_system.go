package tool

import (
	"fmt"
	"slices"
)

// V1 type system literals used by tool manifests.
const (
	TypeString  = "string"
	TypeInteger = "integer"
	TypeFloat   = "float"
	TypeBoolean = "boolean"
	TypeBytes   = "bytes"
	TypeArray   = "array"
	TypeObject  = "object"
	TypeAny     = "any"
)

var validV1Types = map[string]struct{}{
	TypeString:  {},
	TypeInteger: {},
	TypeFloat:   {},
	TypeBoolean: {},
	TypeBytes:   {},
	TypeArray:   {},
	TypeObject:  {},
	TypeAny:     {},
}

// V1TypeSystemValidator validates field declarations and type compatibility.
type V1TypeSystemValidator struct{}

// ValidateManifestTypes validates all type declarations in a manifest.
func (V1TypeSystemValidator) ValidateManifestTypes(manifest Manifest) []Diagnostic {
	diags := make([]Diagnostic, 0)

	for _, actionName := range sortedActionNames(manifest.Actions) {
		action := manifest.Actions[actionName]
		for _, inputName := range sortedFieldNames(action.Inputs) {
			validateFieldSpec("actions."+actionName+".inputs."+inputName, action.Inputs[inputName], &diags)
		}
		for _, outputName := range sortedFieldNames(action.Outputs) {
			validateFieldSpec("actions."+actionName+".outputs."+outputName, action.Outputs[outputName], &diags)
		}
	}

	for _, configName := range sortedFieldNames(manifest.Config) {
		validateFieldSpec("config."+configName, manifest.Config[configName], &diags)
	}

	return diags
}

// ValidateManifest satisfies ManifestValidator.
func (v V1TypeSystemValidator) ValidateManifest(manifest Manifest) []Diagnostic {
	return v.ValidateManifestTypes(manifest)
}

// ValidateCompatibility validates a source output field against a target input field.
//
// When either side is typed as "any", compatibility is allowed but returns a warning.
func (V1TypeSystemValidator) ValidateCompatibility(sourceField string, sourceSpec FieldSpec, targetField string, targetSpec FieldSpec) []Diagnostic {
	diags := make([]Diagnostic, 0)
	validateCompatibility(sourceField, sourceSpec, targetField, targetSpec, &diags)
	return diags
}

func validateFieldSpec(path string, spec FieldSpec, diags *[]Diagnostic) {
	if !isValidV1Type(spec.Type) {
		*diags = append(*diags, Diagnostic{
			Field:    path + ".type",
			Code:     "INVALID_TYPE",
			Severity: SeverityError,
			Message:  fmt.Sprintf("Unsupported type %q; allowed: string, integer, float, boolean, bytes, array, object, any", spec.Type),
		})
		return
	}

	if spec.Type == TypeArray {
		if spec.Items == nil {
			*diags = append(*diags, Diagnostic{
				Field:    path + ".items",
				Code:     "REQUIRED_ITEMS",
				Severity: SeverityError,
				Message:  "items is required when type is array",
			})
			return
		}
		validateFieldSpec(path+".items", *spec.Items, diags)
	}

	for _, name := range sortedFieldNames(spec.Properties) {
		validateFieldSpec(path+".properties."+name, spec.Properties[name], diags)
	}
}

func validateCompatibility(sourceField string, sourceSpec FieldSpec, targetField string, targetSpec FieldSpec, diags *[]Diagnostic) {
	sourceType := sourceSpec.Type
	targetType := targetSpec.Type

	if !isValidV1Type(sourceType) {
		*diags = append(*diags, Diagnostic{
			Field:       sourceField + ".type",
			SourceField: sourceField,
			TargetField: targetField,
			SourceType:  sourceType,
			TargetType:  targetType,
			Code:        "INVALID_SOURCE_TYPE",
			Severity:    SeverityError,
			Message:     fmt.Sprintf("Source field has unsupported type %q", sourceType),
		})
		return
	}

	if !isValidV1Type(targetType) {
		*diags = append(*diags, Diagnostic{
			Field:       targetField + ".type",
			SourceField: sourceField,
			TargetField: targetField,
			SourceType:  sourceType,
			TargetType:  targetType,
			Code:        "INVALID_TARGET_TYPE",
			Severity:    SeverityError,
			Message:     fmt.Sprintf("Target field has unsupported type %q", targetType),
		})
		return
	}

	if sourceType == TypeAny || targetType == TypeAny {
		*diags = append(*diags, Diagnostic{
			Field:       targetField,
			SourceField: sourceField,
			TargetField: targetField,
			SourceType:  sourceType,
			TargetType:  targetType,
			Code:        "TYPE_ANY_BYPASS",
			Severity:    SeverityWarning,
			Message:     "Type compatibility check bypassed because one side is typed as any",
		})
		return
	}

	if sourceType != targetType {
		*diags = append(*diags, Diagnostic{
			Field:       targetField,
			SourceField: sourceField,
			TargetField: targetField,
			SourceType:  sourceType,
			TargetType:  targetType,
			Code:        "TYPE_MISMATCH",
			Severity:    SeverityError,
			Message:     fmt.Sprintf("Type mismatch: source %q (%s) is not compatible with target %q (%s)", sourceField, sourceType, targetField, targetType),
		})
		return
	}

	switch sourceType {
	case TypeArray:
		if sourceSpec.Items == nil {
			*diags = append(*diags, Diagnostic{
				Field:       sourceField + ".items",
				SourceField: sourceField,
				TargetField: targetField,
				SourceType:  sourceType,
				TargetType:  targetType,
				Code:        "ARRAY_ITEMS_REQUIRED",
				Severity:    SeverityError,
				Message:     "Source array type must define items",
			})
			return
		}
		if targetSpec.Items == nil {
			*diags = append(*diags, Diagnostic{
				Field:       targetField + ".items",
				SourceField: sourceField,
				TargetField: targetField,
				SourceType:  sourceType,
				TargetType:  targetType,
				Code:        "ARRAY_ITEMS_REQUIRED",
				Severity:    SeverityError,
				Message:     "Target array type must define items",
			})
			return
		}
		validateCompatibility(sourceField+"[]", *sourceSpec.Items, targetField+"[]", *targetSpec.Items, diags)
	case TypeObject:
		// Generic object type: no declared properties on either side.
		if len(sourceSpec.Properties) == 0 || len(targetSpec.Properties) == 0 {
			return
		}
		for _, propName := range sortedFieldNames(targetSpec.Properties) {
			targetProp := targetSpec.Properties[propName]
			sourceProp, ok := sourceSpec.Properties[propName]
			propSourceField := sourceField + "." + propName
			propTargetField := targetField + "." + propName
			if !ok {
				if targetProp.Required {
					*diags = append(*diags, Diagnostic{
						Field:       propTargetField,
						SourceField: sourceField,
						TargetField: propTargetField,
						SourceType:  sourceType,
						TargetType:  targetProp.Type,
						Code:        "OBJECT_PROPERTY_MISSING",
						Severity:    SeverityError,
						Message:     fmt.Sprintf("Target requires property %q which is missing from source object", propName),
					})
				}
				continue
			}
			validateCompatibility(propSourceField, sourceProp, propTargetField, targetProp, diags)
		}
	}
}

func isValidV1Type(typeName string) bool {
	_, ok := validV1Types[typeName]
	return ok
}

func sortedActionNames(actions map[string]ActionSpec) []string {
	names := make([]string, 0, len(actions))
	for name := range actions {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func sortedFieldNames(fields map[string]FieldSpec) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
