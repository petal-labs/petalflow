package tool

import (
	"slices"
	"testing"
)

func TestV1TypeSystemValidatorValidateManifestTypes(t *testing.T) {
	validator := V1TypeSystemValidator{}
	manifest := NewManifest("type_test")
	manifest.Actions["list"] = ActionSpec{
		Inputs: map[string]FieldSpec{
			"bucket": {Type: TypeString, Required: true},
			"bad":    {Type: "uuid"},
		},
		Outputs: map[string]FieldSpec{
			"keys": {
				Type: TypeArray,
				Items: &FieldSpec{
					Type: TypeString,
				},
			},
			"broken_array": {
				Type: TypeArray,
			},
		},
	}
	manifest.Config = map[string]FieldSpec{
		"meta": {
			Type: TypeObject,
			Properties: map[string]FieldSpec{
				"attempts": {Type: TypeInteger},
				"bad_prop": {Type: "number"},
			},
		},
	}

	diags := validator.ValidateManifestTypes(manifest)
	fields := diagnosticFields(diags)

	expect := []string{
		"actions.list.inputs.bad.type",
		"actions.list.outputs.broken_array.items",
		"config.meta.properties.bad_prop.type",
	}
	for _, field := range expect {
		if !slices.Contains(fields, field) {
			t.Fatalf("expected diagnostic on %q, got: %v", field, fields)
		}
	}
}

func TestV1TypeSystemValidatorCompatibilityMismatchIncludesSourceTarget(t *testing.T) {
	validator := V1TypeSystemValidator{}
	diags := validator.ValidateCompatibility(
		"actions.src.outputs.count",
		FieldSpec{Type: TypeInteger},
		"actions.dst.inputs.count",
		FieldSpec{Type: TypeString},
	)

	if len(diags) != 1 {
		t.Fatalf("diagnostic count = %d, want 1", len(diags))
	}
	diag := diags[0]
	if diag.Code != "TYPE_MISMATCH" {
		t.Fatalf("Code = %q, want TYPE_MISMATCH", diag.Code)
	}
	if diag.SourceField != "actions.src.outputs.count" {
		t.Fatalf("SourceField = %q, want actions.src.outputs.count", diag.SourceField)
	}
	if diag.TargetField != "actions.dst.inputs.count" {
		t.Fatalf("TargetField = %q, want actions.dst.inputs.count", diag.TargetField)
	}
	if diag.SourceType != TypeInteger || diag.TargetType != TypeString {
		t.Fatalf("SourceType/TargetType = (%q,%q), want (%q,%q)", diag.SourceType, diag.TargetType, TypeInteger, TypeString)
	}
}

func TestV1TypeSystemValidatorCompatibilityAnyProducesWarning(t *testing.T) {
	validator := V1TypeSystemValidator{}
	diags := validator.ValidateCompatibility(
		"actions.src.outputs.result",
		FieldSpec{Type: TypeAny},
		"actions.dst.inputs.result",
		FieldSpec{Type: TypeObject},
	)

	if len(diags) != 1 {
		t.Fatalf("diagnostic count = %d, want 1", len(diags))
	}
	if diags[0].Severity != SeverityWarning {
		t.Fatalf("Severity = %q, want %q", diags[0].Severity, SeverityWarning)
	}
	if diags[0].Code != "TYPE_ANY_BYPASS" {
		t.Fatalf("Code = %q, want TYPE_ANY_BYPASS", diags[0].Code)
	}
}

func TestV1TypeSystemValidatorCompatibilityArrayNested(t *testing.T) {
	validator := V1TypeSystemValidator{}
	source := FieldSpec{
		Type: TypeArray,
		Items: &FieldSpec{
			Type: TypeString,
		},
	}

	targetOK := FieldSpec{
		Type: TypeArray,
		Items: &FieldSpec{
			Type: TypeString,
		},
	}
	if diags := validator.ValidateCompatibility("src.keys", source, "dst.keys", targetOK); len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got: %#v", diags)
	}

	targetBad := FieldSpec{
		Type: TypeArray,
		Items: &FieldSpec{
			Type: TypeInteger,
		},
	}
	diags := validator.ValidateCompatibility("src.keys", source, "dst.keys", targetBad)
	if len(diags) == 0 {
		t.Fatal("expected mismatch diagnostics for array item type mismatch")
	}
	if diags[0].Code != "TYPE_MISMATCH" {
		t.Fatalf("Code = %q, want TYPE_MISMATCH", diags[0].Code)
	}
	if diags[0].SourceField != "src.keys[]" || diags[0].TargetField != "dst.keys[]" {
		t.Fatalf("source/target fields = (%q, %q), want (src.keys[], dst.keys[])", diags[0].SourceField, diags[0].TargetField)
	}
}

func TestV1TypeSystemValidatorCompatibilityObjectProperties(t *testing.T) {
	validator := V1TypeSystemValidator{}
	source := FieldSpec{
		Type: TypeObject,
		Properties: map[string]FieldSpec{
			"name": {Type: TypeString, Required: true},
		},
	}
	target := FieldSpec{
		Type: TypeObject,
		Properties: map[string]FieldSpec{
			"name": {Type: TypeString, Required: true},
			"age":  {Type: TypeInteger, Required: true},
		},
	}

	diags := validator.ValidateCompatibility("src.user", source, "dst.user", target)
	if len(diags) != 1 {
		t.Fatalf("diagnostic count = %d, want 1", len(diags))
	}
	if diags[0].Code != "OBJECT_PROPERTY_MISSING" {
		t.Fatalf("Code = %q, want OBJECT_PROPERTY_MISSING", diags[0].Code)
	}
	if diags[0].TargetField != "dst.user.age" {
		t.Fatalf("TargetField = %q, want dst.user.age", diags[0].TargetField)
	}
}

func TestV1TypeSystemValidatorCompatibilityInvalidType(t *testing.T) {
	validator := V1TypeSystemValidator{}
	diags := validator.ValidateCompatibility(
		"src.value",
		FieldSpec{Type: "uuid"},
		"dst.value",
		FieldSpec{Type: TypeString},
	)
	if len(diags) != 1 {
		t.Fatalf("diagnostic count = %d, want 1", len(diags))
	}
	if diags[0].Code != "INVALID_SOURCE_TYPE" {
		t.Fatalf("Code = %q, want INVALID_SOURCE_TYPE", diags[0].Code)
	}
	if diags[0].Field != "src.value.type" {
		t.Fatalf("Field = %q, want src.value.type", diags[0].Field)
	}
}
