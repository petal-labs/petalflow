package nodes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/memory"
	"github.com/petal-labs/petalflow/runtime"
)

// MemoryRecallNodeConfig configures a MemoryRecallNode.
//
// The node reads the run's memory.Scope from the context (set through
// runtime.RunOptions.Scope). At least one of Memory or Knowledge must be set.
type MemoryRecallNodeConfig struct {
	// Memory recalls conversation history for the scoped session. Optional.
	Memory memory.MemoryProvider

	// Knowledge retrieves documents relevant to the query. Optional.
	Knowledge memory.KnowledgeProvider

	// Namespace overrides the run scope's namespace for both memory and
	// knowledge. Empty keeps the run scope.
	Namespace string

	// KnowledgeNamespace overrides the namespace used for knowledge retrieval
	// only, so a workflow can read a shared knowledge base while keeping
	// conversation memory in its own namespace.
	KnowledgeNamespace string

	// QueryVar names the envelope variable holding the retrieval query. When
	// empty or unset, knowledge retrieval is skipped.
	QueryVar string

	// HistoryLimit bounds the number of messages recalled (most recent).
	// Zero uses memory.DefaultRecallLimit.
	HistoryLimit int

	// Roles optionally restricts recalled history to these message roles.
	Roles []string

	// TopK bounds knowledge results. Zero uses memory.DefaultTopK.
	TopK int

	// Collections optionally restricts retrieval to named collections.
	Collections []string

	// MinScore drops knowledge hits below this relevance score.
	MinScore float64

	// Budget bounds the assembled context (history + artifacts). A zero
	// budget keeps everything the provider returned.
	Budget memory.Budget

	// TokenCounter estimates tokens for the budget; nil uses the default.
	TokenCounter memory.TokenCounter

	// Compactor reduces over-budget history; nil truncates oldest turns.
	Compactor memory.Compactor

	// OutputVar receives the rendered context block (retrieved artifacts as a
	// numbered list). Defaults to "<id>_context". The provider summary, if
	// any, is stored under "<OutputVar>_summary" and assembly statistics
	// under "<OutputVar>_stats".
	OutputVar string

	// RecordMessages appends the retained history to envelope.Messages, tagged
	// with Meta["source"]="memory", so an LLM node with IncludeMessages sends
	// it as prior turns and a MemoryStoreNode can skip re-storing it.
	RecordMessages bool

	// OnUnavailable selects behavior when the provider is unavailable. The
	// default fails the node. FailurePolicyContinue completes with empty
	// context and records a core.NodeError on the envelope.
	OnUnavailable memory.FailurePolicy

	// Timeout bounds the combined provider calls. Zero uses
	// memory.DefaultOperationTimeout.
	Timeout time.Duration

	// RecordContent opts in to including the query text in the memory.recall
	// event. Off by default so events never carry user content.
	RecordContent bool
}

// MemoryRecallNode loads conversation history and relevant knowledge for the
// run's scope, fits them to a budget, and exposes them to downstream nodes.
type MemoryRecallNode struct {
	core.BaseNode
	config MemoryRecallNodeConfig
}

// NewMemoryRecallNode creates a memory recall node.
func NewMemoryRecallNode(id string, config MemoryRecallNodeConfig) *MemoryRecallNode {
	if config.OutputVar == "" {
		config.OutputVar = id + "_context"
	}
	if config.Timeout == 0 {
		config.Timeout = memory.DefaultOperationTimeout
	}
	if config.OnUnavailable == "" {
		config.OnUnavailable = memory.FailurePolicyFail
	}
	return &MemoryRecallNode{
		BaseNode: core.NewBaseNode(id, core.NodeKindMemory),
		config:   config,
	}
}

// Config returns the node's configuration.
func (n *MemoryRecallNode) Config() MemoryRecallNodeConfig { return n.config }

// Run executes recall and retrieval and stores the assembled context.
func (n *MemoryRecallNode) Run(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	if n.config.Memory == nil && n.config.Knowledge == nil {
		return nil, fmt.Errorf("memory recall node %q: no memory or knowledge provider configured", n.ID())
	}
	emit := runtime.EmitterFromContext(ctx)
	scope, err := n.scope(ctx)
	if err != nil {
		return nil, fmt.Errorf("memory recall node %q: %w", n.ID(), err)
	}

	opCtx, cancel := context.WithTimeout(ctx, n.config.Timeout)
	defer cancel()

	query := ""
	if n.config.QueryVar != "" {
		query = envString(env, n.config.QueryVar)
	}

	started := time.Now()
	var (
		history  []core.Message
		summary  string
		hits     []memory.SearchHit
		degraded error
	)

	if n.config.Memory != nil {
		res, recallErr := n.config.Memory.Recall(opCtx, memory.RecallRequest{
			Scope: scope,
			Limit: n.config.HistoryLimit,
			Roles: n.config.Roles,
		})
		if recallErr != nil {
			if !n.degradable(recallErr) {
				n.emitRecall(emit, env, scope, "error", recallErr, 0, 0, started, query)
				return nil, fmt.Errorf("memory recall node %q: recall: %w", n.ID(), recallErr)
			}
			degraded = recallErr
		} else {
			history = res.Messages
			summary = res.Summary
		}
	}

	if n.config.Knowledge != nil && query != "" {
		res, searchErr := n.config.Knowledge.Search(opCtx, memory.SearchRequest{
			Scope:       scope.WithNamespace(n.config.KnowledgeNamespace),
			Query:       query,
			TopK:        n.config.TopK,
			Collections: n.config.Collections,
			MinScore:    n.config.MinScore,
		})
		if searchErr != nil {
			if !n.degradable(searchErr) {
				n.emitRecall(emit, env, scope, "error", searchErr, len(history), 0, started, query)
				return nil, fmt.Errorf("memory recall node %q: search: %w", n.ID(), searchErr)
			}
			if degraded == nil {
				degraded = searchErr
			}
		} else {
			hits = res.Hits
		}
	}

	artifacts := make([]core.Artifact, 0, len(hits))
	for _, h := range hits {
		a := h.Artifact
		if a.Type == "" {
			a.Type = "retrieval"
		}
		meta := make(map[string]any, len(a.Meta)+2)
		for k, v := range a.Meta {
			meta[k] = v
		}
		meta["score"] = h.Score
		meta["retrieved_by"] = n.ID()
		a.Meta = meta
		artifacts = append(artifacts, a)
	}

	assembly, err := memory.Assemble(ctx, memory.AssembleInput{
		History:   history,
		Artifacts: artifacts,
		Budget:    n.config.Budget,
		Counter:   n.config.TokenCounter,
		Compactor: n.config.Compactor,
	})
	if err != nil {
		return nil, fmt.Errorf("memory recall node %q: %w", n.ID(), err)
	}

	status := "ok"
	if degraded != nil {
		status = "unavailable"
	}
	n.emitRecall(emit, env, scope, status, degraded, len(assembly.Messages), len(assembly.Artifacts), started, query)
	emitAssembly(emit, env, n.ID(), n.Kind(), assembly.Stats)

	env.SetVar(n.config.OutputVar, assembly.ContextText)
	env.SetVar(n.config.OutputVar+"_stats", assembly.Stats.Payload())
	if summary != "" {
		env.SetVar(n.config.OutputVar+"_summary", summary)
	}
	for _, a := range assembly.Artifacts {
		env.AppendArtifact(a)
	}
	if n.config.RecordMessages {
		for _, m := range assembly.Messages {
			m = tagMemoryMessage(m)
			env.AppendMessage(m)
		}
	}
	if degraded != nil {
		env.AppendError(core.NodeError{
			NodeID:  n.ID(),
			Kind:    n.Kind(),
			Message: "memory provider unavailable; continuing without recalled context",
			Attempt: 1,
			At:      time.Now(),
			Details: map[string]any{"status": "unavailable", "error_class": errorClass(degraded)},
			Cause:   degraded,
		})
	}
	return env, nil
}

func (n *MemoryRecallNode) scope(ctx context.Context) (memory.Scope, error) {
	scope, ok := memory.ScopeFromContext(ctx)
	if !ok {
		return memory.Scope{}, memory.ErrScopeRequired
	}
	scope = scope.WithNamespace(n.config.Namespace)
	if n.config.Memory != nil {
		if err := scope.ValidateConversation(); err != nil {
			return memory.Scope{}, err
		}
	} else if err := scope.Validate(); err != nil {
		return memory.Scope{}, err
	}
	return scope, nil
}

func (n *MemoryRecallNode) degradable(err error) bool {
	return n.config.OnUnavailable == memory.FailurePolicyContinue && memory.IsUnavailable(err)
}

func (n *MemoryRecallNode) emitRecall(
	emit runtime.EventEmitter,
	env *core.Envelope,
	scope memory.Scope,
	status string,
	err error,
	historyCount, retrievedCount int,
	started time.Time,
	query string,
) {
	provider := ""
	if n.config.Memory != nil {
		provider = memory.ProviderName(n.config.Memory)
	}
	if n.config.Knowledge != nil {
		kp := memory.ProviderName(n.config.Knowledge)
		if provider == "" {
			provider = kp
		} else if kp != provider {
			provider += "," + kp
		}
	}
	e := runtime.NewEvent(runtime.EventMemoryRecall, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("provider", provider).
		WithPayload("status", status).
		WithPayload("history_count", historyCount).
		WithPayload("retrieved_count", retrievedCount).
		WithPayload("latency_ms", time.Since(started).Milliseconds())
	e = e.WithScope(scope)
	if err != nil {
		e = e.WithPayload("error_class", errorClass(err))
	}
	if n.config.RecordContent && query != "" {
		e = e.WithPayload("query", query)
	}
	emit(e)
}

// MemoryStoreEntry names an envelope variable to persist as a message.
type MemoryStoreEntry struct {
	// Role is the message role, e.g. "user" or "assistant".
	Role string
	// Var is the envelope variable holding the content. Missing or empty
	// values are skipped.
	Var string
	// Name optionally sets Message.Name.
	Name string
}

// MemoryStoreNodeConfig configures a MemoryStoreNode.
type MemoryStoreNodeConfig struct {
	// Memory is the provider to write to. Required.
	Memory memory.MemoryProvider

	// Namespace overrides the run scope's namespace. Empty keeps the run scope.
	Namespace string

	// Entries are variables to persist, in order.
	Entries []MemoryStoreEntry

	// IncludeNewMessages also persists envelope.Messages produced during this
	// run (those not tagged Meta["source"]="memory" by a recall node).
	IncludeNewMessages bool

	// Metadata is low-cardinality metadata stored with every message.
	Metadata map[string]string

	// OutputVar receives the number of messages stored. Defaults to
	// "<id>_stored".
	OutputVar string

	// OnUnavailable selects behavior when the provider is unavailable. The
	// default fails the node; FailurePolicyContinue records a core.NodeError
	// and continues.
	OnUnavailable memory.FailurePolicy

	// Timeout bounds the provider call. Zero uses memory.DefaultOperationTimeout.
	Timeout time.Duration
}

// MemoryStoreNode persists conversation turns to the run's scoped memory.
type MemoryStoreNode struct {
	core.BaseNode
	config MemoryStoreNodeConfig
}

// NewMemoryStoreNode creates a memory store node.
func NewMemoryStoreNode(id string, config MemoryStoreNodeConfig) *MemoryStoreNode {
	if config.OutputVar == "" {
		config.OutputVar = id + "_stored"
	}
	if config.Timeout == 0 {
		config.Timeout = memory.DefaultOperationTimeout
	}
	if config.OnUnavailable == "" {
		config.OnUnavailable = memory.FailurePolicyFail
	}
	return &MemoryStoreNode{
		BaseNode: core.NewBaseNode(id, core.NodeKindMemory),
		config:   config,
	}
}

// Config returns the node's configuration.
func (n *MemoryStoreNode) Config() MemoryStoreNodeConfig { return n.config }

// Run writes the configured entries to memory.
func (n *MemoryStoreNode) Run(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	if n.config.Memory == nil {
		return nil, fmt.Errorf("memory store node %q: no memory provider configured", n.ID())
	}
	emit := runtime.EmitterFromContext(ctx)
	scope, ok := memory.ScopeFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("memory store node %q: %w", n.ID(), memory.ErrScopeRequired)
	}
	scope = scope.WithNamespace(n.config.Namespace)
	if err := scope.ValidateConversation(); err != nil {
		return nil, fmt.Errorf("memory store node %q: %w", n.ID(), err)
	}

	messages := n.collect(env)
	if len(messages) == 0 {
		env.SetVar(n.config.OutputVar, 0)
		n.emitStore(emit, env, scope, "ok", nil, 0, 0, time.Now())
		return env, nil
	}

	opCtx, cancel := context.WithTimeout(ctx, n.config.Timeout)
	defer cancel()

	started := time.Now()
	res, err := n.config.Memory.Remember(opCtx, memory.RememberRequest{
		Scope:    scope,
		Messages: messages,
		Metadata: n.config.Metadata,
	})
	if err != nil {
		if n.config.OnUnavailable != memory.FailurePolicyContinue || !memory.IsUnavailable(err) {
			n.emitStore(emit, env, scope, "error", err, len(messages), 0, started)
			return nil, fmt.Errorf("memory store node %q: remember: %w", n.ID(), err)
		}
		n.emitStore(emit, env, scope, "unavailable", err, len(messages), 0, started)
		env.SetVar(n.config.OutputVar, 0)
		env.AppendError(core.NodeError{
			NodeID:  n.ID(),
			Kind:    n.Kind(),
			Message: "memory provider unavailable; conversation turns were not stored",
			Attempt: 1,
			At:      time.Now(),
			Details: map[string]any{"status": "unavailable", "error_class": errorClass(err), "message_count": len(messages)},
			Cause:   err,
		})
		return env, nil
	}

	n.emitStore(emit, env, scope, "ok", nil, len(messages), res.Stored, started)
	env.SetVar(n.config.OutputVar, res.Stored)
	return env, nil
}

func (n *MemoryStoreNode) collect(env *core.Envelope) []core.Message {
	var messages []core.Message
	for _, entry := range n.config.Entries {
		content := envString(env, entry.Var)
		if content == "" {
			continue
		}
		role := entry.Role
		if role == "" {
			role = "user"
		}
		messages = append(messages, core.Message{Role: role, Content: content, Name: entry.Name})
	}
	if n.config.IncludeNewMessages {
		for _, m := range env.Messages {
			if isMemoryMessage(m) {
				continue
			}
			messages = append(messages, m)
		}
	}
	return messages
}

func (n *MemoryStoreNode) emitStore(
	emit runtime.EventEmitter,
	env *core.Envelope,
	scope memory.Scope,
	status string,
	err error,
	messageCount, storedCount int,
	started time.Time,
) {
	e := runtime.NewEvent(runtime.EventMemoryStore, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("provider", memory.ProviderName(n.config.Memory)).
		WithPayload("status", status).
		WithPayload("message_count", messageCount).
		WithPayload("stored_count", storedCount).
		WithPayload("latency_ms", time.Since(started).Milliseconds())
	e = e.WithScope(scope)
	if err != nil {
		e = e.WithPayload("error_class", errorClass(err))
	}
	emit(e)
}

// --- shared helpers ---

// emitAssembly emits context.assembled, and context.compacted when history
// was reduced to fit the budget.
func emitAssembly(emit runtime.EventEmitter, env *core.Envelope, nodeID string, kind core.NodeKind, stats memory.AssemblyStats) {
	e := runtime.NewEvent(runtime.EventContextAssembled, env.Trace.RunID).WithNode(nodeID, kind)
	for k, v := range stats.Payload() {
		e = e.WithPayload(k, v)
	}
	emit(e)
	if stats.Compacted {
		emit(runtime.NewEvent(runtime.EventContextCompacted, env.Trace.RunID).
			WithNode(nodeID, kind).
			WithPayload("history_dropped", stats.HistoryDropped).
			WithPayload("summarized", stats.Summarized).
			WithPayload("max_tokens", stats.MaxTokens))
	}
}

// errorClass returns a low-cardinality label for an error, safe for events.
func errorClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, memory.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, memory.ErrInvalidScope):
		return "invalid_scope"
	case errors.Is(err, memory.ErrBudgetExceeded):
		return "budget_exceeded"
	default:
		return "error"
	}
}

func tagMemoryMessage(m core.Message) core.Message {
	meta := make(map[string]any, len(m.Meta)+1)
	for k, v := range m.Meta {
		meta[k] = v
	}
	meta["source"] = "memory"
	m.Meta = meta
	return m
}

func isMemoryMessage(m core.Message) bool {
	src, _ := m.Meta["source"].(string)
	return src == "memory"
}

// envString resolves a (possibly dotted) variable name to trimmed text.
// Missing and nil values yield "".
func envString(env *core.Envelope, name string) string {
	if name == "" {
		return ""
	}
	v, ok := env.GetVar(name)
	if !ok {
		v, ok = env.GetVarNested(name)
	}
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(toString(v))
}

// Ensure interface compliance at compile time.
var (
	_ core.Node = (*MemoryRecallNode)(nil)
	_ core.Node = (*MemoryStoreNode)(nil)
)
