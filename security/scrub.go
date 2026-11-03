package security

import "regexp"

// MaskedValue replaces secret-shaped material in scrubbed output.
const MaskedValue = "**********"

// Secret-scrubbing patterns: obvious credential-shaped values are
// replaced outright, and key/value pairs keep their key prefix while the
// value is masked. This is the canonical scrubber shared by surfaces
// that must not persist or emit secret material (the tool audit trail,
// runtime event privacy, captured telemetry content).
var (
	secretValuePattern = regexp.MustCompile(`(?i)\b(sk-[A-Za-z0-9]{8,}|Bearer\s+[A-Za-z0-9._\-]{8,}|gh[pousr]_[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9\-]{10,})\b`)
	secretKeyPattern   = regexp.MustCompile(`(?i)\b((?:password|passwd|secret|token|api[_-]?key)\s*[:=]\s*)([^\s;,&]+)\b`)
)

// ScrubSecrets scrubs obvious secret material from free-form text:
// credential-shaped values are masked and key/value secret pairs keep
// their key while the value is masked.
func ScrubSecrets(text string) string {
	if text == "" {
		return ""
	}
	scrubbed := secretValuePattern.ReplaceAllString(text, MaskedValue)
	return secretKeyPattern.ReplaceAllStringFunc(scrubbed, func(match string) string {
		submatch := secretKeyPattern.FindStringSubmatchIndex(match)
		if len(submatch) < 4 {
			return match
		}
		prefix := match[:submatch[3]]
		return prefix + MaskedValue
	})
}
