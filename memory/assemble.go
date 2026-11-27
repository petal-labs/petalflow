package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// CompactionStats describes what context assembly did to the retrieved
// documents. It is emitted on context-assembly events so budget enforcement
// is observable without recording document content.
type CompactionStats struct {
	// DocumentsIn is the number of documents returned by the provider.
	DocumentsIn int
	// DocumentsOut is the number of documents kept after assembly.
	DocumentsOut int
	// TokensIn is the estimated token count before compaction.
	TokensIn int
	// TokensOut is the estimated token count after compaction.
	TokensOut int
	// Truncated is true when any document text was cut or documents were
	// dropped to fit the budget.
	Truncated bool
	// Strategy names the compaction strategy applied ("none", "truncate").
	Strategy string
}

// Compactor is the hook that shapes retrieved documents to fit a
// ContextBudget. Implementations can summarize, rank, or truncate; the
// default TruncateCompactor keeps highest-score documents and trims text.
type Compactor interface {
	// Compact returns the documents to inject into context, fitting budget.
	// A nil budget means no compaction is required.
	Compact(ctx context.Context, docs []Document, budget ContextBudget, counter TokenCounter) ([]Document, CompactionStats, error)
}

// TruncateCompactor is the default Compactor. It keeps documents in their
// given order (providers return them ranked by relevance), drops documents
// that no longer fit, and truncates the text of the first overflowing
// document so the budget is filled rather than wasted.
type TruncateCompactor struct {
	// Counter estimates tokens per document. nil uses EstimateTokens.
	Counter TokenCounter
}

// NewTruncateCompactor returns a TruncateCompactor with default counting.
func NewTruncateCompactor() *TruncateCompactor {
	return &TruncateCompactor{}
}

// Compact fits docs to the budget by truncation.
func (c *TruncateCompactor) Compact(ctx context.Context, docs []Document, budget ContextBudget, counter TokenCounter) ([]Document, CompactionStats, error) {
	if err := ctx.Err(); err != nil {
		return nil, CompactionStats{}, err
	}
	if counter == nil {
		counter = c.Counter
	}
	if counter == nil {
		counter = EstimateTokens
	}

	stats := CompactionStats{DocumentsIn: len(docs), Strategy: "none"}
	if len(docs) == 0 || (budget.MaxTokens <= 0 && budget.MaxDocuments <= 0) {
		stats.DocumentsOut = len(docs)
		stats.TokensIn = countTokens(docs, counter)
		stats.TokensOut = stats.TokensIn
		return docs, stats, nil
	}

	stats.Strategy = "truncate"
	out := make([]Document, 0, len(docs))
	tokensUsed := 0
	truncated := false

	for _, doc := range docs {
		if budget.MaxDocuments > 0 && len(out) >= budget.MaxDocuments {
			truncated = true
			break
		}

		docTokens := counter(doc.Text)
		remaining := budget.MaxTokens - tokensUsed
		if budget.MaxTokens > 0 && docTokens > remaining {
			// Truncate the first overflowing document to use the remaining
			// budget; subsequent documents no longer fit.
			truncated = true
			if remaining > 0 {
				if trimmed := truncateText(doc.Text, remaining, counter); trimmed != "" {
					doc.Text = trimmed
					out = append(out, doc)
				}
			}
			break
		}

		out = append(out, doc)
		tokensUsed += docTokens
	}

	stats.DocumentsOut = len(out)
	stats.TokensIn = countTokens(docs, counter)
	stats.TokensOut = countTokens(out, counter)
	stats.Truncated = truncated
	return out, stats, nil
}

// Assemble applies budget-aware compaction and returns the documents plus a
// ready-to-render rendering of the context. It is the shared entry point used
// by the memory node so budget enforcement cannot be bypassed.
func Assemble(ctx context.Context, docs []Document, budget ContextBudget, compactor Compactor, counter TokenCounter) ([]Document, CompactionStats, error) {
	if compactor == nil {
		compactor = NewTruncateCompactor()
	}
	if counter == nil {
		counter = EstimateTokens
	}
	return compactor.Compact(ctx, docs, budget, counter)
}

// AssembleWithGraph applies budget-aware compaction across both retrieved
// documents and optional GraphContext. When budget.MaxTokens > 0, graph
// context is trimmed to fit within a bounded share (at most 25% of MaxTokens,
// and never exceeding MaxTokens) and its rendered token cost is deducted from
// the remaining token budget available to documents so combined TokensOut
// never exceeds budget.MaxTokens.
func AssembleWithGraph(ctx context.Context, docs []Document, gc *GraphContext, budget ContextBudget, compactor Compactor, counter TokenCounter) ([]Document, *GraphContext, CompactionStats, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, CompactionStats{}, err
	}
	if compactor == nil {
		compactor = NewTruncateCompactor()
	}
	if counter == nil {
		counter = EstimateTokens
	}
	if gc.IsEmpty() {
		kept, stats, err := compactor.Compact(ctx, docs, budget, counter)
		return kept, nil, stats, err
	}

	graphTokensIn := counter(RenderGraphContext(gc))
	trimmedGraph := gc
	graphTruncated := false
	docBudget := budget

	if budget.MaxTokens > 0 {
		// Reserve at most 25% of the token budget for graph context (or up to
		// full MaxTokens when there are no documents), keeping documents
		// primary while ensuring graph triples fit within the overall cap.
		graphCap := budget.MaxTokens / 4
		if len(docs) == 0 {
			graphCap = budget.MaxTokens
		}
		if graphCap == 0 && budget.MaxTokens > 0 {
			graphCap = budget.MaxTokens
		}
		trimmedGraph, graphTruncated = trimGraphContext(gc, graphCap, counter)
		graphTokensOut := 0
		if !trimmedGraph.IsEmpty() {
			graphTokensOut = counter(RenderGraphContext(trimmedGraph))
		}
		remainingForDocs := budget.MaxTokens - graphTokensOut
		if remainingForDocs <= 0 {
			// Budget is completely consumed by graph context; drop documents.
			stats := CompactionStats{
				DocumentsIn:  len(docs),
				DocumentsOut: 0,
				TokensIn:     countTokens(docs, counter) + graphTokensIn,
				TokensOut:    graphTokensOut,
				Truncated:    graphTruncated || len(docs) > 0,
				Strategy:     "truncate",
			}
			return nil, trimmedGraph, stats, nil
		}
		docBudget.MaxTokens = remainingForDocs
	}

	kept, stats, err := compactor.Compact(ctx, docs, docBudget, counter)
	if err != nil {
		return nil, nil, CompactionStats{}, err
	}

	graphTokensOut := 0
	if !trimmedGraph.IsEmpty() {
		graphTokensOut = counter(RenderGraphContext(trimmedGraph))
	}
	stats.TokensIn += graphTokensIn
	stats.TokensOut += graphTokensOut
	if graphTruncated {
		stats.Truncated = true
		if stats.Strategy == "" || stats.Strategy == "none" {
			stats.Strategy = "truncate"
		}
	}
	return kept, trimmedGraph, stats, nil
}

// RenderContext renders documents into a single context block suitable for
// prompt injection. Each document becomes a citation-style entry with its
// source when available.
func RenderContext(docs []Document) string {
	if len(docs) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Retrieved context:")
	for _, doc := range docs {
		fmt.Fprintf(&b, "\n- %s", doc.Text)
		if doc.Source != "" {
			fmt.Fprintf(&b, " [source: %s]", doc.Source)
		}
	}
	return b.String()
}

// RenderGraphContext renders a compact GraphContext block (typed relationships
// and top entities) suitable for prompt injection. Returns empty string when
// gc is nil or empty.
func RenderGraphContext(gc *GraphContext) string {
	if gc.IsEmpty() {
		return ""
	}

	var b strings.Builder
	b.WriteString("Graph context:")
	if len(gc.Relationships) > 0 {
		b.WriteString("\nRelationships:")
		for _, r := range gc.Relationships {
			fmt.Fprintf(&b, "\n- %s -[%s]-> %s", r.Source, r.RelationType, r.Target)
			if r.Description != "" {
				fmt.Fprintf(&b, ": %s", r.Description)
			}
		}
	}
	if len(gc.Entities) > 0 {
		b.WriteString("\nEntities:")
		for _, e := range gc.Entities {
			fmt.Fprintf(&b, "\n- %s", e.Name)
			if e.Type != "" {
				fmt.Fprintf(&b, " (%s)", e.Type)
			}
			if e.Summary != "" {
				fmt.Fprintf(&b, ": %s", e.Summary)
			}
		}
	}
	return b.String()
}

// RenderContextWithGraph renders retrieved documents and optional graph
// context into a single prompt-ready block. When gc is nil or empty, the
// output is identical to RenderContext(docs).
func RenderContextWithGraph(docs []Document, gc *GraphContext) string {
	docBlock := RenderContext(docs)
	graphBlock := RenderGraphContext(gc)
	switch {
	case docBlock == "":
		return graphBlock
	case graphBlock == "":
		return docBlock
	default:
		return docBlock + "\n\n" + graphBlock
	}
}

// trimGraphContext keeps as many leading relationships and entities as fit
// within maxTokens (relationships first because multi-hop triples carry the
// highest connective value). Returns (nil, true) if nothing fits.
func trimGraphContext(gc *GraphContext, maxTokens int, counter TokenCounter) (*GraphContext, bool) {
	if gc.IsEmpty() {
		return nil, false
	}
	if maxTokens <= 0 {
		return nil, true
	}
	if counter(RenderGraphContext(gc)) <= maxTokens {
		return gc, false
	}

	out := &GraphContext{}
	truncated := false

	for _, rel := range gc.Relationships {
		candidate := &GraphContext{
			Relationships: append(append([]GraphRelationship(nil), out.Relationships...), rel),
			Entities:      out.Entities,
		}
		if counter(RenderGraphContext(candidate)) > maxTokens {
			truncated = true
			break
		}
		out.Relationships = candidate.Relationships
	}
	if len(out.Relationships) < len(gc.Relationships) {
		truncated = true
	}

	for _, ent := range gc.Entities {
		candidate := &GraphContext{
			Relationships: out.Relationships,
			Entities:      append(append([]GraphEntity(nil), out.Entities...), ent),
		}
		if counter(RenderGraphContext(candidate)) > maxTokens {
			truncated = true
			break
		}
		out.Entities = candidate.Entities
	}
	if len(out.Entities) < len(gc.Entities) {
		truncated = true
	}

	if out.IsEmpty() {
		return nil, true
	}
	return out, truncated
}

// countTokens sums the estimated tokens across documents.
func countTokens(docs []Document, counter TokenCounter) int {
	total := 0
	for _, doc := range docs {
		total += counter(doc.Text)
	}
	return total
}

// truncateText cuts text to fit within maxTokens using the counter (the
// ellipsis suffix is counted against the budget). Counters are assumed
// monotonic in length.
func truncateText(text string, maxTokens int, counter TokenCounter) string {
	const suffix = "…"
	if maxTokens <= 0 {
		return ""
	}
	runes := []rune(text)
	if counter(text) <= maxTokens {
		return text
	}

	// Start from a proportional guess, then adjust with bounded linear scans.
	cut := len(runes) * maxTokens / max(counter(text), 1)
	if cut >= len(runes) {
		cut = len(runes) - 1
	}
	for cut > 0 && counter(string(runes[:cut])+suffix) > maxTokens {
		cut--
	}
	for cut < len(runes) && counter(string(runes[:cut+1])+suffix) <= maxTokens {
		cut++
	}
	if cut <= 0 {
		return ""
	}
	return strings.TrimRight(string(runes[:cut]), " \t\n") + suffix
}

// SortByScore orders documents by descending score, keeping stable order for
// equal scores. Providers usually return ranked results already; this helper
// is available for implementations that do not.
func SortByScore(docs []Document) {
	sort.SliceStable(docs, func(i, j int) bool {
		return docs[i].Score > docs[j].Score
	})
}
