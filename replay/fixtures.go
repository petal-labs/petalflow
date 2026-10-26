package replay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

// FixtureMissPolicy defines what happens when a replay interaction has no
// matching fixture.
type FixtureMissPolicy string

const (
	// FixtureMissFail fails the interaction (default). Replay is
	// deterministic and never silently invokes live systems.
	FixtureMissFail FixtureMissPolicy = "fail"

	// FixtureMissLive falls through to the live client or tool. Requires a
	// live dependency and breaks determinism; use only for hybrid runs.
	FixtureMissLive FixtureMissPolicy = "live"
)

// FixtureStore holds recorded interactions for deterministic offline replay.
//
// Matching strategy: when the recording was captured with full fidelity
// (AllowAllPolicy), fixtures match by input fingerprint — model, prompts,
// and input text for LLM calls, argument maps for tools — falling back to
// recorded order. For redacted recordings the prompts are not available, so
// fixtures match strictly in recorded order. Each fixture is consumed once,
// which keeps replay aligned with the recorded sequence even across loops
// and parallel branches.
type FixtureStore struct {
	mu         sync.Mutex
	llm        []llmFixture
	llmCursor  int
	tools      map[string][]toolFixture
	toolCursor map[string]int
}

type llmFixture struct {
	NodeID      string
	Model       string
	System      string
	InputText   string
	Messages    []LLMMessageRef
	Completion  string
	StopReason  string
	Usage       UsageRecord
	fingerprint string // empty when the recording was redacted
}

type toolFixture struct {
	NodeID      string
	ToolName    string
	Args        map[string]any
	Result      map[string]any
	IsError     bool
	Error       string
	fingerprint string
}

// NewFixtureStore derives fixtures from a recording. Full-fidelity
// recordings yield fingerprint-matched fixtures; redacted recordings yield
// order-matched fixtures.
func NewFixtureStore(rec *Recording) *FixtureStore {
	store := &FixtureStore{tools: make(map[string][]toolFixture)}

	for _, node := range rec.Nodes {
		for _, call := range node.LLM {
			fixture := llmFixture{
				NodeID:     node.NodeID,
				Model:      call.Model,
				System:     call.System,
				InputText:  call.InputText,
				Messages:   call.Messages,
				Completion: call.Completion,
				StopReason: call.StopReason,
				Usage:      call.Usage,
			}
			if isRedacted(call) {
				// Prompts were redacted: fingerprint would be a constant.
				fixture.fingerprint = ""
			} else {
				fixture.fingerprint = llmFingerprint(call.Model, call.System, call.InputText, call.Messages)
			}
			store.llm = append(store.llm, fixture)
		}

		for i := range node.Tools {
			call := node.Tools[i]
			fixture := toolFixture{
				NodeID:   call.NodeID,
				ToolName: call.ToolName,
				Args:     call.Args,
				Result:   call.Result,
				IsError:  call.IsError,
				Error:    call.Error,
			}
			if fixture.Args != nil {
				fixture.fingerprint = mapFingerprint(fixture.Args)
			}
			store.tools[call.ToolName] = append(store.tools[call.ToolName], fixture)
		}
	}
	return store
}

// isRedacted reports whether an LLM record's prompts are placeholders.
func isRedacted(call LLMCallRecord) bool {
	redactedSystem := call.System != "" && isPlaceholder(call.System)
	redactedInput := call.InputText != "" && isPlaceholder(call.InputText)
	for _, m := range call.Messages {
		if m.Content != "" && isPlaceholder(m.Content) {
			return true
		}
	}
	return redactedSystem || redactedInput
}

func isPlaceholder(s string) bool {
	return s == defaultPlaceholder
}

// llmFingerprint hashes the request-defining fields of an LLM call.
func llmFingerprint(model, system, inputText string, messages []LLMMessageRef) string {
	hasher := sha256.New()
	hasher.Write([]byte(model))
	hasher.Write([]byte{0})
	hasher.Write([]byte(system))
	hasher.Write([]byte{0})
	hasher.Write([]byte(inputText))
	hasher.Write([]byte{0})
	for _, m := range messages {
		hasher.Write([]byte(m.Role))
		hasher.Write([]byte(m.Name))
		hasher.Write([]byte{0})
		hasher.Write([]byte(m.Content))
		hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// mapFingerprint hashes a map of arguments with sorted keys.
func mapFingerprint(args map[string]any) string {
	stable, err := json.Marshal(args)
	if err != nil {
		// Non-serializable args never match; fall back to order matching.
		return ""
	}
	hash := sha256.Sum256(stable)
	return hex.EncodeToString(hash[:])
}

// matchLLM returns the next unconsumed fixture for the request, using
// fingerprint match first, then recorded order.
func (s *FixtureStore) matchLLM(model, system, inputText string, messages []LLMMessageRef) (*llmFixture, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fingerprint := llmFingerprint(model, system, inputText, messages)

	// Pass 1: fingerprint match among unconsumed fixtures.
	for i := s.llmCursor; i < len(s.llm); i++ {
		if s.llm[i].fingerprint == fingerprint {
			s.llmCursor = i + 1
			return &s.llm[i], true
		}
	}
	// Pass 2: recorded order (redacted recordings carry no fingerprint, so
	// fingerprint equality cannot be trusted there).
	for i := s.llmCursor; i < len(s.llm); i++ {
		if s.llm[i].fingerprint == "" {
			s.llmCursor = i + 1
			return &s.llm[i], true
		}
	}
	return nil, false
}

// matchTool returns the next unconsumed fixture for the named tool,
// preferring an argument fingerprint match.
func (s *FixtureStore) matchTool(toolName string, args map[string]any) (*toolFixture, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.toolCursor == nil {
		s.toolCursor = make(map[string]int)
	}

	list := s.tools[toolName]
	cursor := s.toolCursor[toolName]
	if cursor >= len(list) {
		return nil, false
	}

	fingerprint := mapFingerprint(args)
	if fingerprint != "" {
		for i := cursor; i < len(list); i++ {
			if list[i].fingerprint == fingerprint {
				s.toolCursor[toolName] = i + 1
				return &list[i], true
			}
		}
	}
	// Recorded order fallback.
	if cursor < len(list) {
		fixture := &list[cursor]
		s.toolCursor[toolName] = cursor + 1
		return fixture, true
	}
	return nil, false
}

// FixtureLLMClient is a deterministic core.LLMClient backed by a
// FixtureStore. Construct it when building a graph for offline replay:
//
//	client := replay.NewFixtureLLMClient(store, replay.FixtureMissFail, nil)
//	node := nodes.NewLLMNode("answer", client, config)
//
// The client emits llm.call / llm.response events during replay so the
// replayed run can be captured and verified. Event attribution uses the
// node ID set via ForNode.
type FixtureLLMClient struct {
	store  *FixtureStore
	policy FixtureMissPolicy
	live   core.LLMClient // used when policy is FixtureMissLive
	nodeID string         // event attribution
}

// NewFixtureLLMClient creates a fixture-backed LLM client. For
// FixtureMissLive, pass a live client to fall through to on mismatch.
func NewFixtureLLMClient(store *FixtureStore, policy FixtureMissPolicy, live core.LLMClient) *FixtureLLMClient {
	if policy == "" {
		policy = FixtureMissFail
	}
	return &FixtureLLMClient{store: store, policy: policy, live: live}
}

// ForNode returns a copy of the client that attributes emitted LLM events
// to the given node ID, so per-node capture can record the interaction.
// Use one copy per LLM node when building the replay graph.
func (c *FixtureLLMClient) ForNode(nodeID string) *FixtureLLMClient {
	copied := *c
	copied.nodeID = nodeID
	return &copied
}

// Complete replays the recorded completion for the request.
func (c *FixtureLLMClient) Complete(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	if err := ctx.Err(); err != nil {
		return core.LLMResponse{}, err
	}

	emit := runtime.EmitterFromContext(ctx)
	runID := envRunIDFromContext(ctx)

	var messages []LLMMessageRef
	for _, m := range req.Messages {
		messages = append(messages, LLMMessageRef{Role: m.Role, Name: m.Name, Content: m.Content})
	}

	fixture, matched := c.store.matchLLM(req.Model, req.System, req.InputText, messages)
	if !matched {
		resp, err := c.miss(ctx, req)
		if err != nil {
			emit(c.replayEvent(runID, runtime.EventLLMResponse, req.Model).
				WithPayload("error", err.Error()).
				WithPayload("status", "error"))
		}
		return resp, err
	}

	resp := core.LLMResponse{
		Text:   fixture.Completion,
		Model:  req.Model,
		Status: fixture.StopReason,
		Usage: core.LLMTokenUsage{
			InputTokens:         fixture.Usage.InputTokens,
			OutputTokens:        fixture.Usage.OutputTokens,
			TotalTokens:         fixture.Usage.TotalTokens,
			CostUSD:             fixture.Usage.CostUSD,
			CacheReadTokens:     fixture.Usage.CacheReadTokens,
			CacheCreationTokens: fixture.Usage.CacheCreationTokens,
		},
	}

	emit(c.replayEvent(runID, runtime.EventLLMCall, req.Model).
		WithPayload("system_prompt", req.System).
		WithPayload("input_text", req.InputText))
	emit(c.replayEvent(runID, runtime.EventLLMResponse, req.Model).
		WithPayload("completion", resp.Text).
		WithPayload("stop_reason", resp.Status).
		WithPayload("input_tokens", resp.Usage.InputTokens).
		WithPayload("output_tokens", resp.Usage.OutputTokens).
		WithPayload("total_tokens", resp.Usage.TotalTokens))
	return resp, nil
}

// replayEvent builds an LLM event attributed to the client's node.
func (c *FixtureLLMClient) replayEvent(runID string, kind runtime.EventKind, model string) runtime.Event {
	event := runtime.NewEvent(kind, runID).WithPayload("model", model)
	if c.nodeID != "" {
		event = event.WithNode(c.nodeID, core.NodeKindLLM)
	}
	return event
}

// envRunIDFromContext extracts the run ID from the envelope trace, which
// the runtime sets before node execution. LLM clients receive the node
// context, so we recover the run ID from the emitter-bearing context chain
// via the request meta set by nodes (or empty for run-less contexts).
func envRunIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(replayRunIDKey{}).(string); ok {
		return id
	}
	return ""
}

// replayRunIDKey is the context key the Replayer uses to thread the replay
// run ID through to fixture clients for event attribution.
type replayRunIDKey struct{}

// miss applies the mismatch policy for an unmatched LLM request.
func (c *FixtureLLMClient) miss(ctx context.Context, req core.LLMRequest) (core.LLMResponse, error) {
	switch c.policy {
	case FixtureMissLive:
		if c.live == nil {
			return core.LLMResponse{}, fmt.Errorf("%w: no live LLM client for fallback", ErrNoFixture)
		}
		return c.live.Complete(ctx, req)
	default:
		return core.LLMResponse{}, fmt.Errorf(
			"%w: no LLM fixture matches request (model=%q, input_text=%d chars); re-record with full-fidelity capture or use FixtureMissLive",
			ErrNoFixture, req.Model, len(req.InputText))
	}
}

// FixtureTool is a deterministic core.PetalTool backed by a FixtureStore.
// When inner is set and the policy is FixtureMissLive, unmatched calls fall
// through to the real tool.
type FixtureTool struct {
	inner  core.PetalTool
	store  *FixtureStore
	policy FixtureMissPolicy
}

// NewFixtureTool wraps a tool for offline replay. inner may be nil when no
// live fallback is possible.
func NewFixtureTool(inner core.PetalTool, store *FixtureStore, policy FixtureMissPolicy) *FixtureTool {
	if policy == "" {
		policy = FixtureMissFail
	}
	return &FixtureTool{inner: inner, store: store, policy: policy}
}

// Name implements core.PetalTool.
func (t *FixtureTool) Name() string {
	if t.inner != nil {
		return t.inner.Name()
	}
	return "fixture"
}

// Invoke replays the recorded result for the arguments.
func (t *FixtureTool) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	fixture, ok := t.store.matchTool(t.Name(), args)
	if !ok {
		switch t.policy {
		case FixtureMissLive:
			if t.inner == nil {
				return nil, fmt.Errorf("%w: no live tool %q for fallback", ErrNoFixture, t.Name())
			}
			return t.inner.Invoke(ctx, args)
		default:
			return nil, fmt.Errorf("%w: no fixture matches tool %q arguments; re-record with full-fidelity capture or use FixtureMissLive", ErrNoFixture, t.Name())
		}
	}

	if fixture.IsError {
		if fixture.Error != "" {
			return nil, fmt.Errorf("recorded tool error: %s", fixture.Error)
		}
		return nil, fmt.Errorf("recorded tool error")
	}
	return fixture.Result, nil
}

// ErrNoFixture signals a replay interaction with no matching fixture.
var ErrNoFixture = errors.New("no replay fixture")

// ReplayToolRegistry wraps every tool in a registry with fixture-backed
// deterministic versions. Use it when building a graph for offline replay
// so tool nodes resolve fixture tools by name:
//
//	registry := replay.ReplayToolRegistry(liveRegistry, store, replay.FixtureMissFail)
func ReplayToolRegistry(inner *core.ToolRegistry, store *FixtureStore, policy FixtureMissPolicy) *core.ToolRegistry {
	if inner == nil {
		return nil
	}
	wrapped := core.NewToolRegistry()
	for _, name := range inner.List() {
		tool, ok := inner.Get(name)
		if !ok {
			continue
		}
		wrapped.Register(NewFixtureTool(tool, store, policy))
	}
	return wrapped
}
