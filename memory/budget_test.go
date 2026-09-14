package memory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/core"
)

// wordCounter counts whitespace-separated words, which makes token arithmetic
// in these tests exact.
var wordCounter = TokenCounterFunc(func(s string) int { return len(strings.Fields(s)) })

func msg(role, content string) core.Message { return core.Message{Role: role, Content: content} }

func TestHeuristicCounter(t *testing.T) {
	c := HeuristicCounter{}
	if got := c.Count(""); got != 0 {
		t.Fatalf("empty = %d", got)
	}
	if got := c.Count("abcd"); got != 1 {
		t.Fatalf("4 chars = %d, want 1", got)
	}
	if got := c.Count("abcde"); got != 2 {
		t.Fatalf("5 chars = %d, want 2 (rounds up)", got)
	}
	if got := (HeuristicCounter{CharsPerToken: 2}).Count("abcd"); got != 2 {
		t.Fatalf("custom ratio = %d, want 2", got)
	}
	if got := c.Count("日本語"); got != 1 {
		t.Fatalf("runes not bytes: %d", got)
	}
}

func TestAssembleUnbounded(t *testing.T) {
	history := []core.Message{msg("user", "hello"), msg("assistant", "hi")}
	artifacts := []core.Artifact{{ID: "a", Text: "alpha"}, {ID: "b", Text: "beta"}}
	got, err := Assemble(context.Background(), AssembleInput{
		System: "sys", Prompt: "question", History: history, Artifacts: artifacts, Counter: wordCounter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 2 || len(got.Artifacts) != 2 {
		t.Fatalf("unbounded should retain all: %+v", got.Stats)
	}
	if got.Stats.Compacted || got.Stats.HistoryDropped != 0 || got.Stats.ArtifactsDropped != 0 {
		t.Fatalf("stats = %+v", got.Stats)
	}
	if got.Stats.PromptCacheKey == "" || len(got.Stats.PromptCacheKey) != 16 {
		t.Fatalf("prompt cache key = %q", got.Stats.PromptCacheKey)
	}
	if !strings.Contains(got.ContextText, "[1] a\nalpha") || !strings.Contains(got.ContextText, "[2] b\nbeta") {
		t.Fatalf("context text = %q", got.ContextText)
	}
	// Retained history is a copy.
	got.Messages[0].Content = "mutated"
	if history[0].Content != "hello" {
		t.Fatal("Assemble must not alias caller history")
	}
}

func TestAssembleFixedPartsExceedBudget(t *testing.T) {
	_, err := Assemble(context.Background(), AssembleInput{
		System: "one two three", Prompt: "four five", Budget: Budget{MaxTokens: 4}, Counter: wordCounter,
	})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
}

func TestAssembleArtifactsThenHistory(t *testing.T) {
	// Each message costs words + 4 framing tokens.
	history := []core.Message{
		msg("user", "oldest turn here"), // 7
		msg("assistant", "middle"),      // 5
		msg("user", "newest"),           // 5
	}
	artifacts := []core.Artifact{
		{ID: "big", Text: "one two three four five six"}, // 6
		{ID: "small", Text: "one"},                       // 1
	}
	got, err := Assemble(context.Background(), AssembleInput{
		System:    "sys",     // 1
		Prompt:    "q",       // 1
		History:   history,   // 17 total
		Artifacts: artifacts, // 7 total
		Budget:    Budget{MaxTokens: 20, MaxArtifacts: 5},
		Counter:   wordCounter,
	})
	if err != nil {
		t.Fatal(err)
	}
	// fixed=2, remaining=18. Artifacts take 7 -> remaining 11. History fits
	// newest (5) + middle (5) = 10; oldest (7) would exceed.
	if got.Stats.ArtifactsIncluded != 2 {
		t.Fatalf("artifacts included = %d, stats %+v", got.Stats.ArtifactsIncluded, got.Stats)
	}
	if len(got.Messages) != 2 || got.Messages[0].Content != "middle" || got.Messages[1].Content != "newest" {
		t.Fatalf("history = %+v", got.Messages)
	}
	if !got.Stats.Compacted || got.Stats.HistoryDropped != 1 || got.Stats.HistoryIncluded != 2 {
		t.Fatalf("stats = %+v", got.Stats)
	}
	if got.Stats.TotalTokens != 2+7+10 {
		t.Fatalf("total tokens = %d", got.Stats.TotalTokens)
	}
	if got.Stats.TotalTokens > got.Stats.MaxTokens {
		t.Fatal("total must not exceed max")
	}
}

func TestAssembleArtifactCountAndSkipLarge(t *testing.T) {
	artifacts := []core.Artifact{
		{ID: "huge", Text: strings.Repeat("w ", 50)}, // 50 tokens, does not fit
		{ID: "fits", Text: "a b"},                    // 2
		{ID: "third", Text: "c"},                     // 1, excluded by MaxArtifacts
	}
	got, err := Assemble(context.Background(), AssembleInput{
		Prompt: "q", Artifacts: artifacts, Budget: Budget{MaxTokens: 10, MaxArtifacts: 1}, Counter: wordCounter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].ID != "fits" {
		t.Fatalf("artifacts = %+v", got.Artifacts)
	}
	if got.Stats.ArtifactsDropped != 2 {
		t.Fatalf("dropped = %d", got.Stats.ArtifactsDropped)
	}
}

func TestAssembleMaxMessages(t *testing.T) {
	history := []core.Message{msg("user", "1"), msg("assistant", "2"), msg("user", "3")}
	got, err := Assemble(context.Background(), AssembleInput{
		History: history, Budget: Budget{MaxMessages: 2}, Counter: wordCounter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 2 || got.Messages[0].Content != "2" {
		t.Fatalf("history = %+v", got.Messages)
	}
	if got.Stats.HistoryDropped != 1 || !got.Stats.Compacted {
		t.Fatalf("stats = %+v", got.Stats)
	}
}

func TestAssembleCustomCompactorSummarizes(t *testing.T) {
	history := []core.Message{msg("user", "a b c d"), msg("assistant", "e f g h"), msg("user", "i")}
	compactor := CompactorFunc(func(_ context.Context, req CompactRequest) (CompactResult, error) {
		if req.MaxTokens != 12 || req.MaxMessages != 0 || req.Counter == nil {
			t.Fatalf("unexpected request: %+v", req)
		}
		return CompactResult{
			Messages:   []core.Message{msg("system", "summary"), req.Messages[len(req.Messages)-1]},
			Dropped:    2,
			Summarized: true,
		}, nil
	})
	got, err := Assemble(context.Background(), AssembleInput{
		Prompt: "q", History: history, Budget: Budget{MaxTokens: 13}, Counter: wordCounter, Compactor: compactor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 2 || got.Messages[0].Content != "summary" {
		t.Fatalf("history = %+v", got.Messages)
	}
	if !got.Stats.Summarized || got.Stats.HistoryDropped != 2 {
		t.Fatalf("stats = %+v", got.Stats)
	}
}

func TestAssembleCompactorOverBudgetIsTruncated(t *testing.T) {
	history := []core.Message{msg("user", "a"), msg("assistant", "b"), msg("user", "c")}
	// A misbehaving compactor returns everything unchanged.
	compactor := CompactorFunc(func(_ context.Context, req CompactRequest) (CompactResult, error) {
		return CompactResult{Messages: req.Messages}, nil
	})
	got, err := Assemble(context.Background(), AssembleInput{
		History: history, Budget: Budget{MaxMessages: 1}, Counter: wordCounter, Compactor: compactor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "c" {
		t.Fatalf("safety net should truncate: %+v", got.Messages)
	}
	if got.Stats.HistoryDropped != 2 {
		t.Fatalf("dropped = %d", got.Stats.HistoryDropped)
	}
}

func TestAssembleCompactorError(t *testing.T) {
	boom := errors.New("summarizer down")
	compactor := CompactorFunc(func(context.Context, CompactRequest) (CompactResult, error) {
		return CompactResult{}, boom
	})
	_, err := Assemble(context.Background(), AssembleInput{
		History: []core.Message{msg("user", "a"), msg("user", "b")}, Budget: Budget{MaxMessages: 1}, Compactor: compactor,
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestPromptCacheKeyStableAcrossPromptButNotHistory(t *testing.T) {
	history := []core.Message{msg("user", "hello")}
	a, _ := Assemble(context.Background(), AssembleInput{System: "s", Prompt: "p1", History: history})
	b, _ := Assemble(context.Background(), AssembleInput{System: "s", Prompt: "p2", History: history})
	c, _ := Assemble(context.Background(), AssembleInput{System: "s", Prompt: "p1", History: append(history, msg("assistant", "x"))})
	if a.Stats.PromptCacheKey != b.Stats.PromptCacheKey {
		t.Fatal("prompt should not affect cache key")
	}
	if a.Stats.PromptCacheKey == c.Stats.PromptCacheKey {
		t.Fatal("history should affect cache key")
	}
}

func TestRenderArtifacts(t *testing.T) {
	if RenderArtifacts(nil) != "" {
		t.Fatal("empty render")
	}
	got := RenderArtifacts([]core.Artifact{
		{ID: "x", Meta: map[string]any{"title": "Doc"}, Text: "body"},
		{URI: "s3://bucket/k", Bytes: []byte("raw")},
		{ID: "id-only", Meta: map[string]any{"source": "web"}, Text: "t"},
	})
	want := "[1] Doc\nbody\n\n[2] s3://bucket/k\nraw\n\n[3] web\nt"
	if got != want {
		t.Fatalf("render = %q\nwant %q", got, want)
	}
}

func TestAssemblyStatsPayload(t *testing.T) {
	p := AssemblyStats{TotalTokens: 3, Compacted: true, PromptCacheKey: "abc"}.Payload()
	if p["total_tokens"] != 3 || p["compacted"] != true || p["prompt_cache_key"] != "abc" {
		t.Fatalf("payload = %v", p)
	}
}
