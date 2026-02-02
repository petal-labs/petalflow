package petalflow

import (
	"strings"
	"time"
)

// Envelope is the single data structure passed between nodes.
// The runtime treats Envelope as append-friendly: nodes should add data
// instead of overwriting, unless specifically configured otherwise.
//
// For parallel execution, envelopes are cloned before branching to avoid
// cross-branch mutation.
type Envelope struct {
	// Input is the primary payload for a run.
	// Many workflows use Vars + Artifacts instead.
	Input any

	// Vars is shared state across the run (named outputs, intermediate state).
	// Nodes write their outputs here for downstream consumption.
	Vars map[string]any

	// Artifacts represent documents, chunks, citations, files, or structured outputs.
	Artifacts []Artifact

	// Messages are chat-style messages useful for LLM steps and auditing.
	Messages []Message

	// Errors accumulated across nodes (for continue-on-error patterns).
	Errors []NodeError

	// Trace information for observability and replay.
	Trace TraceInfo
}

// NewEnvelope creates a new empty envelope with initialized maps and slices.
func NewEnvelope() *Envelope {
	return &Envelope{
		Vars:      make(map[string]any),
		Artifacts: make([]Artifact, 0),
		Messages:  make([]Message, 0),
		Errors:    make([]NodeError, 0),
		Trace: TraceInfo{
			Started: time.Now(),
		},
	}
}

// Clone creates a copy of the envelope suitable for parallel execution.
// Maps and slices are shallow-copied to avoid accidental cross-branch mutation.
// Note: payload fields inside Artifacts and Messages may still reference shared memory.
func (e *Envelope) Clone() *Envelope {
	if e == nil {
		return nil
	}

	out := &Envelope{
		Input: e.Input,
		Trace: e.Trace,
	}

	// Deep copy Vars map
	if e.Vars != nil {
		out.Vars = make(map[string]any, len(e.Vars))
		for k, v := range e.Vars {
			out.Vars[k] = v
		}
	}

	// Copy slices
	if e.Artifacts != nil {
		out.Artifacts = make([]Artifact, len(e.Artifacts))
		copy(out.Artifacts, e.Artifacts)
	}

	if e.Messages != nil {
		out.Messages = make([]Message, len(e.Messages))
		copy(out.Messages, e.Messages)
	}

	if e.Errors != nil {
		out.Errors = make([]NodeError, len(e.Errors))
		copy(out.Errors, e.Errors)
	}

	return out
}

// GetVar retrieves a variable by name from the Vars map.
// Returns the value and true if found, or nil and false if not.
func (e *Envelope) GetVar(name string) (any, bool) {
	if e.Vars == nil {
		return nil, false
	}
	v, ok := e.Vars[name]
	return v, ok
}

// GetVarString retrieves a variable as a string.
// Returns empty string if not found or not a string.
func (e *Envelope) GetVarString(name string) string {
	v, ok := e.GetVar(name)
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// GetVarNested retrieves a nested variable using dot notation (e.g., "response.data.id").
// Returns the value and true if found, or nil and false if not.
func (e *Envelope) GetVarNested(path string) (any, bool) {
	if e.Vars == nil {
		return nil, false
	}

	parts := strings.Split(path, ".")
	var current any = e.Vars

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = v[part]
			if !ok {
				return nil, false
			}
		default:
			return nil, false
		}
	}

	return current, true
}

// SetVar sets a variable in the Vars map.
// Initializes the map if nil.
func (e *Envelope) SetVar(name string, value any) {
	if e.Vars == nil {
		e.Vars = make(map[string]any)
	}
	e.Vars[name] = value
}

// AppendArtifact adds an artifact to the envelope.
func (e *Envelope) AppendArtifact(artifact Artifact) {
	e.Artifacts = append(e.Artifacts, artifact)
}

// AppendMessage adds a message to the envelope.
func (e *Envelope) AppendMessage(msg Message) {
	e.Messages = append(e.Messages, msg)
}

// AppendError records a node error in the envelope.
func (e *Envelope) AppendError(err NodeError) {
	e.Errors = append(e.Errors, err)
}

// HasErrors returns true if there are any recorded errors.
func (e *Envelope) HasErrors() bool {
	return len(e.Errors) > 0
}

// GetArtifactsByType returns all artifacts of a specific type.
func (e *Envelope) GetArtifactsByType(artifactType string) []Artifact {
	var result []Artifact
	for _, a := range e.Artifacts {
		if a.Type == artifactType {
			result = append(result, a)
		}
	}
	return result
}

// GetLastMessage returns the most recent message, or nil if none.
func (e *Envelope) GetLastMessage() *Message {
	if len(e.Messages) == 0 {
		return nil
	}
	return &e.Messages[len(e.Messages)-1]
}

// GetMessagesByRole returns all messages with the specified role.
func (e *Envelope) GetMessagesByRole(role string) []Message {
	var result []Message
	for _, m := range e.Messages {
		if m.Role == role {
			result = append(result, m)
		}
	}
	return result
}

// WithInput returns a new envelope with the given input set.
// This is useful for fluent initialization.
func (e *Envelope) WithInput(input any) *Envelope {
	e.Input = input
	return e
}

// WithVar returns the envelope after setting a variable.
// This is useful for fluent initialization.
func (e *Envelope) WithVar(name string, value any) *Envelope {
	e.SetVar(name, value)
	return e
}

// WithTrace returns the envelope after setting trace info.
// This is useful for fluent initialization.
func (e *Envelope) WithTrace(trace TraceInfo) *Envelope {
	e.Trace = trace
	return e
}
