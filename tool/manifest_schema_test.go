package tool

import (
	"encoding/json"
	"slices"
	"strconv"
	"testing"
)

func TestLoadManifestSchemaV1(t *testing.T) {
	raw, err := LoadManifestSchemaV1()
	if err != nil {
		t.Fatalf("LoadManifestSchemaV1() error = %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("schema artifact is empty")
	}

	var schemaDoc map[string]any
	if err := json.Unmarshal(raw, &schemaDoc); err != nil {
		t.Fatalf("schema artifact is not valid JSON: %v", err)
	}

	if schemaDoc["$id"] != SchemaToolV1 {
		t.Fatalf("$id = %v, want %q", schemaDoc["$id"], SchemaToolV1)
	}
}

func TestValidateManifestJSONValid(t *testing.T) {
	manifest := NewManifest("s3_fetch")
	manifest.Transport = NewHTTPTransport(HTTPTransport{
		Endpoint: "http://localhost:9801",
	})
	manifest.Actions["list"] = ActionSpec{
		Inputs: map[string]FieldSpec{
			"bucket": {Type: "string", Required: true},
			"prefix": {Type: "string"},
		},
		Outputs: map[string]FieldSpec{
			"keys": {
				Type: "array",
				Items: &FieldSpec{
					Type: "string",
				},
			},
		},
		Idempotent: true,
	}
	manifest.Config = map[string]FieldSpec{
		"credentials": {Type: "string", Required: true, Sensitive: true},
	}

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	result := ValidateManifestJSON(data)
	if result.HasErrors() {
		t.Fatalf("ValidateManifestJSON() returned errors: %#v", result.Diagnostics)
	}
}

func TestValidateManifestJSONFieldLevelErrors(t *testing.T) {
	invalid := []byte(`{
	  "manifest_version": 1,
	  "tool": { "name": "" },
	  "transport": { "type": "mcp", "mode": "socket" },
	  "actions": {
		"list": {
		  "idempotent": "yes",
		  "inputs": {
			"bucket": { "type": "str" }
		  }
		}
	  }
	}`)

	result := ValidateManifestJSON(invalid)
	if !result.HasErrors() {
		t.Fatal("ValidateManifestJSON() should return errors for invalid manifest")
	}

	fields := diagnosticFields(result.Diagnostics)
	wantFields := []string{
		"manifest_version",
		"tool.name",
		"transport.mode",
		"actions.list.idempotent",
		"actions.list.inputs.bucket.type",
	}

	for _, field := range wantFields {
		if !slices.Contains(fields, field) {
			t.Fatalf("expected error on field %q, got fields: %v", field, fields)
		}
	}
}

func TestValidateManifestJSONRequiredFieldErrors(t *testing.T) {
	invalid := []byte(`{
	  "manifest_version": "1.0",
	  "tool": {},
	  "transport": { "type": "http" }
	}`)

	result := ValidateManifestJSON(invalid)
	if !result.HasErrors() {
		t.Fatal("ValidateManifestJSON() should return errors when required fields are missing")
	}

	fields := diagnosticFields(result.Diagnostics)
	wantFields := []string{
		"tool.name",
		"actions",
		"transport.endpoint",
	}
	for _, field := range wantFields {
		if !slices.Contains(fields, field) {
			t.Fatalf("expected error on field %q, got fields: %v", field, fields)
		}
	}
}

func TestSchemaManifestValidatorImplementsInterface(t *testing.T) {
	var _ ManifestValidator = SchemaManifestValidator{}
}

func TestAsIntegerOverflowGuards(t *testing.T) {
	if got, ok := asInteger(uint(1)); !ok || got != 1 {
		t.Fatalf("asInteger(uint(1)) = (%d, %t), want (1, true)", got, ok)
	}

	if strconv.IntSize == 64 {
		maxUint := ^uint(0)
		if _, ok := asInteger(maxUint); ok {
			t.Fatal("asInteger(max uint) should fail on 64-bit when value exceeds int64")
		}
	}

	if _, ok := asInteger(maxInt64AsUint64 + 1); ok {
		t.Fatal("asInteger(uint64(maxInt64+1)) should fail")
	}
}

func diagnosticFields(diags []Diagnostic) []string {
	fields := make([]string, 0, len(diags))
	for _, d := range diags {
		fields = append(fields, d.Field)
	}
	return fields
}
