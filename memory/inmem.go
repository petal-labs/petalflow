package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// InMemoryProvider is a reference Provider implementation backed by a
// process-local map. It is useful for tests, examples, and local development;
// production workflows should use a durable provider such as a Cortex adapter.
//
// Documents are keyed by scope (tenant, namespace, session) and scored by
// simple term overlap with the query. A zero MaxTokens in the query is
// treated as unbounded.
type InMemoryProvider struct {
	mu   sync.RWMutex
	docs []storedDocument
	next int64
}

type storedDocument struct {
	scope    Scope
	document Document
}

// NewInMemoryProvider creates an empty InMemoryProvider.
func NewInMemoryProvider() *InMemoryProvider {
	return &InMemoryProvider{}
}

// Retrieve returns documents stored under the query's scope, ranked by term
// overlap with the query text. Documents from other scopes are never visible.
func (p *InMemoryProvider) Retrieve(ctx context.Context, query RetrievalQuery) ([]Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := query.Scope.Validate(); err != nil {
		return nil, fmt.Errorf("memory retrieve: %w", err)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	terms := splitTerms(query.Query)
	matches := make([]Document, 0, len(p.docs))
	// Walk newest-first so stable sorting keeps recent records among equals.
	for i := len(p.docs) - 1; i >= 0; i-- {
		stored := p.docs[i]
		if !sameScope(stored.scope, query.Scope) {
			continue
		}
		doc := stored.document
		doc.Score = scoreOverlap(terms, doc.Text)
		if len(terms) > 0 && doc.Score == 0 {
			continue
		}
		matches = append(matches, doc)
	}

	SortByScore(matches)

	if query.Limit > 0 && len(matches) > query.Limit {
		matches = matches[:query.Limit]
	}
	if query.MaxTokens > 0 {
		matches = fitTokens(matches, query.MaxTokens)
	}
	return matches, nil
}

// Write stores a document under the request's scope.
func (p *InMemoryProvider) Write(ctx context.Context, req WriteRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := req.Scope.Validate(); err != nil {
		return fmt.Errorf("memory write: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.next++
	doc := Document{
		ID:       fmt.Sprintf("mem-%06d", p.next),
		Text:     req.Text,
		Source:   req.Key,
		Metadata: req.Metadata,
	}
	p.docs = append(p.docs, storedDocument{scope: req.Scope, document: doc})
	return nil
}

// Size returns the total number of stored documents (for testing).
func (p *InMemoryProvider) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.docs)
}

// sameScope reports whether two scopes address the same memory partition.
// Thread and Run are refinements, not partitions: records written under a
// scope with a thread are visible to the same session and vice versa.
func sameScope(a, b Scope) bool {
	return a.TenantID == b.TenantID &&
		a.Namespace == b.Namespace &&
		a.SessionID == b.SessionID
}

// splitTerms lowercases and splits a query into significant terms.
func splitTerms(query string) []string {
	return strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		isLower := 'a' <= r && r <= 'z'
		isDigit := '0' <= r && r <= '9'
		return !isLower && !isDigit
	})
}

// scoreOverlap scores text by the fraction of query terms it contains.
func scoreOverlap(terms []string, text string) float64 {
	if len(terms) == 0 {
		return 0
	}
	lower := strings.ToLower(text)
	matched := 0
	for _, term := range terms {
		if strings.Contains(lower, term) {
			matched++
		}
	}
	return float64(matched) / float64(len(terms))
}

// fitTokens truncates the document list to the token budget, dropping whole
// documents (retrieval budgets never rewrite provider output).
func fitTokens(docs []Document, maxTokens int) []Document {
	fitted := make([]Document, 0, len(docs))
	used := 0
	for _, doc := range docs {
		tokens := EstimateTokens(doc.Text)
		if used+tokens > maxTokens {
			break
		}
		fitted = append(fitted, doc)
		used += tokens
	}
	return fitted
}
