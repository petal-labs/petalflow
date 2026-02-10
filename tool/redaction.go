package tool

import "strings"

// MaskedSecretValue is used in user-facing output for sensitive config values.
const MaskedSecretValue = "**********"

// MaskSensitiveConfig returns a copy of config values with sensitive entries masked.
func MaskSensitiveConfig(specs map[string]FieldSpec, values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	masked := make(map[string]string, len(values))
	for key, value := range values {
		spec, ok := specs[key]
		if ok && spec.Sensitive && strings.TrimSpace(value) != "" {
			masked[key] = MaskedSecretValue
			continue
		}
		masked[key] = value
	}
	return masked
}

// RedactRegistration clones a registration and masks sensitive config values.
func RedactRegistration(reg ToolRegistration) ToolRegistration {
	out := cloneRegistration(reg)
	out.Config = MaskSensitiveConfig(out.Manifest.Config, out.Config)
	return out
}

// RedactRegistrations clones all registrations and masks sensitive config values.
func RedactRegistrations(regs []ToolRegistration) []ToolRegistration {
	out := make([]ToolRegistration, 0, len(regs))
	for _, reg := range regs {
		out = append(out, RedactRegistration(reg))
	}
	return out
}
