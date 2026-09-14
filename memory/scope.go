// Package memory defines PetalFlow's pluggable memory and knowledge contract.
//
// The package deliberately contains only the contract, budgeting helpers, and
// a reference in-memory implementation. Durable backends such as Cortex
// implement MemoryProvider and KnowledgeProvider in their own module; the
// conformance suite in memory/memorytest verifies an implementation without
// PetalFlow importing anything from the backend.
package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Scope identifies whose memory a workflow may read and write. Identifiers are
// supplied by the caller (API request, schedule, CLI) and propagated by the
// runtime to every node through the context, so nodes never invent them.
//
// TenantID is the ownership boundary and must match the authenticated caller.
// Namespace partitions memory within a tenant (for example one namespace per
// application or knowledge base). SessionID identifies a stable conversation
// and ThreadID optionally identifies a branch within it. RunID is filled by
// the runtime and lets providers scope run-local state.
type Scope struct {
	TenantID  string `json:"tenant_id,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
}

// MaxIdentifierLength bounds every scope identifier so providers can use them
// as storage keys without further validation.
const MaxIdentifierLength = 256

var (
	// ErrInvalidScope is returned when a scope identifier is missing or malformed.
	ErrInvalidScope = errors.New("invalid memory scope")
	// ErrScopeRequired is returned when a node needs a scope but the run did not
	// supply one.
	ErrScopeRequired = errors.New("memory scope required: set RunOptions.Scope (namespace and session_id)")
)

// Validate checks that the scope can address a memory partition: Namespace is
// required and every set identifier must be safe to use as a storage key.
// Knowledge retrieval needs only this; conversation memory additionally needs
// ValidateConversation.
func (s Scope) Validate() error {
	if strings.TrimSpace(s.Namespace) == "" {
		return fmt.Errorf("%w: namespace is required", ErrInvalidScope)
	}
	for _, field := range []struct{ name, value string }{
		{"tenant_id", s.TenantID},
		{"namespace", s.Namespace},
		{"session_id", s.SessionID},
		{"thread_id", s.ThreadID},
		{"run_id", s.RunID},
	} {
		if err := validateIdentifier(field.name, field.value); err != nil {
			return err
		}
	}
	return nil
}

// ValidateConversation checks Validate plus the SessionID that conversation
// memory (Recall/Remember) requires.
func (s Scope) ValidateConversation() error {
	if err := s.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(s.SessionID) == "" {
		return fmt.Errorf("%w: session_id is required", ErrInvalidScope)
	}
	return nil
}

// IsZero reports whether no identifier is set.
func (s Scope) IsZero() bool {
	return s.TenantID == "" && s.Namespace == "" && s.SessionID == "" && s.ThreadID == "" && s.RunID == ""
}

// Normalized returns a copy with surrounding whitespace removed from every
// identifier.
func (s Scope) Normalized() Scope {
	return Scope{
		TenantID:  strings.TrimSpace(s.TenantID),
		Namespace: strings.TrimSpace(s.Namespace),
		SessionID: strings.TrimSpace(s.SessionID),
		ThreadID:  strings.TrimSpace(s.ThreadID),
		RunID:     strings.TrimSpace(s.RunID),
	}
}

// WithNamespace returns a copy of the scope targeting a different namespace.
// Nodes use this when a workflow reads knowledge from one namespace while
// writing conversation memory to another.
func (s Scope) WithNamespace(namespace string) Scope {
	if strings.TrimSpace(namespace) == "" {
		return s
	}
	s.Namespace = strings.TrimSpace(namespace)
	return s
}

// ConversationKey returns the identifier a provider should use for the
// conversation container: the thread when set, otherwise the session.
func (s Scope) ConversationKey() string {
	if s.ThreadID != "" {
		return s.ThreadID
	}
	return s.SessionID
}

// Attributes returns the scope as low-cardinality event/trace attributes.
// It contains identifiers only, never content.
func (s Scope) Attributes() map[string]any {
	attrs := make(map[string]any, 5)
	if s.TenantID != "" {
		attrs["tenant_id"] = s.TenantID
	}
	if s.Namespace != "" {
		attrs["namespace"] = s.Namespace
	}
	if s.SessionID != "" {
		attrs["session_id"] = s.SessionID
	}
	if s.ThreadID != "" {
		attrs["thread_id"] = s.ThreadID
	}
	if s.RunID != "" {
		attrs["run_id"] = s.RunID
	}
	return attrs
}

func validateIdentifier(name, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > MaxIdentifierLength {
		return fmt.Errorf("%w: %s exceeds %d characters", ErrInvalidScope, name, MaxIdentifierLength)
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%w: %s has surrounding whitespace", ErrInvalidScope, name)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %s contains control characters", ErrInvalidScope, name)
		}
	}
	return nil
}

type scopeContextKey struct{}

// ContextWithScope attaches a scope to the context. The runtime calls this for
// every node execution; providers and nodes read it with ScopeFromContext.
func ContextWithScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, scopeContextKey{}, scope)
}

// ScopeFromContext retrieves the scope attached by the runtime. The boolean is
// false when the run was started without a scope.
func ScopeFromContext(ctx context.Context) (Scope, bool) {
	scope, ok := ctx.Value(scopeContextKey{}).(Scope)
	return scope, ok
}
