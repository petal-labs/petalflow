package memory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/petal-labs/petalflow/core"
)

// MemoryProvider stores and recalls conversation memory for a scope. It is the
// contract a durable backend (for example Cortex) implements so workflows can
// request history by stable session and namespace identifiers.
//
// Implementations must isolate data by Scope.TenantID and Scope.Namespace and
// key conversations by Scope.ConversationKey(). They should wrap transport,
// timeout, and backend-outage errors with Unavailable so nodes can apply the
// configured FailurePolicy.
type MemoryProvider interface {
	// Recall returns messages for the scoped conversation, oldest first, bounded
	// by RecallRequest.Limit. It never returns more than the requested limit.
	Recall(ctx context.Context, req RecallRequest) (RecallResult, error)

	// Remember appends messages to the scoped conversation. Writes are expected
	// to be durable when the call returns without error.
	Remember(ctx context.Context, req RememberRequest) (RememberResult, error)
}

// KnowledgeProvider retrieves knowledge (documents, chunks, citations) that is
// relevant to a query within a scope's namespace.
type KnowledgeProvider interface {
	// Search returns at most SearchRequest.TopK results ordered by descending
	// relevance.
	Search(ctx context.Context, req SearchRequest) (SearchResult, error)
}

// Provider combines conversation memory and knowledge retrieval. Backends that
// only support one capability implement the narrower interface; nodes check
// for each capability separately.
type Provider interface {
	MemoryProvider
	KnowledgeProvider
}

// Named is optionally implemented by providers to identify themselves in
// events and traces. The name should be a short, low-cardinality label such as
// "cortex" or "inmemory".
type Named interface {
	Name() string
}

// ProviderName returns the provider's Name() when implemented, or "unknown".
func ProviderName(p any) string {
	if named, ok := p.(Named); ok && named.Name() != "" {
		return named.Name()
	}
	return "unknown"
}

// RecallRequest asks for conversation history.
type RecallRequest struct {
	Scope Scope
	// Limit is the maximum number of messages to return (most recent). Zero
	// means the provider's default; providers must apply a finite bound.
	Limit int
	// Roles optionally restricts results to the listed message roles.
	Roles []string
}

// RecallResult is the recalled history, oldest first.
type RecallResult struct {
	Messages []core.Message
	// Summary optionally carries a provider-generated summary of history that
	// was not returned (for example older, compacted turns).
	Summary string
	// Truncated reports that more history exists beyond Limit.
	Truncated bool
}

// RememberRequest appends messages to a conversation.
type RememberRequest struct {
	Scope    Scope
	Messages []core.Message
	// Metadata is provider-defined, low-cardinality metadata stored with the
	// messages (for example the workflow ID). Never place secrets here.
	Metadata map[string]string
}

// RememberResult reports the outcome of a write.
type RememberResult struct {
	// Stored is the number of messages the provider accepted.
	Stored int
	// IDs optionally lists provider-assigned message identifiers.
	IDs []string
}

// SearchRequest asks for relevant knowledge.
type SearchRequest struct {
	Scope Scope
	Query string
	// TopK bounds the number of results. Zero means the provider's default;
	// providers must apply a finite bound.
	TopK int
	// Collections optionally restricts the search to named collections within
	// the namespace.
	Collections []string
	// MinScore optionally drops results below a relevance threshold.
	MinScore float64
	// Filters are provider-defined metadata filters.
	Filters map[string]string
}

// SearchHit is one retrieved item. Artifact carries the content and is what a
// node appends to the envelope; Score orders hits for budgeted assembly.
type SearchHit struct {
	Artifact core.Artifact
	Score    float64
}

// SearchResult is the ordered retrieval result.
type SearchResult struct {
	Hits []SearchHit
	// TotalFound optionally reports how many items matched before TopK.
	TotalFound int
}

// ErrUnavailable classifies a provider failure as an outage (transport error,
// timeout, backend down) as opposed to a programming or validation error.
// Nodes apply FailurePolicy only to unavailable errors.
var ErrUnavailable = errors.New("memory provider unavailable")

// Unavailable wraps err so that errors.Is(err, ErrUnavailable) holds. A nil
// err returns nil.
func Unavailable(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

// IsUnavailable reports whether err is an outage-class failure. Context
// deadline and cancellation errors are treated as unavailable because a node
// timeout is indistinguishable from a slow backend.
func IsUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrUnavailable) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled)
}

// FailurePolicy defines what a node does when the provider is unavailable.
type FailurePolicy string

const (
	// FailurePolicyFail (the default) fails the node, and therefore the run
	// unless ContinueOnError is set. Memory is treated as required.
	FailurePolicyFail FailurePolicy = "fail"

	// FailurePolicyContinue degrades gracefully: the node completes with empty
	// results, records a core.NodeError on the envelope so the degradation is
	// visible, and emits an event with status "unavailable".
	FailurePolicyContinue FailurePolicy = "continue"
)

// ParseFailurePolicy validates a policy string. Empty selects the default.
func ParseFailurePolicy(s string) (FailurePolicy, error) {
	switch FailurePolicy(s) {
	case "":
		return FailurePolicyFail, nil
	case FailurePolicyFail, FailurePolicyContinue:
		return FailurePolicy(s), nil
	default:
		return "", fmt.Errorf("invalid failure policy %q: want %q or %q", s, FailurePolicyFail, FailurePolicyContinue)
	}
}

// DefaultOperationTimeout bounds a single provider call when a node does not
// configure its own timeout.
const DefaultOperationTimeout = 10 * time.Second

// DefaultRecallLimit is applied when a request leaves Limit at zero.
const DefaultRecallLimit = 50

// DefaultTopK is applied when a request leaves TopK at zero.
const DefaultTopK = 5
