package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTruncateCompactorNoBudget(t *testing.T) {
	docs := []Document{{ID: "1", Text: "hello world"}}
	stats, err := compactForTest(context.Background(), docs, ContextBudget{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if stats.Truncated {
		t.Error("empty budget should not truncate")
	}
	if stats.Strategy != "none" {
		t.Errorf("Strategy = %q, want %q", stats.Strategy, "none")
	}
}

func TestTruncateCompactorMaxDocuments(t *testing.T) {
	docs := []Document{
		{ID: "1", Text: "one"},
		{ID: "2", Text: "two"},
		{ID: "3", Text: "three"},
	}
	kept, stats, err := newTruncateForTest().Compact(context.Background(), docs, ContextBudget{MaxDocuments: 2}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(kept) != 2 {
		t.Fatalf("kept %d documents, want 2", len(kept))
	}
	if kept[0].ID != "1" || kept[1].ID != "2" {
		t.Errorf("kept IDs = %v,%v; want order preserved", kept[0].ID, kept[1].ID)
	}
	if !stats.Truncated {
		t.Error("expected Truncated=true when documents are dropped")
	}
	if stats.DocumentsIn != 3 || stats.DocumentsOut != 2 {
		t.Errorf("stats documents in/out = %d/%d, want 3/2", stats.DocumentsIn, stats.DocumentsOut)
	}
}

func TestTruncateCompactorMaxTokens(t *testing.T) {
	// Each 8-char doc estimates to 2 tokens.
	docs := []Document{
		{ID: "1", Text: "12345678"},
		{ID: "2", Text: "87654321"},
		{ID: "3", Text: "00000000"},
	}
	kept, stats, err := newTruncateForTest().Compact(context.Background(), docs, ContextBudget{MaxTokens: 5}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if stats.TokensOut > 5 {
		t.Fatalf("TokensOut = %d, exceeds budget 5", stats.TokensOut)
	}
	// Budget of 5 tokens: two full docs (4) + a truncated third (1).
	if len(kept) != 3 {
		t.Fatalf("kept %d documents, want 3 (third truncated)", len(kept))
	}
	if !stats.Truncated {
		t.Error("expected Truncated=true")
	}
	if got := EstimateTokens(kept[2].Text); got > 1 {
		t.Errorf("truncated document tokens = %d, want <= 1", got)
	}
}

func TestTruncateCompactorTruncationEndsWithEllipsis(t *testing.T) {
	docs := []Document{{ID: "1", Text: "word word word word word word"}}
	kept, _, err := newTruncateForTest().Compact(context.Background(), docs, ContextBudget{MaxTokens: 2}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(kept) != 1 {
		t.Fatalf("kept %d documents, want 1", len(kept))
	}
	if !endsWithRune(kept[0].Text, '…') {
		t.Errorf("truncated text = %q, want ellipsis suffix", kept[0].Text)
	}
}

func TestAssembleDefaults(t *testing.T) {
	docs := []Document{{ID: "1", Text: "text"}}
	kept, stats, err := Assemble(context.Background(), docs, ContextBudget{}, nil, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(kept) != 1 || stats.DocumentsOut != 1 {
		t.Fatalf("Assemble changed documents without a budget")
	}
}

func TestAssembleContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Assemble(ctx, []Document{{ID: "1", Text: "text"}}, ContextBudget{}, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Assemble error = %v, want context.Canceled", err)
	}
}

func TestRenderContext(t *testing.T) {
	if got := RenderContext(nil); got != "" {
		t.Errorf("RenderContext(nil) = %q, want empty", got)
	}
	got := RenderContext([]Document{
		{ID: "1", Text: "first fact", Source: "kb-1"},
		{ID: "2", Text: "second fact"},
	})
	for _, want := range []string{"first fact", "[source: kb-1]", "second fact"} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderContext() missing %q in %q", want, got)
		}
	}
}

func TestSortByScore(t *testing.T) {
	docs := []Document{{ID: "a", Score: 0.1}, {ID: "b", Score: 0.9}, {ID: "c", Score: 0.5}}
	SortByScore(docs)
	if docs[0].ID != "b" || docs[1].ID != "c" || docs[2].ID != "a" {
		t.Errorf("SortByScore order = %v,%v,%v", docs[0].ID, docs[1].ID, docs[2].ID)
	}
}

func newTruncateForTest() *TruncateCompactor {
	return NewTruncateCompactor()
}

func compactForTest(ctx context.Context, docs []Document, budget ContextBudget) (CompactionStats, error) {
	_, stats, err := newTruncateForTest().Compact(ctx, docs, budget, nil)
	return stats, err
}

func endsWithRune(s string, r rune) bool {
	runes := []rune(s)
	return len(runes) > 0 && runes[len(runes)-1] == r
}

func TestRenderGraphContext(t *testing.T) {
	if got := RenderGraphContext(nil); got != "" {
		t.Errorf("RenderGraphContext(nil) = %q, want empty", got)
	}
	if got := RenderGraphContext(&GraphContext{}); got != "" {
		t.Errorf("RenderGraphContext(empty) = %q, want empty", got)
	}

	gc := &GraphContext{
		Entities: []GraphEntity{
			{ID: "e1", Name: "PaymentsDB", Type: "product", Summary: "Primary transactional database"},
		},
		Relationships: []GraphRelationship{
			{Source: "Checkout Gateway", Target: "PaymentsDB", RelationType: "persists_in", Description: "Persists order state"},
		},
	}
	got := RenderGraphContext(gc)
	for _, want := range []string{
		"Graph context:",
		"Checkout Gateway -[persists_in]-> PaymentsDB: Persists order state",
		"PaymentsDB (product): Primary transactional database",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderGraphContext() missing %q in:\n%s", want, got)
		}
	}

	docs := []Document{{ID: "d1", Text: "Order flow chunk", Source: "arch.md"}}
	if same := RenderContextWithGraph(docs, nil); same != RenderContext(docs) {
		t.Errorf("RenderContextWithGraph(docs, nil) diverged from RenderContext(docs)")
	}
	combined := RenderContextWithGraph(docs, gc)
	if !strings.Contains(combined, "Retrieved context:") || !strings.Contains(combined, "Graph context:") {
		t.Errorf("RenderContextWithGraph missing sections:\n%s", combined)
	}
}

func TestAssembleWithGraphBudgetEnforced(t *testing.T) {
	docs := []Document{
		{ID: "d1", Text: strings.Repeat("a", 200)}, // 50 tokens
		{ID: "d2", Text: strings.Repeat("b", 200)}, // 50 tokens
	}
	gc := &GraphContext{
		Relationships: []GraphRelationship{
			{Source: "Checkout Gateway", Target: "PaymentsDB", RelationType: "persists_in"},
			{Source: "PaymentsDB", Target: "AuroraRegion", RelationType: "replicated_to"},
			{Source: "AuroraRegion", Target: "DRRunbook", RelationType: "documented_by", Description: strings.Repeat("x", 200)},
		},
		Entities: []GraphEntity{
			{Name: "Checkout Gateway", Type: "product"},
			{Name: "PaymentsDB", Type: "product", Summary: strings.Repeat("y", 200)},
		},
	}

	budget := ContextBudget{MaxTokens: 120}
	keptDocs, keptGraph, stats, err := AssembleWithGraph(context.Background(), docs, gc, budget, nil, nil)
	if err != nil {
		t.Fatalf("AssembleWithGraph: %v", err)
	}
	if stats.TokensOut > budget.MaxTokens {
		t.Errorf("TokensOut = %d exceeds MaxTokens = %d", stats.TokensOut, budget.MaxTokens)
	}
	if !stats.Truncated {
		t.Error("expected Truncated=true under tight budget")
	}
	if len(keptDocs) == 0 {
		t.Error("expected at least one document kept alongside graph context")
	}
	if keptGraph.IsEmpty() {
		t.Error("expected at least one relationship kept within graph cap")
	}
	if len(keptGraph.Relationships) >= len(gc.Relationships) && len(keptGraph.Entities) >= len(gc.Entities) {
		t.Error("expected graph context to be trimmed to fit 25% cap")
	}

	// Unbounded budget keeps everything.
	allDocs, allGraph, unboundedStats, err := AssembleWithGraph(context.Background(), docs, gc, ContextBudget{}, nil, nil)
	if err != nil {
		t.Fatalf("AssembleWithGraph unbounded: %v", err)
	}
	if len(allDocs) != 2 || len(allGraph.Relationships) != 3 || len(allGraph.Entities) != 2 {
		t.Errorf("unbounded assembly unexpectedly dropped items: docs=%d rels=%d ents=%d",
			len(allDocs), len(allGraph.Relationships), len(allGraph.Entities))
	}
	if unboundedStats.Truncated {
		t.Error("unbounded assembly should not mark Truncated=true")
	}
}

func TestExtractGraphContext(t *testing.T) {
	if got := ExtractGraphContext(nil); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
	gc := GraphContext{
		Entities: []GraphEntity{{Name: "PaymentsDB"}},
	}
	fromVal := ExtractGraphContext([]Document{
		{ID: "d1", Metadata: map[string]any{MetadataKeyGraphContext: gc}},
	})
	if fromVal == nil || len(fromVal.Entities) != 1 {
		t.Fatalf("expected GraphContext extracted from value, got %+v", fromVal)
	}
	fromPtr := ExtractGraphContext([]Document{
		{ID: "d1", Metadata: map[string]any{MetadataKeyGraphContext: &gc}},
	})
	if fromPtr == nil || len(fromPtr.Entities) != 1 {
		t.Fatalf("expected GraphContext extracted from pointer, got %+v", fromPtr)
	}
}
