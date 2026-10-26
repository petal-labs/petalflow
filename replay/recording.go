// Package replay defines the PetalFlow replay recording format and capture
// layer: a versioned, redacted, on-disk record of a workflow run that can be
// validated on read and later replayed offline with deterministic fixtures.
//
// The zero value of RedactionPolicy is the default-deny policy: prompts,
// outputs, tool data, and artifact contents are redacted so recordings are
// safe to store and share. Full-fidelity capture for fixture replay is an
// explicit opt-out (AllowAllPolicy).
package replay

import (
	"errors"
	"fmt"
	"time"
)

// FormatVersion is the schema version of the recording format. Load rejects
// recordings with an incompatible version.
const FormatVersion = 1

// Recording is the versioned capture of a single workflow run.
type Recording struct {
	// Version is the recording schema version (FormatVersion at write time).
	Version int `json:"version"`

	// RunID is the runtime run identifier.
	RunID string `json:"run_id"`

	// GraphName and GraphDefinition describe the executed workflow. The
	// definition is only present when the run was started with
	// RunOptions.CaptureSnapshots.
	GraphName       string `json:"graph_name,omitempty"`
	GraphDefinition any    `json:"graph_definition,omitempty"`

	// Workflow identity and trigger metadata.
	WorkflowID      string `json:"workflow_id,omitempty"`
	WorkflowVersion string `json:"workflow_version,omitempty"`
	Trigger         string `json:"trigger,omitempty"`

	// Inputs is the initial workflow input captured from the run snapshot,
	// when available.
	Inputs any `json:"inputs,omitempty"`

	// StartedAt and FinishedAt bound the run.
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`

	// Status is the terminal run status ("succeeded", "failed", ...).
	Status string `json:"status,omitempty"`
	// FinalError is the terminal error message, when the run failed.
	FinalError string `json:"final_error,omitempty"`

	// Nodes records node executions in execution order. A node may appear
	// more than once (loops, fan-out).
	Nodes []NodeRecord `json:"nodes"`
}

// NodeRecord captures one execution of one node.
type NodeRecord struct {
	NodeID   string `json:"node_id"`
	NodeKind string `json:"node_kind"`

	// Input and Output are envelope snapshots around the node execution.
	// Output is nil when the node failed. Content is redacted per policy.
	Input  *EnvelopeState `json:"input,omitempty"`
	Output *EnvelopeState `json:"output,omitempty"`

	// ElapsedMS is the node wall-clock duration in milliseconds.
	ElapsedMS int64 `json:"elapsed_ms,omitempty"`

	// Error is the node error message, when the node failed.
	Error string `json:"error,omitempty"`

	// LLM and Tools list model calls and tool invocations performed by this
	// node execution, in order.
	LLM   []LLMCallRecord  `json:"llm,omitempty"`
	Tools []ToolCallRecord `json:"tools,omitempty"`
}

// EnvelopeState is a (possibly redacted) snapshot of envelope state.
type EnvelopeState struct {
	Input     any             `json:"input,omitempty"`
	Vars      map[string]any  `json:"vars,omitempty"`
	Artifacts []ArtifactState `json:"artifacts,omitempty"`
	Messages  []MessageState  `json:"messages,omitempty"`
}

// ArtifactState is a captured artifact. Text and Bytes are redacted unless
// the policy includes artifacts.
type ArtifactState struct {
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type,omitempty"`
	MimeType string         `json:"mime_type,omitempty"`
	URI      string         `json:"uri,omitempty"`
	Text     string         `json:"text,omitempty"`
	Bytes    []byte         `json:"bytes,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"`
}

// MessageState is a captured chat message. Content is redacted unless the
// policy includes prompts.
type MessageState struct {
	Role    string         `json:"role"`
	Name    string         `json:"name,omitempty"`
	Content string         `json:"content,omitempty"`
	Raw     any            `json:"raw,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// LLMCallRecord captures one model call and its response. Prompt content
// (System, Messages, InputText) and completion text are redacted unless the
// policy includes them; usage and latency are always kept.
type LLMCallRecord struct {
	NodeID string `json:"node_id"`
	Model  string `json:"model,omitempty"`

	System    string          `json:"system,omitempty"`
	InputText string          `json:"input_text,omitempty"`
	Messages  []LLMMessageRef `json:"messages,omitempty"`

	// Completion is the response text (redacted unless included).
	Completion string `json:"completion,omitempty"`

	// Usage is token consumption (always captured; no content).
	Usage UsageRecord `json:"usage,omitempty"`

	// LatencyMS is the call round-trip time in milliseconds.
	LatencyMS int64 `json:"latency_ms,omitempty"`

	// StopReason and Error carry the outcome.
	StopReason string `json:"stop_reason,omitempty"`
	Error      string `json:"error,omitempty"`
}

// LLMMessageRef is a captured LLM message. Content is redacted unless the
// policy includes prompts.
type LLMMessageRef struct {
	Role    string `json:"role"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

// UsageRecord is token consumption for a model call.
type UsageRecord struct {
	InputTokens         int     `json:"input_tokens,omitempty"`
	OutputTokens        int     `json:"output_tokens,omitempty"`
	TotalTokens         int     `json:"total_tokens,omitempty"`
	CostUSD             float64 `json:"cost_usd,omitempty"`
	CacheReadTokens     int     `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int     `json:"cache_creation_tokens,omitempty"`
}

// ToolCallRecord captures one tool invocation. Arguments and results are
// redacted unless the policy includes tool data.
type ToolCallRecord struct {
	NodeID   string `json:"node_id"`
	ToolName string `json:"tool_name,omitempty"`

	Args      map[string]any `json:"args,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
	Error     string         `json:"error,omitempty"`
	LatencyMS int64          `json:"latency_ms,omitempty"`
}

// ErrInvalidRecording is returned when a recording fails validation.
var ErrInvalidRecording = errors.New("invalid replay recording")

// Validate checks the recording's structural invariants. It is called by
// Load; callers constructing recordings in memory may call it explicitly.
func (r *Recording) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: recording is nil", ErrInvalidRecording)
	}
	if r.Version != FormatVersion {
		return fmt.Errorf("%w: version %d, want %d", ErrInvalidRecording, r.Version, FormatVersion)
	}
	if r.RunID == "" {
		return fmt.Errorf("%w: run_id is required", ErrInvalidRecording)
	}
	for i, node := range r.Nodes {
		if node.NodeID == "" {
			return fmt.Errorf("%w: nodes[%d] missing node_id", ErrInvalidRecording, i)
		}
		if node.Input == nil && node.Output == nil && node.Error == "" && len(node.LLM) == 0 && len(node.Tools) == 0 {
			return fmt.Errorf("%w: nodes[%d] (%s) is empty", ErrInvalidRecording, i, node.NodeID)
		}
	}
	return nil
}
