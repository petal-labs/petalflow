package tool

import (
	"net/url"
	"strings"
)

// MaskedSecretValue is used in user-facing output for sensitive config values.
const MaskedSecretValue = "**********"

// sensitiveHeaderNames are HTTP header names whose values are always
// redacted in user-facing registration output.
var sensitiveHeaderNames = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"cookie":              {},
	"x-api-key":           {},
	"x-auth-token":        {},
	"x-secret":            {},
}

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

// RedactRegistration clones a registration and masks sensitive config values
// and transport credential material.
func RedactRegistration(reg ToolRegistration) ToolRegistration {
	out := cloneRegistration(reg)
	out.Config = MaskSensitiveConfig(out.Manifest.Config, out.Config)
	redactTransport(&out.Manifest.Transport)
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

// redactTransport masks credential material in a transport spec in place:
// sensitive HTTP header values are masked and endpoint URL query strings
// are stripped so tokens cannot surface in logs, API responses, or events.
func redactTransport(transport *TransportSpec) {
	if transport == nil {
		return
	}

	if len(transport.Headers) > 0 {
		headers := make(map[string]string, len(transport.Headers))
		for key, value := range transport.Headers {
			if isSensitiveHeaderName(key) && strings.TrimSpace(value) != "" {
				headers[key] = MaskedSecretValue
				continue
			}
			headers[key] = value
		}
		transport.Headers = headers
	}

	if strings.TrimSpace(transport.Endpoint) != "" {
		transport.Endpoint = redactEndpoint(transport.Endpoint)
	}
}

func isSensitiveHeaderName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if _, ok := sensitiveHeaderNames[normalized]; ok {
		return true
	}
	return strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "key") ||
		strings.Contains(normalized, "password")
}

// redactEndpoint strips the query string from an endpoint URL.
func redactEndpoint(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return MaskedSecretValue
	}
	if parsed.RawQuery == "" {
		return raw
	}
	parsed.RawQuery = ""
	return parsed.String()
}
