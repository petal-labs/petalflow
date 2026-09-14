package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/petal-labs/petalflow/core"
)

// Budget bounds how much recalled and retrieved context may enter a prompt.
// Zero values mean "unbounded" for that dimension; callers that want a hard
// cap must set MaxTokens.
type Budget struct {
	// MaxTokens caps the estimated tokens of system prompt + current prompt +
	// retrieved artifacts + history combined.
	MaxTokens int `json:"max_tokens,omitempty"`
	// MaxMessages caps the number of history messages retained (most recent).
	MaxMessages int `json:"max_messages,omitempty"`
	// MaxArtifacts caps the number of retrieved artifacts retained (highest
	// relevance first).
	MaxArtifacts int `json:"max_artifacts,omitempty"`
}

// IsZero reports whether the budget imposes no limits.
func (b Budget) IsZero() bool {
	return b.MaxTokens == 0 && b.MaxMessages == 0 && b.MaxArtifacts == 0
}

// ErrBudgetExceeded is returned when the fixed parts of a prompt (system
// prompt and current user turn) alone exceed Budget.MaxTokens, so no amount
// of compaction can make the request fit.
var ErrBudgetExceeded = errors.New("context budget exceeded")

// TokenCounter estimates the token cost of text. Providers can supply an exact
// tokenizer; the default is a conservative character heuristic.
type TokenCounter interface {
	Count(text string) int
}

// TokenCounterFunc adapts a function to TokenCounter.
type TokenCounterFunc func(text string) int

// Count implements TokenCounter.
func (f TokenCounterFunc) Count(text string) int { return f(text) }

// HeuristicCounter estimates tokens as ceil(characters / CharsPerToken). It is
// tokenizer-agnostic and errs on the side of over-counting for English text.
type HeuristicCounter struct {
	CharsPerToken int
}

// DefaultCharsPerToken is the ratio used when HeuristicCounter is zero-valued.
const DefaultCharsPerToken = 4

// Count implements TokenCounter.
func (c HeuristicCounter) Count(text string) int {
	if text == "" {
		return 0
	}
	per := c.CharsPerToken
	if per <= 0 {
		per = DefaultCharsPerToken
	}
	n := utf8.RuneCountInString(text)
	return (n + per - 1) / per
}

// DefaultTokenCounter returns the counter used when none is configured.
func DefaultTokenCounter() TokenCounter {
	return HeuristicCounter{CharsPerToken: DefaultCharsPerToken}
}

// MessageTokens estimates the cost of a message including a small per-message
// framing overhead, so budgets are not silently exceeded by role markers.
func MessageTokens(counter TokenCounter, m core.Message) int {
	const framing = 4
	return counter.Count(m.Content) + counter.Count(m.Name) + framing
}

// ArtifactTokens estimates the cost of an artifact's textual content.
func ArtifactTokens(counter TokenCounter, a core.Artifact) int {
	if a.Text != "" {
		return counter.Count(a.Text)
	}
	if len(a.Bytes) > 0 {
		return counter.Count(string(a.Bytes))
	}
	return 0
}

// Compactor reduces conversation history to fit a budget. The default
// (TruncateOldest) drops the oldest turns; a backend may summarize instead.
type Compactor interface {
	Compact(ctx context.Context, req CompactRequest) (CompactResult, error)
}

// CompactorFunc adapts a function to Compactor.
type CompactorFunc func(ctx context.Context, req CompactRequest) (CompactResult, error)

// Compact implements Compactor.
func (f CompactorFunc) Compact(ctx context.Context, req CompactRequest) (CompactResult, error) {
	return f(ctx, req)
}

// CompactRequest describes history that does not fit its budget.
type CompactRequest struct {
	// Messages is the full history, oldest first.
	Messages []core.Message
	// MaxTokens is the token budget available for history (0 = unlimited).
	MaxTokens int
	// MaxMessages is the message budget available for history (0 = unlimited).
	MaxMessages int
	// Counter is the token counter the assembler is using.
	Counter TokenCounter
}

// CompactResult is compacted history. Messages must fit the requested budget;
// the assembler truncates oldest turns if an implementation returns too much.
type CompactResult struct {
	Messages []core.Message
	// Dropped is the number of original messages not represented verbatim.
	Dropped int
	// Summarized reports that dropped content was folded into a summary
	// message rather than discarded.
	Summarized bool
}

// TruncateOldest is the default Compactor: it keeps the most recent messages
// that fit within the token and message budgets.
type TruncateOldest struct{}

// Compact implements Compactor.
func (TruncateOldest) Compact(_ context.Context, req CompactRequest) (CompactResult, error) {
	kept, dropped := truncateOldest(req.Messages, req.MaxTokens, req.MaxMessages, req.Counter)
	return CompactResult{Messages: kept, Dropped: dropped}, nil
}

func truncateOldest(messages []core.Message, maxTokens, maxMessages int, counter TokenCounter) ([]core.Message, int) {
	if counter == nil {
		counter = DefaultTokenCounter()
	}
	start := len(messages)
	tokens := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if maxMessages > 0 && len(messages)-i > maxMessages {
			break
		}
		cost := MessageTokens(counter, messages[i])
		if maxTokens > 0 && tokens+cost > maxTokens {
			break
		}
		tokens += cost
		start = i
	}
	kept := make([]core.Message, len(messages)-start)
	copy(kept, messages[start:])
	return kept, start
}

// AssembleInput is everything the assembler needs to fit context to a budget.
type AssembleInput struct {
	// System is the system prompt; it is always retained.
	System string
	// Prompt is the current user turn; it is always retained.
	Prompt string
	// History is prior conversation, oldest first.
	History []core.Message
	// Artifacts are retrieved items ordered by descending relevance.
	Artifacts []core.Artifact
	// Budget bounds the result. A zero budget retains everything.
	Budget Budget
	// Counter estimates tokens; nil selects DefaultTokenCounter.
	Counter TokenCounter
	// Compactor reduces history that exceeds the budget; nil selects
	// TruncateOldest.
	Compactor Compactor
}

// Assembly is budgeted context ready to be turned into a model request.
type Assembly struct {
	// Messages is the retained history, oldest first.
	Messages []core.Message
	// Artifacts is the retained retrieval set, highest relevance first.
	Artifacts []core.Artifact
	// ContextText renders Artifacts as a numbered block suitable for a prompt
	// template.
	ContextText string
	Stats       AssemblyStats
}

// AssemblyStats describes how the budget was spent. It contains counts and
// hashes only and is safe to emit in events.
type AssemblyStats struct {
	SystemTokens      int  `json:"system_tokens"`
	PromptTokens      int  `json:"prompt_tokens"`
	HistoryTokens     int  `json:"history_tokens"`
	ArtifactTokens    int  `json:"artifact_tokens"`
	TotalTokens       int  `json:"total_tokens"`
	MaxTokens         int  `json:"max_tokens,omitempty"`
	HistoryIncluded   int  `json:"history_included"`
	HistoryDropped    int  `json:"history_dropped"`
	ArtifactsIncluded int  `json:"artifacts_included"`
	ArtifactsDropped  int  `json:"artifacts_dropped"`
	Compacted         bool `json:"compacted"`
	Summarized        bool `json:"summarized,omitempty"`
	// PromptCacheKey is a stable hash of the system prompt and retained history
	// (the request prefix that providers can cache across turns).
	PromptCacheKey string `json:"prompt_cache_key,omitempty"`
}

// Payload returns the stats as an event payload map.
func (s AssemblyStats) Payload() map[string]any {
	return map[string]any{
		"system_tokens":      s.SystemTokens,
		"prompt_tokens":      s.PromptTokens,
		"history_tokens":     s.HistoryTokens,
		"artifact_tokens":    s.ArtifactTokens,
		"total_tokens":       s.TotalTokens,
		"max_tokens":         s.MaxTokens,
		"history_included":   s.HistoryIncluded,
		"history_dropped":    s.HistoryDropped,
		"artifacts_included": s.ArtifactsIncluded,
		"artifacts_dropped":  s.ArtifactsDropped,
		"compacted":          s.Compacted,
		"summarized":         s.Summarized,
		"prompt_cache_key":   s.PromptCacheKey,
	}
}

// Assemble fits history and retrieved artifacts into a budget.
//
// Allocation order is deterministic: the system prompt and current prompt are
// reserved first (ErrBudgetExceeded if they alone do not fit), then artifacts
// are admitted in relevance order up to MaxArtifacts and the remaining tokens,
// and finally history receives whatever is left, compacted through the
// configured Compactor when it does not fit.
func Assemble(ctx context.Context, in AssembleInput) (Assembly, error) {
	counter := in.Counter
	if counter == nil {
		counter = DefaultTokenCounter()
	}
	compactor := in.Compactor
	if compactor == nil {
		compactor = TruncateOldest{}
	}

	stats := AssemblyStats{
		SystemTokens: counter.Count(in.System),
		PromptTokens: counter.Count(in.Prompt),
		MaxTokens:    in.Budget.MaxTokens,
	}
	fixed := stats.SystemTokens + stats.PromptTokens
	if in.Budget.MaxTokens > 0 && fixed > in.Budget.MaxTokens {
		return Assembly{}, fmt.Errorf("%w: system and prompt need %d tokens, budget is %d", ErrBudgetExceeded, fixed, in.Budget.MaxTokens)
	}
	remaining := in.Budget.MaxTokens - fixed
	unlimited := in.Budget.MaxTokens == 0

	// Artifacts: relevance order, bounded by count and tokens.
	artifacts := make([]core.Artifact, 0, len(in.Artifacts))
	for _, a := range in.Artifacts {
		if in.Budget.MaxArtifacts > 0 && len(artifacts) >= in.Budget.MaxArtifacts {
			break
		}
		cost := ArtifactTokens(counter, a)
		if !unlimited && cost > remaining {
			continue // a smaller, lower-ranked artifact may still fit
		}
		artifacts = append(artifacts, a)
		stats.ArtifactTokens += cost
		if !unlimited {
			remaining -= cost
		}
	}
	stats.ArtifactsIncluded = len(artifacts)
	stats.ArtifactsDropped = len(in.Artifacts) - len(artifacts)

	// History: most recent first, compacted when over budget.
	history := in.History
	historyBudget := remaining
	if unlimited {
		historyBudget = 0
	}
	if exceedsHistoryBudget(history, historyBudget, in.Budget.MaxMessages, counter) {
		result, err := compactor.Compact(ctx, CompactRequest{
			Messages:    history,
			MaxTokens:   historyBudget,
			MaxMessages: in.Budget.MaxMessages,
			Counter:     counter,
		})
		if err != nil {
			return Assembly{}, fmt.Errorf("compact history: %w", err)
		}
		stats.Compacted = true
		stats.Summarized = result.Summarized
		stats.HistoryDropped = result.Dropped
		history = result.Messages
		// Safety net: a compactor may return more than the budget allows.
		if exceedsHistoryBudget(history, historyBudget, in.Budget.MaxMessages, counter) {
			var dropped int
			history, dropped = truncateOldest(history, historyBudget, in.Budget.MaxMessages, counter)
			stats.HistoryDropped += dropped
		}
	}
	for _, m := range history {
		stats.HistoryTokens += MessageTokens(counter, m)
	}
	stats.HistoryIncluded = len(history)
	stats.TotalTokens = fixed + stats.ArtifactTokens + stats.HistoryTokens
	stats.PromptCacheKey = promptCacheKey(in.System, history)

	retained := make([]core.Message, len(history))
	copy(retained, history)

	return Assembly{
		Messages:    retained,
		Artifacts:   artifacts,
		ContextText: RenderArtifacts(artifacts),
		Stats:       stats,
	}, nil
}

func exceedsHistoryBudget(messages []core.Message, maxTokens, maxMessages int, counter TokenCounter) bool {
	if maxMessages > 0 && len(messages) > maxMessages {
		return true
	}
	if maxTokens <= 0 {
		return false
	}
	total := 0
	for _, m := range messages {
		total += MessageTokens(counter, m)
		if total > maxTokens {
			return true
		}
	}
	return false
}

// promptCacheKey hashes the stable prefix of a request (system prompt and
// history) so callers can correlate provider prompt-cache behavior across
// turns without recording the content itself.
func promptCacheKey(system string, history []core.Message) string {
	h := sha256.New()
	h.Write([]byte(system))
	h.Write([]byte{0})
	for _, m := range history {
		h.Write([]byte(m.Role))
		h.Write([]byte{0})
		h.Write([]byte(m.Name))
		h.Write([]byte{0})
		h.Write([]byte(m.Content))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// RenderArtifacts formats artifacts as a numbered context block. Each entry
// shows its source (Meta "title" or "source", else ID) and text. Empty input
// renders as an empty string.
func RenderArtifacts(artifacts []core.Artifact) string {
	if len(artifacts) == 0 {
		return ""
	}
	var b strings.Builder
	for i, a := range artifacts {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%d]", i+1)
		if label := artifactLabel(a); label != "" {
			b.WriteString(" ")
			b.WriteString(label)
		}
		b.WriteString("\n")
		if a.Text != "" {
			b.WriteString(a.Text)
		} else if len(a.Bytes) > 0 {
			b.Write(a.Bytes)
		}
	}
	return b.String()
}

func artifactLabel(a core.Artifact) string {
	for _, key := range []string{"title", "source"} {
		if v, ok := a.Meta[key].(string); ok && v != "" {
			return v
		}
	}
	if a.URI != "" {
		return a.URI
	}
	return a.ID
}
