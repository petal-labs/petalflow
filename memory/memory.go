// Package memory defines the pluggable memory and knowledge interface for
// PetalFlow workflows, plus token-aware context budgets and assembly.
//
// The package is intentionally dependency-free (stdlib only) so that external
// implementations — such as a Cortex-backed provider — can satisfy the
// Provider interface without importing PetalFlow internals beyond this
// contract. Implementations are verified against the conformance suite in
// memory/memorytest.
package memory

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// ErrUnavailable signals that a memory backend is temporarily unavailable
// (network partition, outage, timeout). Providers should wrap this sentinel
// so workflows can distinguish unavailability from genuine failures:
//
//	return fmt.Errorf("%w: connection refused", memory.ErrUnavailable)
//
// How a node reacts to ErrUnavailable is governed by its unavailability
// policy (see nodes.MemoryNodeConfig.OnUnavailable); all other errors
// surface as regular node failures.
var ErrUnavailable = errors.New("memory provider unavailable")

// Scope carries the stable identifiers that bind memory reads and writes to
// a conversation. TenantID and Namespace isolate data across callers and
// knowledge partitions; SessionID and ThreadID identify a conversation and a
// branch of it; RunID ties records to a single workflow execution.
//
// A Scope is validated before use: identifiers must be non-empty where
// required and may only contain characters that form stable, injectable keys
// (letters, digits, dash, underscore, dot).
type Scope struct {
	TenantID  string `json:"tenant_id,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
}

// identifierPattern matches identifiers safe for use in storage keys and
// event payloads: letters, digits, dash, underscore, and dot.
var identifierPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// Validate checks that the scope's identifiers are well-formed. Namespace and
// SessionID are required: together they are the stable address under which
// workflows request memory and context. Empty optional fields are allowed.
func (s Scope) Validate() error {
	if s.Namespace == "" {
		return fmt.Errorf("memory scope: namespace is required")
	}
	if s.SessionID == "" {
		return fmt.Errorf("memory scope: session_id is required")
	}
	for field, value := range map[string]string{
		"tenant_id":  s.TenantID,
		"namespace":  s.Namespace,
		"session_id": s.SessionID,
		"thread_id":  s.ThreadID,
		"run_id":     s.RunID,
	} {
		if value == "" {
			continue
		}
		if len(value) > 256 {
			return fmt.Errorf("memory scope: %s exceeds 256 characters", field)
		}
		if !identifierPattern.MatchString(value) {
			return fmt.Errorf("memory scope: %s %q contains invalid characters (allowed: letters, digits, '-', '_', '.')", field, value)
		}
	}
	return nil
}

// Document is a single retrieved memory or knowledge item.
type Document struct {
	ID       string         `json:"id"`
	Text     string         `json:"text"`
	Source   string         `json:"source,omitempty"`
	Score    float64        `json:"score,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// TokenCounter estimates the token count of a text string. Providers and
// compactors accept this type so model-specific tokenizers can replace the
// default estimate (see EstimateTokens).
type TokenCounter func(text string) int

// EstimateTokens is the default TokenCounter. It uses the common heuristic of
// roughly four characters per token, which is provider-independent and cheap.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len(text) + 3) / 4
}

// ContextBudget bounds the context assembled from retrieved memory, in
// estimated tokens and artifact count. A zero value means unbounded.
type ContextBudget struct {
	// MaxTokens caps the estimated token count of assembled document text.
	MaxTokens int
	// MaxDocuments caps the number of documents in the assembled context.
	MaxDocuments int
}

// RetrievalQuery describes a memory read against a Scope.
type RetrievalQuery struct {
	// Scope addresses the memory: namespace and session are required.
	Scope Scope
	// Query is the free-form retrieval query (may be empty for "recent").
	Query string
	// Limit is the maximum number of documents the provider should return
	// (the retrieval budget). Zero means provider-default.
	Limit int
	// MaxTokens is the token budget for retrieved content. Providers may
	// truncate or rank to fit; the caller enforces the budget again during
	// assembly so a non-compliant provider cannot overrun.
	MaxTokens int
}

// WriteRequest describes a memory write against a Scope.
type WriteRequest struct {
	// Scope addresses the memory: namespace and session are required.
	Scope Scope
	// Key optionally names the record (e.g. a fact or preference label).
	Key string
	// Text is the content to persist.
	Text string
	// Metadata carries structured annotations (source node, retention
	// policy hints, citations). Avoid placing sensitive content here:
	// metadata is observable in events.
	Metadata map[string]any
}

// Provider is the pluggable memory/knowledge interface implemented by
// backends such as Cortex. Implementations must be safe for concurrent use.
type Provider interface {
	// Retrieve reads documents relevant to the query. Implementations should
	// return memory.ErrUnavailable (wrapped) when the backend cannot be
	// reached, and an empty (nil, nil) result when the scope has no matches.
	Retrieve(ctx context.Context, query RetrievalQuery) ([]Document, error)

	// Write persists a record. Implementations should return
	// memory.ErrUnavailable (wrapped) when the backend cannot be reached.
	Write(ctx context.Context, req WriteRequest) error
}
