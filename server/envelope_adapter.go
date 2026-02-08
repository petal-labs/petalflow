package server

import (
	"encoding/base64"
	"time"

	"github.com/petal-labs/petalflow/core"
)

// EnvelopeJSON is the JSON-serializable representation of an Envelope.
type EnvelopeJSON struct {
	Vars      map[string]any `json:"vars"`
	Messages  []MessageJSON  `json:"messages,omitempty"`
	Artifacts []ArtifactJSON `json:"artifacts,omitempty"`
	Trace     *TraceJSON     `json:"trace,omitempty"`
}

// MessageJSON is the JSON-serializable representation of a Message.
type MessageJSON struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

// ArtifactJSON is the JSON-serializable representation of an Artifact.
type ArtifactJSON struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type"`
	MimeType string `json:"mime_type,omitempty"`
	Text     string `json:"text,omitempty"`
	Content  string `json:"content,omitempty"` // base64 for binary data
	URI      string `json:"uri,omitempty"`
}

// TraceJSON is the JSON-serializable representation of TraceInfo.
type TraceJSON struct {
	RunID     string `json:"run_id"`
	StartedAt string `json:"started_at,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
}

// EnvelopeToJSON converts a live Envelope to the JSON-serializable form.
func EnvelopeToJSON(env *core.Envelope) EnvelopeJSON {
	if env == nil {
		return EnvelopeJSON{Vars: make(map[string]any)}
	}

	result := EnvelopeJSON{
		Vars: env.Vars,
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
		}
		// Base64-encode binary content.
		if len(art.Bytes) > 0 {
			aj.Content = base64.StdEncoding.EncodeToString(art.Bytes)
		}
		result.Artifacts = append(result.Artifacts, aj)
	}

	// Convert trace.
	if env.Trace.RunID != "" {
		tj := &TraceJSON{
			RunID:   env.Trace.RunID,
			TraceID: env.Trace.TraceID,
		}
		if !env.Trace.Started.IsZero() {
			tj.StartedAt = env.Trace.Started.Format(time.RFC3339)
		}
		result.Trace = tj
	}

	return result
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
