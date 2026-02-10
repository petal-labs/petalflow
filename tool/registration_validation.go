package tool

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
)

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

const (
	// RegistrationValidationFailedCode identifies aggregated registration failures.
	RegistrationValidationFailedCode = "REGISTRATION_VALIDATION_FAILED"
)

// RegistrationValidationError is a structured validation error payload.
type RegistrationValidationError struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details []Diagnostic `json:"details"`
}

func (e *RegistrationValidationError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// ReachabilityChecker validates transport connectivity for registrations.
type ReachabilityChecker interface {
	CheckHTTP(ctx context.Context, endpoint string) error
	CheckStdio(ctx context.Context, command string) error
	CheckMCP(ctx context.Context, reg Registration) error
}

// DefaultReachabilityChecker validates transport reachability using local probes.
type DefaultReachabilityChecker struct{}

func (DefaultReachabilityChecker) CheckHTTP(ctx context.Context, endpoint string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("invalid endpoint: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("received HTTP %d", resp.StatusCode)
	}
	return nil
}

func (DefaultReachabilityChecker) CheckStdio(ctx context.Context, command string) error {
	_, err := exec.LookPath(command)
	return err
}

func (DefaultReachabilityChecker) CheckMCP(ctx context.Context, reg Registration) error {
	transport, ok := reg.Manifest.Transport.AsMCP()
	if !ok {
		return errors.New("registration transport is not mcp")
	}

	overlay, err := loadOverlayForRegistration(reg)
	if err != nil {
		return err
	}
	client, cleanup, err := newMCPClientFromConfig(ctx, reg.Name, transport, reg.Config, overlay)
	if err != nil {
		return err
	}
	defer cleanup()

	_, err = client.Initialize(ctx)
	return err
}

// RegistrationValidationOptions configures validation behavior.
type RegistrationValidationOptions struct {
	Store               Store
	ReachabilityChecker ReachabilityChecker
	AllowExistingName   bool
}

// ValidateNewRegistration validates a registration against manifest and policy.
func ValidateNewRegistration(ctx context.Context, reg Registration, opts RegistrationValidationOptions) error {
	if opts.ReachabilityChecker == nil {
		opts.ReachabilityChecker = DefaultReachabilityChecker{}
	}

	var pipeline Pipeline
	pipeline.AddManifestValidator(SchemaManifestValidator{})
	pipeline.AddManifestValidator(V1TypeSystemValidator{})
	pipeline.AddRegistrationValidator(RegistrationNameValidator{
		Store:         opts.Store,
		RequireUnique: !opts.AllowExistingName,
	})
	pipeline.AddRegistrationValidator(ActionNameValidator{})
	pipeline.AddRegistrationValidator(TransportReachabilityValidator{
		Checker: opts.ReachabilityChecker,
	})
	pipeline.AddRegistrationValidator(ConfigCompletenessValidator{})
	pipeline.AddRegistrationValidator(SensitiveFieldValidator{})

	manifestResult := pipeline.ValidateManifest(reg.Manifest)
	registrationResult := pipeline.ValidateRegistration(reg)
	diags := append(manifestResult.Diagnostics, registrationResult.Diagnostics...)

	if !hasValidationErrors(diags) {
		return nil
	}

	return &RegistrationValidationError{
		Code:    RegistrationValidationFailedCode,
		Message: "Tool registration failed validation",
		Details: diags,
	}
}

// RegistrationNameValidator validates tool names and uniqueness constraints.
type RegistrationNameValidator struct {
	Store         Store
	RequireUnique bool
}

func (v RegistrationNameValidator) ValidateRegistration(reg Registration) []Diagnostic {
	diags := make([]Diagnostic, 0)
	name := strings.TrimSpace(reg.Name)
	if name == "" {
		return append(diags, Diagnostic{
			Field:    "name",
			Code:     "REQUIRED_FIELD",
			Severity: SeverityError,
			Message:  "Tool name is required",
		})
	}

	if !toolNamePattern.MatchString(name) {
		diags = append(diags, Diagnostic{
			Field:    "name",
			Code:     "INVALID_NAME",
			Severity: SeverityError,
			Message:  "Tool name must match ^[a-z][a-z0-9_]{1,63}$",
		})
	}

	if v.Store != nil && v.RequireUnique {
		if _, exists, err := v.Store.Get(context.Background(), name); err != nil {
			diags = append(diags, Diagnostic{
				Field:    "name",
				Code:     "STORE_LOOKUP_FAILED",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Failed to verify name uniqueness: %v", err),
			})
		} else if exists {
			diags = append(diags, Diagnostic{
				Field:    "name",
				Code:     "NAME_NOT_UNIQUE",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Tool name %q is already registered", name),
			})
		}
	}

	return diags
}

// ActionNameValidator validates action naming and uniqueness.
type ActionNameValidator struct{}

func (ActionNameValidator) ValidateRegistration(reg Registration) []Diagnostic {
	diags := make([]Diagnostic, 0)
	seen := make(map[string]string)

	if len(reg.Manifest.Actions) == 0 {
		diags = append(diags, Diagnostic{
			Field:    "actions",
			Code:     "REQUIRED_FIELD",
			Severity: SeverityError,
			Message:  "At least one action is required",
		})
		return diags
	}

	for actionName := range reg.Manifest.Actions {
		clean := strings.TrimSpace(actionName)
		if clean == "" {
			diags = append(diags, Diagnostic{
				Field:    "actions",
				Code:     "INVALID_ACTION_NAME",
				Severity: SeverityError,
				Message:  "Action names must not be empty",
			})
			continue
		}
		if !toolNamePattern.MatchString(clean) {
			diags = append(diags, Diagnostic{
				Field:    "actions." + clean,
				Code:     "INVALID_ACTION_NAME",
				Severity: SeverityError,
				Message:  "Action name must match ^[a-z][a-z0-9_]{1,63}$",
			})
		}

		lower := strings.ToLower(clean)
		if prev, exists := seen[lower]; exists && prev != clean {
			diags = append(diags, Diagnostic{
				Field:    "actions." + clean,
				Code:     "ACTION_NOT_UNIQUE",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Action name %q conflicts with %q", clean, prev),
			})
			continue
		}
		seen[lower] = clean
	}

	return diags
}

// TransportReachabilityValidator verifies transport-specific connectivity.
type TransportReachabilityValidator struct {
	Checker ReachabilityChecker
}

func (v TransportReachabilityValidator) ValidateRegistration(reg Registration) []Diagnostic {
	checker := v.Checker
	if checker == nil {
		checker = DefaultReachabilityChecker{}
	}

	switch reg.Manifest.Transport.Type {
	case TransportTypeNative:
		return nil
	case TransportTypeHTTP:
		endpoint := strings.TrimSpace(reg.Manifest.Transport.Endpoint)
		if endpoint == "" {
			return []Diagnostic{{
				Field:    "transport.endpoint",
				Code:     "REQUIRED_FIELD",
				Severity: SeverityError,
				Message:  "HTTP transport requires endpoint",
			}}
		}
		if err := checker.CheckHTTP(context.Background(), endpoint); err != nil {
			return []Diagnostic{{
				Field:    "transport.endpoint",
				Code:     "UNREACHABLE",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Could not connect to %s", endpoint),
			}}
		}
	case TransportTypeStdio:
		command := strings.TrimSpace(reg.Manifest.Transport.Command)
		if command == "" {
			return []Diagnostic{{
				Field:    "transport.command",
				Code:     "REQUIRED_FIELD",
				Severity: SeverityError,
				Message:  "Stdio transport requires command",
			}}
		}
		if err := checker.CheckStdio(context.Background(), command); err != nil {
			return []Diagnostic{{
				Field:    "transport.command",
				Code:     "UNREACHABLE",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Command %q is not executable", command),
			}}
		}
	case TransportTypeMCP:
		if err := checker.CheckMCP(context.Background(), reg); err != nil {
			return []Diagnostic{{
				Field:    "transport",
				Code:     "UNREACHABLE",
				Severity: SeverityError,
				Message:  "MCP transport initialization failed",
			}}
		}
	default:
		return []Diagnostic{{
			Field:    "transport.type",
			Code:     "INVALID_TRANSPORT",
			Severity: SeverityError,
			Message:  fmt.Sprintf("Unsupported transport type %q", reg.Manifest.Transport.Type),
		}}
	}

	return nil
}

// ConfigCompletenessValidator enforces required config field values.
type ConfigCompletenessValidator struct{}

func (ConfigCompletenessValidator) ValidateRegistration(reg Registration) []Diagnostic {
	diags := make([]Diagnostic, 0)
	for key, spec := range reg.Manifest.Config {
		if !spec.Required {
			continue
		}

		value := strings.TrimSpace(reg.Config[key])
		if value != "" {
			continue
		}

		diags = append(diags, Diagnostic{
			Field:    "config." + key,
			Code:     "REQUIRED_FIELD",
			Severity: SeverityError,
			Message:  fmt.Sprintf("Required config field %q has no value", key),
		})
	}
	return diags
}

// SensitiveFieldValidator enforces secure sensitive field declarations.
type SensitiveFieldValidator struct{}

func (SensitiveFieldValidator) ValidateRegistration(reg Registration) []Diagnostic {
	diags := make([]Diagnostic, 0)
	for key, spec := range reg.Manifest.Config {
		if !spec.Sensitive {
			continue
		}
		if spec.Default == nil {
			continue
		}

		diags = append(diags, Diagnostic{
			Field:    "config." + key + ".default",
			Code:     "SENSITIVE_DEFAULT_FORBIDDEN",
			Severity: SeverityError,
			Message:  fmt.Sprintf("Sensitive config field %q must not define a default", key),
		})
	}
	return diags
}

func hasValidationErrors(diags []Diagnostic) bool {
	for _, diag := range diags {
		if diag.Severity == SeverityError {
			return true
		}
	}
	return false
}
