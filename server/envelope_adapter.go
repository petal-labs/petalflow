package server

import (
	"encoding/base64"
	"time"

	"github.com/petal-labs/petalflow/core"
)

// EnvelopeJSON is the JSON-serializable representation of an Envelope.
//
// This is the stable wire contract returned by the CLI (petalflow run --format
// json) and the daemon HTTP API. It intentionally mirrors every field of
// core.Envelope that a caller needs to observe a run's outcome, including any
// node Errors recorded during continue-on-error execution. Dropping Errors here
// would make failed nodes invisible to callers, so they are always surfaced.
type EnvelopeJSON struct {
	Input     any             `json:"input,omitempty"`
	Vars      map[string]any  `json:"vars"`
	Messages  []MessageJSON   `json:"messages,omitempty"`
	Artifacts []ArtifactJSON  `json:"artifacts,omitempty"`
	Errors    []NodeErrorJSON `json:"errors,omitempty"`
	Trace     *TraceJSON      `json:"trace,omitempty"`
}

// MessageJSON is the JSON-serializable representation of a Message.
type MessageJSON struct {
	Role    string         `json:"role"`
	Content string         `json:"content"`
	Name    string         `json:"name,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// ArtifactJSON is the JSON-serializable representation of an Artifact.
type ArtifactJSON struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name,omitempty"`
	Type     string         `json:"type"`
	MimeType string         `json:"mime_type,omitempty"`
	Text     string         `json:"text,omitempty"`
	Content  string         `json:"content,omitempty"` // base64 for binary data
	URI      string         `json:"uri,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"`
}

// NodeErrorJSON is the JSON-serializable representation of a core.NodeError.
// It surfaces errors that were recorded on the envelope while the run continued
// (continue-on-error / record policies) so callers can see which nodes failed
// and why.
type NodeErrorJSON struct {
	NodeID  string         `json:"node_id"`
	Kind    string         `json:"kind,omitempty"`
	Message string         `json:"message"`
	Attempt int            `json:"attempt,omitempty"`
	At      string         `json:"at,omitempty"` // RFC3339
	Details map[string]any `json:"details,omitempty"`
	Cause   string         `json:"cause,omitempty"` // underlying cause, when distinct
}

// TraceJSON is the JSON-serializable representation of TraceInfo.
type TraceJSON struct {
	RunID     string `json:"run_id"`
	StartedAt string `json:"started_at,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
	ParentID  string `json:"parent_id,omitempty"`
	SpanID    string `json:"span_id,omitempty"`
}

// EnvelopeToJSON converts a live Envelope to the JSON-serializable form.
func EnvelopeToJSON(env *core.Envelope) EnvelopeJSON {
	if env == nil {
		return EnvelopeJSON{Vars: make(map[string]any)}
	}

	result := EnvelopeJSON{
		Input: env.Input,
		Vars:  env.Vars,
	}
	if result.Vars == nil {
		result.Vars = make(map[string]any)
	}

	// Convert messages.
	for _, msg := range env.Messages {
		result.Messages = append(result.Messages, MessageJSON{
			Role:    msg.Role,
			Content: msg.Content,
			Name:    msg.Name,
			Meta:    msg.Meta,
		})
	}

	// Convert artifacts.
	for _, art := range env.Artifacts {
		aj := ArtifactJSON{
			ID:       art.ID,
			Name:     art.ID, // Use ID as name fallback
			Type:     art.Type,
			MimeType: art.MimeType,
			Text:     art.Text,
			URI:      art.URI,
			Meta:     art.Meta,
		}
		// Base64-encode binary content.
		if len(art.Bytes) > 0 {
			aj.Content = base64.StdEncoding.EncodeToString(art.Bytes)
		}
		result.Artifacts = append(result.Artifacts, aj)
	}

	// Convert node errors so failed nodes remain visible to callers.
	for _, nodeErr := range env.Errors {
		result.Errors = append(result.Errors, nodeErrorToJSON(nodeErr))
	}

	// Convert trace.
	if env.Trace.RunID != "" {
		tj := &TraceJSON{
			RunID:    env.Trace.RunID,
			TraceID:  env.Trace.TraceID,
			ParentID: env.Trace.ParentID,
			SpanID:   env.Trace.SpanID,
		}
		if !env.Trace.Started.IsZero() {
			tj.StartedAt = env.Trace.Started.Format(time.RFC3339)
		}
		result.Trace = tj
	}

	return result
}

// nodeErrorToJSON converts a core.NodeError to its wire representation.
// The underlying cause is surfaced as a string only when it adds information
// beyond Message (nodes commonly set Message = cause.Error()).
func nodeErrorToJSON(e core.NodeError) NodeErrorJSON {
	out := NodeErrorJSON{
		NodeID:  e.NodeID,
		Kind:    string(e.Kind),
		Message: e.Message,
		Attempt: e.Attempt,
		Details: e.Details,
	}
	if !e.At.IsZero() {
		out.At = e.At.Format(time.RFC3339)
	}
	if e.Cause != nil && e.Cause.Error() != e.Message {
		out.Cause = e.Cause.Error()
	}
	return out
}

// EnvelopeFromJSON converts JSON input data into an Envelope.
// The input map is used to populate the Vars field.
func EnvelopeFromJSON(data map[string]any) *core.Envelope {
	env := core.NewEnvelope()
	for k, v := range data {
		env.SetVar(k, v)
	}
	return env
}
