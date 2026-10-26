package nodes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/memory"
	"github.com/petal-labs/petalflow/runtime"
)

// MemoryMode selects what a MemoryNode does with the provider.
type MemoryMode string

const (
	// MemoryModeRetrieve reads memory and injects the assembled context into
	// the envelope (default).
	MemoryModeRetrieve MemoryMode = "retrieve"

	// MemoryModeWrite persists envelope content to memory.
	MemoryModeWrite MemoryMode = "write"
)

// MemoryUnavailableAction defines what happens when the memory provider
// returns an error wrapping memory.ErrUnavailable. All other errors are
// regular node failures governed by RunOptions.ContinueOnError.
type MemoryUnavailableAction string

const (
	// MemoryUnavailableFail fails the node when memory is unavailable
	// (default). The runtime's error policy then decides the run's fate.
	MemoryUnavailableFail MemoryUnavailableAction = "fail"

	// MemoryUnavailableSkip continues without memory: retrieval yields no
	// context and writes are dropped. The memory.read / memory.write event
	// is still emitted with unavailable=true so the gap is observable.
	MemoryUnavailableSkip MemoryUnavailableAction = "skip"
)

// MemoryResult records what a MemoryNode did. It is stored next to the
// node's output variable (suffix "_stats") and is content-free except for
// the assembled context on the result variable itself.
type MemoryResult struct {
	Mode            MemoryMode              `json:"mode"`
	Namespace       string                  `json:"namespace"`
	SessionID       string                  `json:"session_id"`
	ThreadID        string                  `json:"thread_id,omitempty"`
	TenantID        string                  `json:"tenant_id,omitempty"`
	DocumentCount   int                     `json:"document_count,omitempty"`
	EstimatedTokens int                     `json:"estimated_tokens,omitempty"`
	Truncated       bool                    `json:"truncated,omitempty"`
	Compaction      *memory.CompactionStats `json:"compaction,omitempty"`
	Unavailable     bool                    `json:"unavailable,omitempty"`
}

// MemoryNodeConfig configures a MemoryNode.
type MemoryNodeConfig struct {
	// Provider is the memory backend. Required.
	Provider memory.Provider

	// Mode selects retrieve or write. Defaults to retrieve.
	Mode MemoryMode

	// QueryTemplate is a Go text/template for the retrieval query, executed
	// with {{.input}} and {{.vars.name}}. If empty, InputVars are joined.
	QueryTemplate string

	// InputVars lists envelope variables joined into the retrieval query
	// when QueryTemplate is empty.
	InputVars []string

	// Namespace, SessionID, ThreadID, and TenantID override the
	// corresponding fields of the run's memory scope (RunOptions.MemoryScope).
	// Each accepts a template rendered against the envelope. A namespace and
	// session ID must resolve from configuration or the run scope.
	Namespace string
	SessionID string
	ThreadID  string
	TenantID  string

	// Budget bounds the assembled context (token and document/artifact
	// counts). Zero means unbounded.
	Budget memory.ContextBudget

	// Compactor is the compaction/truncation hook applied to retrieved
	// documents. nil uses memory.NewTruncateCompactor().
	Compactor memory.Compactor

	// TokenCounter estimates token counts for budget enforcement.
	// nil uses memory.EstimateTokens.
	TokenCounter memory.TokenCounter

	// OutputVar receives the assembled context text in retrieve mode.
	// Defaults to the node ID + "_memory". A "<OutputVar>_stats" variable
	// always receives the MemoryResult for both modes.
	OutputVar string

	// OmitArtifacts disables injecting retrieved documents as envelope
	// artifacts of type "chunk" (retrieve mode). By default documents are
	// added as artifacts, bounded by Budget.
	OmitArtifacts bool

	// WriteVar names the envelope variable to persist in write mode. When
	// empty, the envelope Input is written.
	WriteVar string

	// WriteKey optionally labels the written record. Accepts a template.
	WriteKey string

	// OnUnavailable selects behavior when the provider is unavailable.
	// Defaults to MemoryUnavailableFail.
	OnUnavailable MemoryUnavailableAction

	// IncludeContentInEvents opts in to recording document text and written
	// content in memory events. The default (false) keeps events
	// content-free: memory reads/writes are observable without storing
	// sensitive content.
	IncludeContentInEvents bool

	// Timeout bounds provider calls. Defaults to 30s.
	Timeout time.Duration
}

// MemoryNode integrates durable memory and knowledge into a workflow. It
// retrieves context by stable session and namespace identifiers, enforces
// token and artifact budgets during assembly, and makes reads and writes
// observable through memory.* events.
type MemoryNode struct {
	core.BaseNode
	config MemoryNodeConfig
}

// NewMemoryNode creates a new MemoryNode with the given configuration.
func NewMemoryNode(id string, config MemoryNodeConfig) *MemoryNode {
	if config.Mode == "" {
		config.Mode = MemoryModeRetrieve
	}
	if config.OnUnavailable == "" {
		config.OnUnavailable = MemoryUnavailableFail
	}
	if config.OutputVar == "" {
		config.OutputVar = id + "_memory"
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	return &MemoryNode{
		BaseNode: core.NewBaseNode(id, core.NodeKindMemory),
		config:   config,
	}
}

// Config returns the node's configuration.
func (n *MemoryNode) Config() MemoryNodeConfig {
	return n.config
}

// Run executes the memory operation and stores results in the envelope.
func (n *MemoryNode) Run(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	switch n.config.Mode {
	case MemoryModeRetrieve:
		return n.runRetrieve(ctx, env)
	case MemoryModeWrite:
		return n.runWrite(ctx, env)
	default:
		return nil, fmt.Errorf("memory node %s: unknown mode %q", n.ID(), n.config.Mode)
	}
}

// runRetrieve reads memory, assembles it within budget, and injects it.
func (n *MemoryNode) runRetrieve(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	emit := runtime.EmitterFromContext(ctx)

	if n.config.Provider == nil {
		return nil, fmt.Errorf("memory node %s: no provider configured", n.ID())
	}
	if n.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, n.config.Timeout)
		defer cancel()
	}

	scope, err := n.resolveScope(ctx, env)
	if err != nil {
		return nil, err
	}
	query, err := n.buildQuery(env)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	docs, retrieveErr := n.config.Provider.Retrieve(ctx, memory.RetrievalQuery{
		Scope:     scope,
		Query:     query,
		Limit:     n.config.Budget.MaxDocuments,
		MaxTokens: n.config.Budget.MaxTokens,
	})
	latency := time.Since(start)

	unavailable := false
	if retrieveErr != nil {
		if !errors.Is(retrieveErr, memory.ErrUnavailable) || n.config.OnUnavailable != MemoryUnavailableSkip {
			return nil, fmt.Errorf("memory node %s: retrieval failed: %w", n.ID(), retrieveErr)
		}
		unavailable = true
	}

	var kept []memory.Document
	var stats memory.CompactionStats
	if !unavailable {
		kept, stats, err = memory.Assemble(ctx, docs, n.config.Budget, n.config.Compactor, n.config.TokenCounter)
		if err != nil {
			return nil, fmt.Errorf("memory node %s: context assembly failed: %w", n.ID(), err)
		}
	}

	result := MemoryResult{
		Mode:          MemoryModeRetrieve,
		Namespace:     scope.Namespace,
		SessionID:     scope.SessionID,
		ThreadID:      scope.ThreadID,
		TenantID:      scope.TenantID,
		DocumentCount: len(kept),
	}
	if stats.Strategy != "" {
		compaction := stats
		result.Compaction = &compaction
		result.EstimatedTokens = stats.TokensOut
		result.Truncated = stats.Truncated
	}

	contextText := memory.RenderContext(kept)

	emitEvent := runtime.NewEvent(runtime.EventMemoryRead, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("namespace", scope.Namespace).
		WithPayload("session_id", scope.SessionID).
		WithPayload("query_length", len(query)).
		WithPayload("document_count", len(kept)).
		WithPayload("latency_ms", latency.Milliseconds())
	if scope.ThreadID != "" {
		emitEvent = emitEvent.WithPayload("thread_id", scope.ThreadID)
	}
	if scope.TenantID != "" {
		emitEvent = emitEvent.WithPayload("tenant_id", scope.TenantID)
	}
	if unavailable {
		emitEvent = emitEvent.WithPayload("unavailable", true)
	}
	if n.config.IncludeContentInEvents {
		emitEvent = emitEvent.WithPayload("documents", documentsForEvents(kept))
	}
	emit(emitEvent)

	if !unavailable {
		emit(runtime.NewEvent(runtime.EventContextAssembled, env.Trace.RunID).
			WithNode(n.ID(), n.Kind()).
			WithPayload("documents_in", stats.DocumentsIn).
			WithPayload("documents_out", stats.DocumentsOut).
			WithPayload("tokens_in", stats.TokensIn).
			WithPayload("tokens_out", stats.TokensOut).
			WithPayload("truncated", stats.Truncated).
			WithPayload("strategy", stats.Strategy).
			WithPayload("max_tokens", n.config.Budget.MaxTokens).
			WithPayload("max_documents", n.config.Budget.MaxDocuments))
	}

	resultEnv := env.Clone()
	resultEnv.SetVar(n.config.OutputVar, contextText)
	resultEnv.SetVar(n.config.OutputVar+"_stats", result)
	if !n.config.OmitArtifacts && !unavailable {
		for _, doc := range kept {
			resultEnv.AppendArtifact(core.Artifact{
				ID:       doc.ID,
				Type:     "chunk",
				MimeType: "text/plain",
				Text:     doc.Text,
				Meta: map[string]any{
					"source":     doc.Source,
					"score":      doc.Score,
					"namespace":  scope.Namespace,
					"session_id": scope.SessionID,
				},
			})
		}
	}
	return resultEnv, nil
}

// runWrite persists envelope content and records the write observably.
func (n *MemoryNode) runWrite(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	emit := runtime.EmitterFromContext(ctx)

	if n.config.Provider == nil {
		return nil, fmt.Errorf("memory node %s: no provider configured", n.ID())
	}
	if n.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, n.config.Timeout)
		defer cancel()
	}

	scope, err := n.resolveScope(ctx, env)
	if err != nil {
		return nil, err
	}
	content := n.writeContent(env)
	key, err := renderEnvelopeTemplate("write_key", n.config.WriteKey, env)
	if err != nil {
		return nil, fmt.Errorf("memory node %s: %w", n.ID(), err)
	}

	start := time.Now()
	writeErr := n.config.Provider.Write(ctx, memory.WriteRequest{
		Scope: scope,
		Key:   key,
		Text:  content,
		Metadata: map[string]any{
			"source_node": n.ID(),
			"run_id":      env.Trace.RunID,
		},
	})
	latency := time.Since(start)

	unavailable := false
	if writeErr != nil {
		if !errors.Is(writeErr, memory.ErrUnavailable) || n.config.OnUnavailable != MemoryUnavailableSkip {
			return nil, fmt.Errorf("memory node %s: write failed: %w", n.ID(), writeErr)
		}
		unavailable = true
	}

	emitEvent := runtime.NewEvent(runtime.EventMemoryWrite, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("namespace", scope.Namespace).
		WithPayload("session_id", scope.SessionID).
		WithPayload("byte_count", len(content)).
		WithPayload("latency_ms", latency.Milliseconds())
	if scope.ThreadID != "" {
		emitEvent = emitEvent.WithPayload("thread_id", scope.ThreadID)
	}
	if scope.TenantID != "" {
		emitEvent = emitEvent.WithPayload("tenant_id", scope.TenantID)
	}
	if unavailable {
		emitEvent = emitEvent.WithPayload("unavailable", true)
	}
	if n.config.IncludeContentInEvents {
		emitEvent = emitEvent.WithPayload("content", content)
	}
	emit(emitEvent)

	resultEnv := env.Clone()
	resultEnv.SetVar(n.config.OutputVar+"_stats", MemoryResult{
		Mode:        MemoryModeWrite,
		Namespace:   scope.Namespace,
		SessionID:   scope.SessionID,
		ThreadID:    scope.ThreadID,
		TenantID:    scope.TenantID,
		Unavailable: unavailable,
	})
	return resultEnv, nil
}

// resolveScope builds the memory scope from the run scope plus per-node
// template overrides, falling back to env.Trace.RunID for the run field.
func (n *MemoryNode) resolveScope(ctx context.Context, env *core.Envelope) (memory.Scope, error) {
	base := runtime.ScopeFromContext(ctx)

	scope := memory.Scope{
		TenantID:  base.TenantID,
		Namespace: base.Namespace,
		SessionID: base.SessionID,
		ThreadID:  base.ThreadID,
		RunID:     env.Trace.RunID,
	}

	for _, field := range []struct {
		tmpl   string
		target *string
		label  string
	}{
		{n.config.Namespace, &scope.Namespace, "namespace"},
		{n.config.SessionID, &scope.SessionID, "session_id"},
		{n.config.ThreadID, &scope.ThreadID, "thread_id"},
		{n.config.TenantID, &scope.TenantID, "tenant_id"},
	} {
		if field.tmpl == "" {
			continue
		}
		rendered, err := renderEnvelopeTemplate("scope_"+field.label, field.tmpl, env)
		if err != nil {
			return memory.Scope{}, fmt.Errorf("memory node %s: %w", n.ID(), err)
		}
		*field.target = rendered
	}

	if err := scope.Validate(); err != nil {
		return memory.Scope{}, fmt.Errorf(
			"memory node %s: %v (set node namespace/session_id or pass RunOptions.MemoryScope)",
			n.ID(), err)
	}
	return scope, nil
}

// buildQuery renders the retrieval query from the template or input vars.
func (n *MemoryNode) buildQuery(env *core.Envelope) (string, error) {
	if n.config.QueryTemplate != "" {
		query, err := renderEnvelopeTemplate("query", n.config.QueryTemplate, env)
		if err != nil {
			return "", fmt.Errorf("memory node %s: %w", n.ID(), err)
		}
		return query, nil
	}

	var parts []string
	for _, varName := range n.config.InputVars {
		if val, ok := env.GetVar(varName); ok {
			parts = append(parts, toString(val))
		}
	}
	return strings.Join(parts, "\n"), nil
}

// writeContent selects the content to persist in write mode.
func (n *MemoryNode) writeContent(env *core.Envelope) string {
	if n.config.WriteVar != "" {
		if val, ok := env.GetVar(n.config.WriteVar); ok {
			return toString(val)
		}
		return ""
	}
	if env.Input != nil {
		return toString(env.Input)
	}
	return ""
}

// documentsForEvents renders documents for event payloads. Callers gate this
// behind IncludeContentInEvents; the returned form still carries only the
// text and provenance, never envelope data.
func documentsForEvents(docs []memory.Document) []map[string]any {
	out := make([]map[string]any, 0, len(docs))
	for _, doc := range docs {
		entry := map[string]any{
			"id":   doc.ID,
			"text": doc.Text,
		}
		if doc.Source != "" {
			entry["source"] = doc.Source
		}
		out = append(out, entry)
	}
	return out
}

// renderEnvelopeTemplate executes a template against {{.input}} and
// {{.vars.*}}, mirroring cache key rendering.
func renderEnvelopeTemplate(name, text string, env *core.Envelope) (string, error) {
	if text == "" {
		return "", nil
	}
	tmpl, err := template.New(name).Parse(text)
	if err != nil {
		return "", fmt.Errorf("invalid %s template: %w", name, err)
	}

	data := map[string]any{
		"input": env.Input,
		"vars":  env.Vars,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("%s template execution failed: %w", name, err)
	}
	return buf.String(), nil
}

// Ensure interface compliance at compile time.
var _ core.Node = (*MemoryNode)(nil)
