package tool

// Severity defines diagnostic severity produced by validators.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is a structured validation finding.
type Diagnostic struct {
	Field    string   `json:"field,omitempty"`
	Code     string   `json:"code,omitempty"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// ManifestValidator validates a tool manifest.
type ManifestValidator interface {
	ValidateManifest(manifest Manifest) []Diagnostic
}

// RegistrationValidator validates a full registration record.
type RegistrationValidator interface {
	ValidateRegistration(reg Registration) []Diagnostic
}

// Result aggregates diagnostics from one or more validation passes.
type Result struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// HasErrors returns true when at least one error-severity diagnostic exists.
func (r Result) HasErrors() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Pipeline composes validators for manifest and registration checks.
type Pipeline struct {
	manifestValidators     []ManifestValidator
	registrationValidators []RegistrationValidator
}

// AddManifestValidator appends a manifest validator to the pipeline.
func (p *Pipeline) AddManifestValidator(v ManifestValidator) {
	p.manifestValidators = append(p.manifestValidators, v)
}

// AddRegistrationValidator appends a registration validator to the pipeline.
func (p *Pipeline) AddRegistrationValidator(v RegistrationValidator) {
	p.registrationValidators = append(p.registrationValidators, v)
}

// ValidateManifest runs all manifest validators and returns aggregated findings.
func (p Pipeline) ValidateManifest(manifest Manifest) Result {
	result := Result{Diagnostics: make([]Diagnostic, 0)}
	for _, validator := range p.manifestValidators {
		result.Diagnostics = append(result.Diagnostics, validator.ValidateManifest(manifest)...)
	}
	return result
}

// ValidateRegistration runs all registration validators and returns findings.
func (p Pipeline) ValidateRegistration(reg Registration) Result {
	result := Result{Diagnostics: make([]Diagnostic, 0)}
	for _, validator := range p.registrationValidators {
		result.Diagnostics = append(result.Diagnostics, validator.ValidateRegistration(reg)...)
	}
	return result
}
