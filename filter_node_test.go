package petalflow

import (
	"context"
	"errors"
	"testing"
)

func TestNewFilterNode(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		node := NewFilterNode("filter1", FilterNodeConfig{})

		if node.ID() != "filter1" {
			t.Errorf("expected ID 'filter1', got %q", node.ID())
		}
		if node.Kind() != NodeKindFilter {
			t.Errorf("expected kind %v, got %v", NodeKindFilter, node.Kind())
		}

		config := node.Config()
		if config.Target != FilterTargetArtifacts {
			t.Errorf("expected default target %q, got %q", FilterTargetArtifacts, config.Target)
		}
	})

	t.Run("custom config", func(t *testing.T) {
		node := NewFilterNode("custom", FilterNodeConfig{
			Target:    FilterTargetMessages,
			OutputVar: "filtered_messages",
			StatsVar:  "filter_stats",
		})

		config := node.Config()
		if config.Target != FilterTargetMessages {
			t.Errorf("expected target %q, got %q", FilterTargetMessages, config.Target)
		}
		if config.OutputVar != "filtered_messages" {
			t.Errorf("expected OutputVar 'filtered_messages', got %q", config.OutputVar)
		}
		if config.StatsVar != "filter_stats" {
			t.Errorf("expected StatsVar 'filter_stats', got %q", config.StatsVar)
		}
	})
}

func TestFilterNode_TopN(t *testing.T) {
	t.Run("keeps top N by score descending", func(t *testing.T) {
		node := NewFilterNode("topN", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpTopN, N: 3, ScoreField: "score", Order: "desc"},
			},
			OutputVar: "top_items",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "score": 0.5},
			{"id": "b", "score": 0.9},
			{"id": "c", "score": 0.3},
			{"id": "d", "score": 0.7},
			{"id": "e", "score": 0.8},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		topItems, ok := result.GetVar("top_items")
		if !ok {
			t.Fatal("expected top_items variable")
		}

		items := topItems.([]any)
		if len(items) != 3 {
			t.Errorf("expected 3 items, got %d", len(items))
		}

		// Should be: b(0.9), e(0.8), d(0.7)
		first := items[0].(map[string]any)
		if first["id"] != "b" {
			t.Errorf("expected first item id 'b', got %v", first["id"])
		}
	})

	t.Run("keeps top N ascending", func(t *testing.T) {
		node := NewFilterNode("topNAsc", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpTopN, N: 2, ScoreField: "score", Order: "asc"},
			},
			OutputVar: "bottom_items",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "score": 0.5},
			{"id": "b", "score": 0.9},
			{"id": "c", "score": 0.3},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["bottom_items"].([]any)
		if len(items) != 2 {
			t.Errorf("expected 2 items, got %d", len(items))
		}

		// Should be: c(0.3), a(0.5)
		first := items[0].(map[string]any)
		if first["id"] != "c" {
			t.Errorf("expected first item id 'c', got %v", first["id"])
		}
	})

	t.Run("returns all items when N greater than count", func(t *testing.T) {
		node := NewFilterNode("topNLarge", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpTopN, N: 10, ScoreField: "score"},
			},
			OutputVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "score": 0.5},
			{"id": "b", "score": 0.9},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["result"].([]any)
		if len(items) != 2 {
			t.Errorf("expected all 2 items, got %d", len(items))
		}
	})
}

func TestFilterNode_Threshold(t *testing.T) {
	t.Run("filters by minimum threshold", func(t *testing.T) {
		minVal := 0.5
		node := NewFilterNode("threshold", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpThreshold, ScoreField: "confidence", Min: &minVal},
			},
			OutputVar: "confident_items",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "confidence": 0.3},
			{"id": "b", "confidence": 0.7},
			{"id": "c", "confidence": 0.5},
			{"id": "d", "confidence": 0.9},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["confident_items"].([]any)
		if len(items) != 3 {
			t.Errorf("expected 3 items with confidence >= 0.5, got %d", len(items))
		}
	})

	t.Run("filters by maximum threshold", func(t *testing.T) {
		maxVal := 100.0
		node := NewFilterNode("maxThreshold", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpThreshold, ScoreField: "length", Max: &maxVal},
			},
			OutputVar: "short_items",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "length": 50},
			{"id": "b", "length": 150},
			{"id": "c", "length": 100},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["short_items"].([]any)
		if len(items) != 2 {
			t.Errorf("expected 2 items with length <= 100, got %d", len(items))
		}
	})

	t.Run("filters by range", func(t *testing.T) {
		minVal := 0.3
		maxVal := 0.7
		node := NewFilterNode("range", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpThreshold, ScoreField: "score", Min: &minVal, Max: &maxVal},
			},
			OutputVar: "mid_items",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "score": 0.1},
			{"id": "b", "score": 0.5},
			{"id": "c", "score": 0.9},
			{"id": "d", "score": 0.3},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["mid_items"].([]any)
		if len(items) != 2 {
			t.Errorf("expected 2 items in range [0.3, 0.7], got %d", len(items))
		}
	})
}

func TestFilterNode_Dedupe(t *testing.T) {
	t.Run("dedupes by field keeping first", func(t *testing.T) {
		node := NewFilterNode("dedupeFirst", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpDedupe, Field: "category", Keep: "first"},
			},
			OutputVar: "unique_items",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "category": "tech"},
			{"id": "b", "category": "sports"},
			{"id": "c", "category": "tech"},
			{"id": "d", "category": "sports"},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["unique_items"].([]any)
		if len(items) != 2 {
			t.Errorf("expected 2 unique items, got %d", len(items))
		}

		// First occurrence should be kept
		first := items[0].(map[string]any)
		if first["id"] != "a" {
			t.Errorf("expected first tech item 'a', got %v", first["id"])
		}
	})

	t.Run("dedupes by field keeping last", func(t *testing.T) {
		node := NewFilterNode("dedupeLast", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpDedupe, Field: "category", Keep: "last"},
			},
			OutputVar: "unique_items",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "category": "tech"},
			{"id": "b", "category": "sports"},
			{"id": "c", "category": "tech"},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["unique_items"].([]any)
		// Should keep b and c (last occurrences)
		for _, item := range items {
			m := item.(map[string]any)
			if m["category"] == "tech" && m["id"] != "c" {
				t.Errorf("expected last tech item 'c', got %v", m["id"])
			}
		}
	})

	t.Run("dedupes by field keeping highest score", func(t *testing.T) {
		node := NewFilterNode("dedupeHighest", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpDedupe, Field: "category", Keep: "highest_score", ScoreField: "score"},
			},
			OutputVar: "best_items",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "category": "tech", "score": 0.5},
			{"id": "b", "category": "tech", "score": 0.9},
			{"id": "c", "category": "tech", "score": 0.3},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["best_items"].([]any)
		if len(items) != 1 {
			t.Errorf("expected 1 unique item, got %d", len(items))
		}

		best := items[0].(map[string]any)
		if best["id"] != "b" {
			t.Errorf("expected highest score item 'b', got %v", best["id"])
		}
	})
}

func TestFilterNode_ByType(t *testing.T) {
	t.Run("includes specific types", func(t *testing.T) {
		node := NewFilterNode("typeInclude", FilterNodeConfig{
			Target: FilterTargetArtifacts,
			Filters: []FilterOp{
				{Type: FilterOpByType, IncludeTypes: []string{"chunk", "citation"}},
			},
		})

		env := NewEnvelope()
		env.Artifacts = []Artifact{
			{ID: "1", Type: "chunk"},
			{ID: "2", Type: "metadata"},
			{ID: "3", Type: "citation"},
			{ID: "4", Type: "summary"},
		}

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Artifacts) != 2 {
			t.Errorf("expected 2 artifacts, got %d", len(result.Artifacts))
		}

		for _, a := range result.Artifacts {
			if a.Type != "chunk" && a.Type != "citation" {
				t.Errorf("unexpected type %q", a.Type)
			}
		}
	})

	t.Run("excludes specific types", func(t *testing.T) {
		node := NewFilterNode("typeExclude", FilterNodeConfig{
			Target: FilterTargetArtifacts,
			Filters: []FilterOp{
				{Type: FilterOpByType, ExcludeTypes: []string{"metadata"}},
			},
		})

		env := NewEnvelope()
		env.Artifacts = []Artifact{
			{ID: "1", Type: "chunk"},
			{ID: "2", Type: "metadata"},
			{ID: "3", Type: "citation"},
		}

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Artifacts) != 2 {
			t.Errorf("expected 2 artifacts, got %d", len(result.Artifacts))
		}

		for _, a := range result.Artifacts {
			if a.Type == "metadata" {
				t.Error("metadata should have been excluded")
			}
		}
	})
}

func TestFilterNode_Match(t *testing.T) {
	t.Run("matches by value", func(t *testing.T) {
		node := NewFilterNode("matchValue", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpMatch, Field: "status", Value: "verified"},
			},
			OutputVar: "verified_items",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "status": "pending"},
			{"id": "b", "status": "verified"},
			{"id": "c", "status": "verified"},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["verified_items"].([]any)
		if len(items) != 2 {
			t.Errorf("expected 2 verified items, got %d", len(items))
		}
	})

	t.Run("matches by pattern", func(t *testing.T) {
		node := NewFilterNode("matchPattern", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpMatch, Field: "email", Pattern: `^.*@example\.com$`},
			},
			OutputVar: "example_emails",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "email": "user@example.com"},
			{"id": "b", "email": "user@other.com"},
			{"id": "c", "email": "admin@example.com"},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["example_emails"].([]any)
		if len(items) != 2 {
			t.Errorf("expected 2 example.com emails, got %d", len(items))
		}
	})

	t.Run("matches nested field", func(t *testing.T) {
		node := NewFilterNode("matchNested", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpMatch, Field: "meta.verified", Value: true},
			},
			OutputVar: "verified_items",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "meta": map[string]any{"verified": true}},
			{"id": "b", "meta": map[string]any{"verified": false}},
			{"id": "c", "meta": map[string]any{"verified": true}},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["verified_items"].([]any)
		if len(items) != 2 {
			t.Errorf("expected 2 verified items, got %d", len(items))
		}
	})
}

func TestFilterNode_Exclude(t *testing.T) {
	t.Run("excludes by value", func(t *testing.T) {
		node := NewFilterNode("excludeValue", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpExclude, Field: "status", Value: "deleted"},
			},
			OutputVar: "active_items",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "status": "active"},
			{"id": "b", "status": "deleted"},
			{"id": "c", "status": "active"},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["active_items"].([]any)
		if len(items) != 2 {
			t.Errorf("expected 2 non-deleted items, got %d", len(items))
		}
	})

	t.Run("excludes by pattern", func(t *testing.T) {
		node := NewFilterNode("excludePattern", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpExclude, Field: "text", Pattern: `^test`},
			},
			OutputVar: "real_items",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "text": "test item 1"},
			{"id": "b", "text": "real content"},
			{"id": "c", "text": "testing again"},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["real_items"].([]any)
		if len(items) != 1 {
			t.Errorf("expected 1 non-test item, got %d", len(items))
		}
	})
}

func TestFilterNode_Custom(t *testing.T) {
	t.Run("custom filter function", func(t *testing.T) {
		node := NewFilterNode("custom", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "numbers",
			Filters: []FilterOp{
				{
					Type: FilterOpCustom,
					CustomFunc: func(item any, index int, env *Envelope) (bool, error) {
						num, ok := item.(int)
						if !ok {
							return false, nil
						}
						return num%2 == 0, nil // Keep even numbers
					},
				},
			},
			OutputVar: "even_numbers",
		})

		env := NewEnvelope()
		env.SetVar("numbers", []int{1, 2, 3, 4, 5, 6})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["even_numbers"].([]any)
		if len(items) != 3 {
			t.Errorf("expected 3 even numbers, got %d", len(items))
		}
	})

	t.Run("custom filter returns error", func(t *testing.T) {
		node := NewFilterNode("customError", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{
					Type: FilterOpCustom,
					CustomFunc: func(item any, index int, env *Envelope) (bool, error) {
						return false, errors.New("custom error")
					},
				},
			},
		})

		env := NewEnvelope()
		env.SetVar("items", []any{"a"})

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error from custom filter")
		}
	})

	t.Run("custom filter respects context cancellation", func(t *testing.T) {
		node := NewFilterNode("customCancel", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{
					Type: FilterOpCustom,
					CustomFunc: func(item any, index int, env *Envelope) (bool, error) {
						return true, nil
					},
				},
			},
		})

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		env := NewEnvelope()
		env.SetVar("items", []any{"a", "b", "c"})

		_, err := node.Run(ctx, env)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})
}

func TestFilterNode_Messages(t *testing.T) {
	t.Run("filters messages by role", func(t *testing.T) {
		node := NewFilterNode("msgFilter", FilterNodeConfig{
			Target: FilterTargetMessages,
			Filters: []FilterOp{
				{Type: FilterOpMatch, Field: "role", Value: "assistant"},
			},
		})

		env := NewEnvelope()
		env.Messages = []Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
			{Role: "user", Content: "How are you?"},
			{Role: "assistant", Content: "I'm doing well"},
		}

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Messages) != 2 {
			t.Errorf("expected 2 assistant messages, got %d", len(result.Messages))
		}
	})
}

func TestFilterNode_Artifacts(t *testing.T) {
	t.Run("filters artifacts with nested meta field", func(t *testing.T) {
		minConf := 0.8
		node := NewFilterNode("artifactFilter", FilterNodeConfig{
			Target: FilterTargetArtifacts,
			Filters: []FilterOp{
				{Type: FilterOpThreshold, ScoreField: "meta.confidence", Min: &minConf},
			},
		})

		env := NewEnvelope()
		env.Artifacts = []Artifact{
			{ID: "1", Type: "chunk", Meta: map[string]any{"confidence": 0.9}},
			{ID: "2", Type: "chunk", Meta: map[string]any{"confidence": 0.5}},
			{ID: "3", Type: "chunk", Meta: map[string]any{"confidence": 0.85}},
		}

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Artifacts) != 2 {
			t.Errorf("expected 2 high-confidence artifacts, got %d", len(result.Artifacts))
		}
	})
}

func TestFilterNode_ChainedFilters(t *testing.T) {
	t.Run("applies multiple filters in order", func(t *testing.T) {
		minConf := 0.5
		node := NewFilterNode("chained", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				// First: filter by confidence
				{Type: FilterOpThreshold, ScoreField: "confidence", Min: &minConf},
				// Then: dedupe by category
				{Type: FilterOpDedupe, Field: "category", Keep: "highest_score", ScoreField: "confidence"},
				// Finally: take top 2
				{Type: FilterOpTopN, N: 2, ScoreField: "confidence"},
			},
			OutputVar: "result",
			StatsVar:  "stats",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"id": "a", "category": "tech", "confidence": 0.3},
			{"id": "b", "category": "tech", "confidence": 0.9},
			{"id": "c", "category": "sports", "confidence": 0.7},
			{"id": "d", "category": "tech", "confidence": 0.6},
			{"id": "e", "category": "news", "confidence": 0.8},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["result"].([]any)
		// After threshold (>=0.5): b, c, d, e (4 items)
		// After dedupe: b (tech), c (sports), e (news) (3 items)
		// After topN(2): b, e (2 items)
		if len(items) != 2 {
			t.Errorf("expected 2 items after chained filters, got %d", len(items))
		}

		stats := result.Vars["stats"].(FilterStats)
		if stats.InputCount != 5 {
			t.Errorf("expected input count 5, got %d", stats.InputCount)
		}
		if stats.OutputCount != 2 {
			t.Errorf("expected output count 2, got %d", stats.OutputCount)
		}
		if stats.Removed != 3 {
			t.Errorf("expected removed 3, got %d", stats.Removed)
		}
	})
}

func TestFilterNode_Stats(t *testing.T) {
	t.Run("records filter statistics", func(t *testing.T) {
		minVal := 0.5
		node := NewFilterNode("stats", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpThreshold, ScoreField: "score", Min: &minVal},
			},
			OutputVar: "result",
			StatsVar:  "filter_stats",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"score": 0.3},
			{"score": 0.6},
			{"score": 0.8},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		stats, ok := result.GetVar("filter_stats")
		if !ok {
			t.Fatal("expected filter_stats variable")
		}

		fs := stats.(FilterStats)
		if fs.InputCount != 3 {
			t.Errorf("expected input count 3, got %d", fs.InputCount)
		}
		if fs.OutputCount != 2 {
			t.Errorf("expected output count 2, got %d", fs.OutputCount)
		}
		if fs.Removed != 1 {
			t.Errorf("expected removed 1, got %d", fs.Removed)
		}
	})
}

func TestFilterNode_InPlaceModification(t *testing.T) {
	t.Run("modifies artifacts in-place when no OutputVar", func(t *testing.T) {
		node := NewFilterNode("inPlace", FilterNodeConfig{
			Target: FilterTargetArtifacts,
			Filters: []FilterOp{
				{Type: FilterOpByType, IncludeTypes: []string{"chunk"}},
			},
			// No OutputVar - modifies envelope directly
		})

		env := NewEnvelope()
		env.Artifacts = []Artifact{
			{ID: "1", Type: "chunk"},
			{ID: "2", Type: "metadata"},
		}

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Result should have filtered artifacts directly
		if len(result.Artifacts) != 1 {
			t.Errorf("expected 1 artifact in-place, got %d", len(result.Artifacts))
		}
	})

	t.Run("replaces input var when no OutputVar for var target", func(t *testing.T) {
		minVal := 0.5
		node := NewFilterNode("replaceVar", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpThreshold, ScoreField: "score", Min: &minVal},
			},
			// No OutputVar - replaces InputVar
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"score": 0.3},
			{"score": 0.8},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		items := result.Vars["items"].([]any)
		if len(items) != 1 {
			t.Errorf("expected 1 item after in-place filter, got %d", len(items))
		}
	})
}

func TestFilterNode_ErrorCases(t *testing.T) {
	t.Run("error when var target without InputVar", func(t *testing.T) {
		node := NewFilterNode("noInput", FilterNodeConfig{
			Target: FilterTargetVar,
			// No InputVar
		})

		env := NewEnvelope()

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for missing InputVar")
		}
	})

	t.Run("error when variable not found", func(t *testing.T) {
		node := NewFilterNode("notFound", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "nonexistent",
		})

		env := NewEnvelope()

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for nonexistent variable")
		}
	})

	t.Run("error when variable is not a slice", func(t *testing.T) {
		node := NewFilterNode("notSlice", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
		})

		env := NewEnvelope()
		env.SetVar("items", "not a slice")

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for non-slice variable")
		}
	})

	t.Run("error for dedupe without field", func(t *testing.T) {
		node := NewFilterNode("dedupeNoField", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpDedupe}, // No Field specified
			},
		})

		env := NewEnvelope()
		env.SetVar("items", []any{"a", "b"})

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for dedupe without Field")
		}
	})

	t.Run("error for invalid regex pattern", func(t *testing.T) {
		node := NewFilterNode("badPattern", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpMatch, Field: "text", Pattern: "[invalid"},
			},
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{{"text": "test"}})

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for invalid regex")
		}
	})

	t.Run("error for custom filter without function", func(t *testing.T) {
		node := NewFilterNode("customNoFunc", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpCustom}, // No CustomFunc
			},
		})

		env := NewEnvelope()
		env.SetVar("items", []any{"a"})

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for custom filter without function")
		}
	})
}

func TestFilterNode_EnvelopeIsolation(t *testing.T) {
	t.Run("original envelope not modified", func(t *testing.T) {
		minVal := 0.5
		node := NewFilterNode("isolated", FilterNodeConfig{
			Target:   FilterTargetVar,
			InputVar: "items",
			Filters: []FilterOp{
				{Type: FilterOpThreshold, ScoreField: "score", Min: &minVal},
			},
			OutputVar: "filtered",
			StatsVar:  "stats",
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"score": 0.3},
			{"score": 0.8},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Original should not have filtered or stats
		_, hasFiltered := env.GetVar("filtered")
		if hasFiltered {
			t.Error("original envelope should not have 'filtered'")
		}

		_, hasStats := env.GetVar("stats")
		if hasStats {
			t.Error("original envelope should not have 'stats'")
		}

		// Result should have both
		_, hasFiltered = result.GetVar("filtered")
		if !hasFiltered {
			t.Error("result envelope should have 'filtered'")
		}
	})
}

func TestExtractValue(t *testing.T) {
	t.Run("extracts from map", func(t *testing.T) {
		item := map[string]any{"name": "test", "meta": map[string]any{"score": 0.8}}

		val, ok := extractValue(item, "name")
		if !ok || val != "test" {
			t.Errorf("expected 'test', got %v", val)
		}

		val, ok = extractValue(item, "meta.score")
		if !ok || val != 0.8 {
			t.Errorf("expected 0.8, got %v", val)
		}
	})

	t.Run("extracts from Artifact", func(t *testing.T) {
		item := Artifact{
			ID:   "123",
			Type: "chunk",
			Meta: map[string]any{"confidence": 0.9},
		}

		val, ok := extractValue(item, "Type")
		if !ok || val != "chunk" {
			t.Errorf("expected 'chunk', got %v", val)
		}

		val, ok = extractValue(item, "meta.confidence")
		if !ok || val != 0.9 {
			t.Errorf("expected 0.9, got %v", val)
		}
	})

	t.Run("extracts from Message", func(t *testing.T) {
		item := Message{
			Role:    "assistant",
			Content: "Hello",
			Meta:    map[string]any{"tokens": 10},
		}

		val, ok := extractValue(item, "role")
		if !ok || val != "assistant" {
			t.Errorf("expected 'assistant', got %v", val)
		}
	})

	t.Run("returns false for missing field", func(t *testing.T) {
		item := map[string]any{"name": "test"}

		_, ok := extractValue(item, "nonexistent")
		if ok {
			t.Error("expected false for missing field")
		}
	})
}
