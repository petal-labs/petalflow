// Package core provides the foundational types and interfaces for PetalFlow workflows.
//
// This package contains:
//   - Core types: NodeKind, Message, Artifact, TraceInfo, etc.
//   - Interfaces: Node, LLMClient, PetalTool
//   - Data structures: Envelope (the primary data carrier)
package core

import (
	"context"
	"time"
)

// NodeKind identifies the type of a node.
// The set of kinds is intentionally small to avoid growing a "node zoo".
type NodeKind string

const (
	NodeKindLLM         NodeKind = "llm"
	NodeKindTool        NodeKind = "tool"
	NodeKindRouter      NodeKind = "router"
	NodeKindMerge       NodeKind = "merge"
	NodeKindMap         NodeKind = "map"
	NodeKindGate        NodeKind = "gate"
	NodeKindNoop        NodeKind = "noop"
	NodeKindFilter      NodeKind = "filter"
	NodeKindTransform   NodeKind = "transform"
	NodeKindGuardian    NodeKind = "guardian"
	NodeKindCache       NodeKind = "cache"
	NodeKindSink        NodeKind = "sink"
	NodeKindHuman       NodeKind = "human"
	NodeKindConditional NodeKind = "conditional"
)

// String returns the string representation of the NodeKind.
func (k NodeKind) String() string {
	return string(k)
}

// Message is a chat-style message used for LLM steps and auditing.
type Message struct {
	Role    string         // "system" | "user" | "assistant" | "tool"
	Content string         // plain text; markdown allowed
	Name    string         // optional (tool name, agent role, etc.)
	Meta    map[string]any // optional metadata
}

// Artifact represents a document or derived data produced during a run.
// For large payloads, use URI to reference external storage rather than
// embedding the data directly.
type Artifact struct {
	ID       string         // stable within a run; may be empty if not needed
	Type     string         // e.g. "document", "chunk", "citation", "json", "file"
	MimeType string         // optional: "text/plain", "application/json", etc.
	Text     string         // optional textual content
	Bytes    []byte         // optional binary content (avoid for large data)
	URI      string         // optional pointer to external storage
	Meta     map[string]any // flexible metadata (source, offsets, confidence, etc.)
}

// TraceInfo is propagated by the runtime for observability and replay.
type TraceInfo struct {
	RunID    string    // unique identifier for this run
	ParentID string    // optional: for subgraphs or map/fanout
	SpanID   string    // optional: for node-level tracing
	TraceID  string    // OpenTelemetry trace ID
	Started  time.Time // when the run started
}

// NodeError is recorded when nodes fail but the graph continues
// (when ContinueOnError is enabled).
type NodeError struct {
	NodeID  string         // ID of the node that failed
	Kind    NodeKind       // kind of the node
	Message string         // error message
	Attempt int            // which attempt this was (1-indexed)
	At      time.Time      // when the error occurred
	Details map[string]any // additional error context
	Cause   error          // underlying error (may be nil)
}

// Error implements the error interface for NodeError.
func (e NodeError) Error() string {
	return e.Message
}

// Unwrap returns the underlying cause for error unwrapping.
func (e NodeError) Unwrap() error {
	return e.Cause
}

// RouteDecision is produced by RouterNode to indicate which targets to activate.
type RouteDecision struct {
	Targets    []string       // node IDs to route to
	Reason     string         // explanation for the decision
	Confidence *float64       // optional confidence score (0.0-1.0)
	Meta       map[string]any // additional routing metadata
}

// RetryPolicy configures retry behavior for nodes that call external systems.
type RetryPolicy struct {
	MaxAttempts int           // maximum number of attempts (1 = no retries)
	Backoff     time.Duration // base backoff duration between attempts
}

// DefaultRetryPolicy returns a sensible default retry policy.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		Backoff:     time.Second,
	}
}

// Budget is an optional guardrail for LLM calls to limit resource usage.
type Budget struct {
	MaxInputTokens  int     // maximum input tokens allowed
	MaxOutputTokens int     // maximum output tokens allowed
	MaxTotalTokens  int     // maximum total tokens allowed
	MaxCostUSD      float64 // maximum cost in USD allowed
}

// TokenUsage tracks token consumption for cost tracking and budgeting.
type TokenUsage struct {
	InputTokens  int     // tokens consumed by input/prompt
	OutputTokens int     // tokens generated in output
	TotalTokens  int     // total tokens (input + output)
	CostUSD      float64 // cost in USD (if computable)
}

// Add combines two TokenUsage values.
func (u TokenUsage) Add(other TokenUsage) TokenUsage {
	return TokenUsage{
		InputTokens:  u.InputTokens + other.InputTokens,
		OutputTokens: u.OutputTokens + other.OutputTokens,
		TotalTokens:  u.TotalTokens + other.TotalTokens,
		CostUSD:      u.CostUSD + other.CostUSD,
	}
}

// ErrorPolicy defines how a node handles errors.
type ErrorPolicy string

const (
	// ErrorPolicyFail causes the node to fail and abort the run (default).
	ErrorPolicyFail ErrorPolicy = "fail"
	// ErrorPolicyContinue records the error and continues execution.
	ErrorPolicyContinue ErrorPolicy = "continue"
	// ErrorPolicyRecord records the error in the envelope and continues.
	ErrorPolicyRecord ErrorPolicy = "record"
)

// =============================================================================
// LLM Client Interface
// =============================================================================

// LLMClient abstracts a single provider/model backend for PetalFlow.
// Implementations adapt various LLM providers to this common interface.
type LLMClient interface {
	Complete(ctx context.Context, req LLMRequest) (LLMResponse, error)
}

// StreamingLLMClient extends LLMClient with streaming capability.
type StreamingLLMClient interface {
	LLMClient
	// CompleteStream returns a channel of StreamChunks.
	// The channel is closed when streaming is complete.
	// The final chunk has Done=true and includes Usage.
	CompleteStream(ctx context.Context, req LLMRequest) (<-chan StreamChunk, error)
}

// StreamChunk is a partial response from the LLM.
type StreamChunk struct {
	Delta       string         // incremental text
	Index       int            // chunk sequence (0-indexed)
	Done        bool           // final chunk indicator
	Accumulated string         // full text so far (optional)
	Usage       *LLMTokenUsage // populated on final chunk
	Error       error          // streaming error
}

// LLMRequest is the request structure for LLM completion.
// It is transport-agnostic and works across different providers.
type LLMRequest struct {
	Model        string         // model identifier (e.g., "gpt-4", "claude-3-opus")
	System       string         // system prompt (Chat Completions API style)
	Instructions string         // system instructions (Responses API style)
	Messages     []LLMMessage   // conversation messages
	InputText    string         // optional: simple prompt mode (converted to user message)
	JSONSchema   map[string]any // optional: structured output constraints
	Temperature  *float64       // optional: sampling temperature
	MaxTokens    *int           // optional: maximum output tokens
	Meta         map[string]any // trace/cost controls
}

// LLMMessage is a chat message in PetalFlow format.
type LLMMessage struct {
	Role        string          // "system", "user", "assistant", "tool"
	Content     string          // message content
	Name        string          // optional: tool name, agent role, etc.
	ToolCalls   []LLMToolCall   // for assistant messages with pending tool calls
	ToolResults []LLMToolResult // for tool result messages (Role="tool")
	Meta        map[string]any  // optional metadata
}

// LLMResponse captures the output from an LLM call.
type LLMResponse struct {
	Text      string              // raw text output
	JSON      map[string]any      // parsed JSON if structured output was requested
	Messages  []LLMMessage        // conversation messages including response
	Usage     LLMTokenUsage       // token consumption
	Provider  string              // provider ID that handled the request
	Model     string              // model that generated the response
	ToolCalls []LLMToolCall       // tool calls requested by the model
	Reasoning *LLMReasoningOutput // reasoning output from the model (optional)
	Status    string              // response status (optional)
	Meta      map[string]any      // additional response metadata
}

// LLMTokenUsage tracks token consumption for LLM calls.
type LLMTokenUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	CostUSD      float64 // optional: computed cost
}

// LLMToolCall represents a tool invocation requested by the model.
type LLMToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// LLMToolResult represents the result of executing a tool.
// This is used to send tool execution results back to the model for multi-turn tool use.
type LLMToolResult struct {
	CallID  string // Must match LLMToolCall.ID from the response
	Content any    // Result data (will be JSON marshaled by the adapter)
	IsError bool   // True if this represents an error result
}

// LLMReasoningOutput contains reasoning information from the model.
// This is populated for models that support reasoning features (e.g., o1, o3).
type LLMReasoningOutput struct {
	ID      string   // Reasoning output identifier
	Summary []string // Reasoning summary points
}

// =============================================================================
// Tool Interface
// =============================================================================

// PetalTool is the tool interface for PetalFlow.
// It provides a simpler interface for workflow integration.
type PetalTool interface {
	Name() string
	Invoke(ctx context.Context, args map[string]any) (map[string]any, error)
}

// FuncTool is a simple function-backed tool for PetalFlow.
// Useful for creating tools inline without implementing a full interface.
type FuncTool struct {
	name        string
	description string
	fn          func(ctx context.Context, args map[string]any) (map[string]any, error)
}

// NewFuncTool creates a new function-backed tool.
func NewFuncTool(name, description string, fn func(ctx context.Context, args map[string]any) (map[string]any, error)) *FuncTool {
	return &FuncTool{
		name:        name,
		description: description,
		fn:          fn,
	}
}

// Name returns the tool's name.
func (t *FuncTool) Name() string {
	return t.name
}

// Description returns the tool's description.
func (t *FuncTool) Description() string {
	return t.description
}

// Invoke executes the tool function.
func (t *FuncTool) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	if t.fn == nil {
		return map[string]any{}, nil
	}
	return t.fn(ctx, args)
}

// ToolRegistry holds a collection of tools for lookup by name.
type ToolRegistry struct {
	tools map[string]PetalTool
}

// NewToolRegistry creates a new tool registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]PetalTool),
	}
}

// Register adds a tool to the registry.
func (r *ToolRegistry) Register(tool PetalTool) {
	r.tools[tool.Name()] = tool
}

// Get retrieves a tool by name.
func (r *ToolRegistry) Get(name string) (PetalTool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// List returns all registered tool names.
func (r *ToolRegistry) List() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// ParseNodeKind converts a string to a NodeKind.
func ParseNodeKind(s string) NodeKind {
	return NodeKind(s)
}

// Ensure interface compliance at compile time.
var _ PetalTool = (*FuncTool)(nil)
