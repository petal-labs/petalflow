package tool

import "testing"

type stubManifestValidator struct {
	diags []Diagnostic
}

func (s stubManifestValidator) ValidateManifest(manifest Manifest) []Diagnostic {
	return s.diags
}

type stubRegistrationValidator struct {
	diags []Diagnostic
}

func (s stubRegistrationValidator) ValidateRegistration(reg Registration) []Diagnostic {
	return s.diags
}

func TestPipelineValidateManifest(t *testing.T) {
	var p Pipeline
	p.AddManifestValidator(stubManifestValidator{
		diags: []Diagnostic{
			{Severity: SeverityWarning, Message: "warning"},
		},
	})
	p.AddManifestValidator(stubManifestValidator{
		diags: []Diagnostic{
			{Severity: SeverityError, Message: "error"},
		},
	})

	result := p.ValidateManifest(NewManifest("test_tool"))
	if len(result.Diagnostics) != 2 {
		t.Fatalf("diagnostic count = %d, want 2", len(result.Diagnostics))
	}
	if !result.HasErrors() {
		t.Fatal("HasErrors() = false, want true")
	}
}

func TestPipelineValidateRegistration(t *testing.T) {
	var p Pipeline
	p.AddRegistrationValidator(stubRegistrationValidator{
		diags: []Diagnostic{
			{Severity: SeverityWarning, Message: "warning"},
		},
	})

	result := p.ValidateRegistration(Registration{Name: "tool_1"})
	if len(result.Diagnostics) != 1 {
		t.Fatalf("diagnostic count = %d, want 1", len(result.Diagnostics))
	}
	if result.HasErrors() {
		t.Fatal("HasErrors() = true, want false")
	}
}
