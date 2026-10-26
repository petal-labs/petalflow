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
