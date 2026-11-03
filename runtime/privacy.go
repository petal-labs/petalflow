package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/petal-labs/petalflow/budget"
	"github.com/petal-labs/petalflow/security"
)

// Event payload privacy classification.
//
// Every runtime event payload field belongs to one of three classes:
//
//   - metadata: identifiers, counts, timings, statuses, model names.
//     Always emitted; low cardinality, no user or model content.
//   - content: prompts, completions, messages, tool arguments, and other
//     user or model data. By default these are reduced to a shape
//     descriptor (sizes, counts, key names — never values) and only
//     leave the process boundary when content capture is explicitly
//     opted in via RunOptions.ContentCapture, in which case they are
//     size-bounded and passed through a redaction pass (credential
//     scrubbing plus sensitive-field masking).
//   - secret: credential-shaped fields (by name). Never emitted in any
//     mode.
//
// Error-class fields (free-form error text) are always scrubbed of
// credential-shaped material and paired with a typed error code where
// the error can be classified.
//
// The runtime applies this classification to every emitted event
// (SanitizeEvent), so the event store, the run-events API, SSE streams,
// and tracing handlers all inherit the same behavior without a separate
// redaction path. Emitters are also expected to classify content fields
// at emission (Event.WithContentPayload) so direct wiring of node
// emitters cannot bypass the classification.

// PayloadClass classifies a payload field by privacy impact.
type PayloadClass string

const (
	// PayloadMetadata is safe to emit in all modes.
	PayloadMetadata PayloadClass = "metadata"

	// PayloadContent is user or model data: metadata descriptors by
	// default, bounded and redacted when capture is opted in.
	PayloadContent PayloadClass = "content"

	// PayloadSecret is credential material: never emitted.
	PayloadSecret PayloadClass = "secret"
)

// RedactedPlaceholder replaces masked values in captured content.
const RedactedPlaceholder = "[redacted]"

// DefaultContentCaptureLimit bounds each captured content field (in
// bytes) when content capture is enabled without an explicit limit.
const DefaultContentCaptureLimit = 16 * 1024

// ContentCapture gates opt-in content capture for event payloads and
// span attributes. The zero value is the default: metadata only —
// content-classified fields are reduced to shape descriptors.
type ContentCapture struct {
	// Enabled opts in to capturing content-classified fields (prompts,
	// completions, messages, tool arguments). Captured values are
	// size-bounded and passed through the redaction pass before
	// emission or persistence. This aligns with the PetalTrace capture
	// ladder: "minimal" maps to disabled, "standard" and "full" to
	// enabled.
	Enabled bool

	// MaxBytes bounds each captured content field. Values at or below
	// zero use DefaultContentCaptureLimit.
	MaxBytes int
}

// limit returns the effective per-field byte limit.
func (c ContentCapture) limit() int {
	if c.MaxBytes > 0 {
		return c.MaxBytes
	}
	return DefaultContentCaptureLimit
}

// RedactText scrubs credential-shaped material from free-form text.
// It is the canonical scrubber used for error messages and captured
// content (see security.ScrubSecrets).
func RedactText(s string) string {
	return security.ScrubSecrets(s)
}

// contentPayloadFields are payload fields classified as content,
// regardless of event kind. Field names are stable across emitters
// (instrumented LLM client, llm/tool nodes, memory nodes, edge
// transfers, run snapshots).
var contentPayloadFields = map[string]struct{}{
	"system_prompt":     {},
	"instructions":      {},
	"messages":          {},
	"input_text":        {},
	"json_schema":       {},
	"completion":        {},
	"tool_calls":        {},
	"text":              {},
	"delta":             {},
	"arguments":         {},
	"data_preview":      {},
	"inputs":            {},
	"reasoning_summary": {},
	"documents":         {},
	"content":           {},
	"note":              {},
}

// errorPayloadFields carry free-form error text: always scrubbed, never
// dropped (paired with typed error_code payload fields at emission).
var errorPayloadFields = map[string]struct{}{
	"error":         {},
	"trigger_error": {},
	"panic_stack":   {},
}

// ClassifyPayloadField returns the privacy class of a payload field.
func ClassifyPayloadField(field string) PayloadClass {
	switch {
	case isSecretFieldName(field):
		return PayloadSecret
	case isContentField(field):
		return PayloadContent
	case isErrorField(field):
		// Error text is scrubbed metadata: it stays present but is
		// never captured raw.
		return PayloadMetadata
	default:
		return PayloadMetadata
	}
}

func isContentField(field string) bool {
	_, ok := contentPayloadFields[field]
	return ok
}

func isErrorField(field string) bool {
	_, ok := errorPayloadFields[field]
	return ok
}

// secretNameFragments and secretNameExacts identify credential-shaped
// field names. Exact names avoid false positives on metadata fields
// like "input_tokens" (only the singular "_token" suffix matches).
var (
	secretNameFragments = []string{
		"password", "passwd", "secret", "credential", "api_key", "apikey",
	}
	secretNameExacts = []string{
		"authorization", "auth", "token", "key", "bearer", "cookie",
	}
)

func isSecretFieldName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	for _, fragment := range secretNameFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	for _, exact := range secretNameExacts {
		if normalized == exact {
			return true
		}
	}
	return strings.HasSuffix(normalized, "_token")
}

// WithContentPayload adds a content-classified field to the event
// payload, applying the capture policy: the bounded, redacted value
// when capture is enabled, or a metadata shape descriptor (sizes,
// counts, key names) by default. Emitters use this at emission so
// classification does not depend on downstream sanitization.
func (e Event) WithContentPayload(field string, value any, capture ContentCapture) Event {
	for key, val := range capture.contentEntries(field, value) {
		e = e.WithPayload(key, val)
	}
	return e
}

// contentEntries returns the payload entries for a content-classified
// field: the captured value when capture is enabled (and the value fits
// the bound), or a shape descriptor otherwise.
func (c ContentCapture) contentEntries(field string, value any) map[string]any {
	if c.Enabled {
		if captured, ok := c.captureValue(value); ok {
			return map[string]any{field: captured}
		}
		// Value cannot be captured within the byte bound: degrade to a
		// shape descriptor.
	}
	return describeContent(field, value)
}

// captureValue returns the captured form of a content value: truncated
// to the byte limit for strings, with sensitive keys masked and string
// leaves scrubbed for maps, and the encoded size bounded. ok is false
// when the value cannot be captured within the limit.
func (c ContentCapture) captureValue(value any) (any, bool) {
	switch typed := value.(type) {
	case string:
		captured := truncateString(RedactText(truncateString(typed, c.limit())), c.limit())
		return captured, true
	case map[string]any:
		masked := redactForCapture(typed)
		if encodedSize(masked) > c.limit() {
			return nil, false
		}
		return masked, true
	default:
		if encodedSize(value) > c.limit() {
			return nil, false
		}
		return value, true
	}
}

// redactForCapture deep-copies a value with secret-named keys masked
// outright and credential-shaped substrings scrubbed from remaining
// string leaves.
func redactForCapture(v any) any {
	switch typed := v.(type) {
	case string:
		return RedactText(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSecretFieldName(key) {
				out[key] = RedactedPlaceholder
				continue
			}
			out[key] = redactForCapture(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactForCapture(item)
		}
		return out
	default:
		return v
	}
}

// Descriptor conventions (aligned with the audit-summary conventions):
// key names and value kinds only, capped at 16 keys and 64 characters
// per key name.
const (
	descriptorKeyLimit = 16
	descriptorKeyChars = 64
)

// describeContent returns the metadata shape descriptor for a content
// value: sizes, counts, and key names — never values. Strings become
// byte sizes; JSON-array strings (messages, tool calls) become element
// counts; maps become key-name/kind shapes.
func describeContent(field string, value any) map[string]any {
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return nil
		}
		if isCountField(field) {
			if count, ok := jsonArrayCount(typed); ok {
				return map[string]any{field + "_count": count}
			}
		}
		return map[string]any{field + "_bytes": len(typed)}
	case map[string]any:
		if len(typed) == 0 {
			return nil
		}
		return map[string]any{field + "_shape": shapeOf(typed)}
	default:
		if value == nil {
			return nil
		}
		return map[string]any{field + "_bytes": encodedSize(value)}
	}
}

// isCountField reports whether a field carries a JSON-encoded array
// whose element count is a useful descriptor.
func isCountField(field string) bool {
	return field == "messages" || field == "tool_calls"
}

// jsonArrayCount parses a JSON array string and returns its element
// count.
func jsonArrayCount(s string) (int, bool) {
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return 0, false
	}
	return len(arr), true
}

// shapeOf maps key names to value kinds, capped at 16 keys and 64
// characters per key name (values are never included).
func shapeOf(obj map[string]any) map[string]string {
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	shape := make(map[string]string, min(len(keys), descriptorKeyLimit))
	for i, key := range keys {
		if i >= descriptorKeyLimit {
			break
		}
		if len(key) > descriptorKeyChars {
			key = key[:descriptorKeyChars]
		}
		shape[key] = valueKindOf(obj[keys[i]])
	}
	return shape
}

func valueKindOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case int, int64, float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

// encodedSize returns the JSON-encoded size of a value in bytes, or 0
// when it cannot be encoded.
func encodedSize(v any) int {
	data, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(data)
}

// truncationMarker marks content cut to fit the capture bound.
const truncationMarker = "…[truncated]"

// truncateString bounds s to limit bytes, cutting at a rune boundary
// and marking the cut.
func truncateString(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	usable := limit - len(truncationMarker)
	if usable <= 0 {
		return s[:runeSafeCut(s, limit)]
	}
	return s[:runeSafeCut(s, usable)] + truncationMarker
}

// runeSafeCut returns the largest byte index at or below max where a
// cut keeps a valid UTF-8 sequence.
func runeSafeCut(s string, max int) int {
	if max >= len(s) {
		return len(s)
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}

// SanitizeEvent applies the payload privacy classification to e and
// returns the sanitized event. Secret-named fields are dropped, content
// fields are reduced to shape descriptors (or captured, bounded and
// redacted, when capture is enabled), and free-form error text is
// scrubbed. The runtime applies this to every emitted event so the
// event store, run-events API, SSE streams, and tracing handlers
// inherit the classification without a separate redaction path.
func SanitizeEvent(e Event, capture ContentCapture) Event {
	if len(e.Payload) == 0 {
		return e
	}

	var out map[string]any
	for key, value := range e.Payload {
		switch {
		case isSecretFieldName(key):
			out = sanitizedPayload(out, e.Payload)
			delete(out, key)
		case isContentField(key):
			entries := capture.contentEntries(key, value)
			if len(entries) == 1 {
				if captured, ok := entries[key]; ok && equalJSON(captured, value) {
					continue
				}
			}
			out = sanitizedPayload(out, e.Payload)
			delete(out, key)
			for descriptorKey, descriptorValue := range entries {
				out[descriptorKey] = descriptorValue
			}
		default:
			if s, ok := value.(string); ok && isErrorField(key) {
				scrubbed := RedactText(s)
				if scrubbed == s {
					continue
				}
				out = sanitizedPayload(out, e.Payload)
				out[key] = scrubbed
			}
		}
	}
	if out == nil {
		return e
	}
	e.Payload = out
	return e
}

// sanitizedPayload lazily clones the payload map on first change so
// unmodified events are returned without allocation.
func sanitizedPayload(out, payload map[string]any) map[string]any {
	if out != nil {
		return out
	}
	cloned := make(map[string]any, len(payload))
	for key, value := range payload {
		cloned[key] = value
	}
	return cloned
}

// equalJSON compares two values by their JSON encoding (safe for
// incomparable types such as maps).
func equalJSON(a, b any) bool {
	ab, aErr := json.Marshal(a)
	bb, bErr := json.Marshal(b)
	return aErr == nil && bErr == nil && string(ab) == string(bb)
}

// ErrorCodeFor maps an error to a stable, machine-readable code for
// failure events. Codes carry no payload, endpoint, or secret
// material; redacted messages stay alongside them for diagnosis.
func ErrorCodeFor(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled), errors.Is(err, ErrRunCanceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, ErrNodePanic):
		return "node_panic"
	case errors.Is(err, ErrHumanPending):
		return "human_pending"
	case errors.Is(err, ErrMaxHopsExceeded):
		return "max_hops_exceeded"
	}
	var exceeded *budget.BudgetExceededError
	if errors.As(err, &exceeded) {
		return "budget_exceeded"
	}
	return "error"
}

// contentCaptureKey is the context key for the node-scoped content
// capture policy.
type contentCaptureKey struct{}

// ContextWithContentCapture attaches the content capture policy for
// node execution. The runtime sets it from RunOptions; nodes read it
// with ContentCaptureFromContext to classify content fields at
// emission. An absent policy means the default metadata-only policy.
func ContextWithContentCapture(ctx context.Context, capture ContentCapture) context.Context {
	return context.WithValue(ctx, contentCaptureKey{}, capture)
}

// ContentCaptureFromContext returns the attached content capture
// policy and whether one was set. The zero policy (returned when unset)
// is the default metadata-only policy.
func ContentCaptureFromContext(ctx context.Context) (ContentCapture, bool) {
	capture, ok := ctx.Value(contentCaptureKey{}).(ContentCapture)
	if !ok {
		return ContentCapture{}, false
	}
	return capture, true
}
