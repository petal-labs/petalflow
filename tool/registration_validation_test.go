package tool

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

type stubReachabilityChecker struct {
	httpErr  error
	stdioErr error
	mcpErr   error
}

func (s stubReachabilityChecker) CheckHTTP(ctx context.Context, endpoint string) error {
	return s.httpErr
}

func (s stubReachabilityChecker) CheckStdio(ctx context.Context, command string) error {
	return s.stdioErr
}

func (s stubReachabilityChecker) CheckMCP(ctx context.Context, reg Registration) error {
	return s.mcpErr
}

func newSQLiteValidationStore(t *testing.T) Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tools.sqlite")
	store, err := NewSQLiteStore(SQLiteStoreConfig{DSN: path, Scope: path})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestValidateNewRegistrationSuccess(t *testing.T) {
	store := newSQLiteValidationStore(t)
	reg := ToolRegistration{
		Name:     "http_probe",
		Origin:   OriginHTTP,
		Manifest: NewManifest("http_probe"),
		Config: map[string]string{
			"token": "abc",
		},
	}
	reg.Manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	reg.Manifest.Actions["check"] = ActionSpec{
		Inputs: map[string]FieldSpec{
			"path": {Type: TypeString},
		},
		Outputs: map[string]FieldSpec{
			"ok": {Type: TypeBoolean},
		},
	}
	reg.Manifest.Config = map[string]FieldSpec{
		"token": {Type: TypeString, Required: true, Sensitive: true},
	}

	err := ValidateNewRegistration(context.Background(), reg, RegistrationValidationOptions{
		Store:               store,
		ReachabilityChecker: stubReachabilityChecker{},
	})
	if err != nil {
		t.Fatalf("ValidateNewRegistration() error = %v", err)
	}
}

func TestValidateNewRegistrationInvalidName(t *testing.T) {
	reg := ToolRegistration{
		Name:     "Bad Name",
		Origin:   OriginNative,
		Manifest: NewManifest("Bad Name"),
	}
	reg.Manifest.Transport = NewNativeTransport()
	reg.Manifest.Actions["execute"] = ActionSpec{
		Inputs:  map[string]FieldSpec{},
		Outputs: map[string]FieldSpec{},
	}

	err := ValidateNewRegistration(context.Background(), reg, RegistrationValidationOptions{
		ReachabilityChecker: stubReachabilityChecker{},
	})
	var validationErr *RegistrationValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *RegistrationValidationError", err)
	}

	if validationErr.Code != RegistrationValidationFailedCode {
		t.Fatalf("Code = %q, want %q", validationErr.Code, RegistrationValidationFailedCode)
	}

	fields := validationFields(validationErr.Details)
	if !slices.Contains(fields, "name") {
		t.Fatalf("expected name diagnostic, got %v", fields)
	}
}

func TestValidateNewRegistrationDuplicateName(t *testing.T) {
	store := newSQLiteValidationStore(t)
	existing := ToolRegistration{
		Name:     "duplicate_tool",
		Origin:   OriginNative,
		Manifest: NewManifest("duplicate_tool"),
		Status:   StatusReady,
	}
	existing.Manifest.Transport = NewNativeTransport()
	existing.Manifest.Actions["execute"] = ActionSpec{
		Inputs:  map[string]FieldSpec{},
		Outputs: map[string]FieldSpec{},
	}
	if err := store.Upsert(context.Background(), existing); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	reg := existing
	err := ValidateNewRegistration(context.Background(), reg, RegistrationValidationOptions{
		Store:               store,
		ReachabilityChecker: stubReachabilityChecker{},
	})
	if err == nil {
		t.Fatal("ValidateNewRegistration() error = nil, want non-nil")
	}
	var validationErr *RegistrationValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *RegistrationValidationError", err)
	}
	if !slices.Contains(validationCodes(validationErr.Details), "NAME_NOT_UNIQUE") {
		t.Fatalf("expected NAME_NOT_UNIQUE, got %v", validationCodes(validationErr.Details))
	}
}

func TestValidateNewRegistrationConfigCompletenessAndSensitiveDefaults(t *testing.T) {
	reg := ToolRegistration{
		Name:     "secure_tool",
		Origin:   OriginNative,
		Manifest: NewManifest("secure_tool"),
		Config:   map[string]string{},
	}
	reg.Manifest.Transport = NewNativeTransport()
	reg.Manifest.Actions["execute"] = ActionSpec{
		Inputs:  map[string]FieldSpec{},
		Outputs: map[string]FieldSpec{},
	}
	reg.Manifest.Config = map[string]FieldSpec{
		"credentials": {
			Type:      TypeString,
			Required:  true,
			Sensitive: true,
			Default:   "do-not-allow",
		},
	}

	err := ValidateNewRegistration(context.Background(), reg, RegistrationValidationOptions{
		ReachabilityChecker: stubReachabilityChecker{},
	})
	if err == nil {
		t.Fatal("ValidateNewRegistration() error = nil, want non-nil")
	}
	var validationErr *RegistrationValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *RegistrationValidationError", err)
	}

	codes := validationCodes(validationErr.Details)
	if !slices.Contains(codes, "REQUIRED_FIELD") {
		t.Fatalf("expected REQUIRED_FIELD, got %v", codes)
	}
	if !slices.Contains(codes, "SENSITIVE_DEFAULT_FORBIDDEN") {
		t.Fatalf("expected SENSITIVE_DEFAULT_FORBIDDEN, got %v", codes)
	}

	for _, detail := range validationErr.Details {
		if detail.Field == "config.credentials" && detail.Message == "do-not-allow" {
			t.Fatalf("sensitive default leaked in message: %q", detail.Message)
		}
	}
}

func TestValidateNewRegistrationTransportUnreachable(t *testing.T) {
	reg := ToolRegistration{
		Name:     "http_down",
		Origin:   OriginHTTP,
		Manifest: NewManifest("http_down"),
	}
	reg.Manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9999"})
	reg.Manifest.Actions["check"] = ActionSpec{
		Inputs:  map[string]FieldSpec{},
		Outputs: map[string]FieldSpec{},
	}

	err := ValidateNewRegistration(context.Background(), reg, RegistrationValidationOptions{
		ReachabilityChecker: stubReachabilityChecker{httpErr: errors.New("connection refused")},
	})
	if err == nil {
		t.Fatal("ValidateNewRegistration() error = nil, want non-nil")
	}
	var validationErr *RegistrationValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *RegistrationValidationError", err)
	}
	if !slices.Contains(validationCodes(validationErr.Details), "UNREACHABLE") {
		t.Fatalf("expected UNREACHABLE, got %v", validationCodes(validationErr.Details))
	}
}

func validationFields(diags []Diagnostic) []string {
	fields := make([]string, 0, len(diags))
	for _, d := range diags {
		fields = append(fields, d.Field)
	}
	return fields
}

func validationCodes(diags []Diagnostic) []string {
	codes := make([]string, 0, len(diags))
	for _, d := range diags {
		codes = append(codes, d.Code)
	}
	return codes
}
