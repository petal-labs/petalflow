package petalflow

import (
	"context"
	"errors"
	"testing"
)

func TestNewMergeNode(t *testing.T) {
	node := NewMergeNode("test-merge", MergeNodeConfig{})

	if node.ID() != "test-merge" {
		t.Errorf("expected ID 'test-merge', got %q", node.ID())
	}
	if node.Kind() != NodeKindMerge {
		t.Errorf("expected kind %q, got %q", NodeKindMerge, node.Kind())
	}
}

func TestNewMergeNode_DefaultOutputKey(t *testing.T) {
	node := NewMergeNode("my-merger", MergeNodeConfig{})

	config := node.Config()
	if config.OutputKey != "my-merger_output" {
		t.Errorf("expected default output key 'my-merger_output', got %q", config.OutputKey)
	}
}

func TestNewMergeNode_DefaultStrategy(t *testing.T) {
	node := NewMergeNode("test", MergeNodeConfig{})

	config := node.Config()
	if config.Strategy == nil {
		t.Fatal("expected default strategy to be set")
	}
	if config.Strategy.Name() != "json_merge" {
		t.Errorf("expected default strategy 'json_merge', got %q", config.Strategy.Name())
	}
}

func TestMergeNode_Run_SingleEnvelope(t *testing.T) {
	node := NewMergeNode("test", MergeNodeConfig{})

	env := NewEnvelope().WithVar("key", "value")
	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != env {
		t.Error("expected same envelope to be returned for single input")
	}
}

func TestMergeNode_MergeInputs_Empty(t *testing.T) {
	node := NewMergeNode("test", MergeNodeConfig{})

	result, err := node.MergeInputs(context.Background(), []*Envelope{})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil envelope")
	}
}

func TestMergeNode_MergeInputs_Single(t *testing.T) {
	node := NewMergeNode("test", MergeNodeConfig{})

	env := NewEnvelope().WithVar("key", "value")
	result, err := node.MergeInputs(context.Background(), []*Envelope{env})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != env {
		t.Error("expected same envelope for single input")
	}
}

func TestMergeNode_IsMergeNode(t *testing.T) {
	node := NewMergeNode("test", MergeNodeConfig{})

	if !node.IsMergeNode() {
		t.Error("expected IsMergeNode to return true")
	}
}

func TestMergeNode_ExpectedInputs(t *testing.T) {
	node := NewMergeNode("test", MergeNodeConfig{
		ExpectedInputs: 3,
	})

	if node.ExpectedInputs() != 3 {
		t.Errorf("expected 3, got %d", node.ExpectedInputs())
	}
}

// JSONMergeStrategy tests

func TestJSONMergeStrategy_Name(t *testing.T) {
	s := NewJSONMergeStrategy(JSONMergeConfig{})
	if s.Name() != "json_merge" {
		t.Errorf("expected 'json_merge', got %q", s.Name())
	}
}

func TestJSONMergeStrategy_Merge_EmptyInputs(t *testing.T) {
	s := NewJSONMergeStrategy(JSONMergeConfig{})

	result, err := s.Merge(context.Background(), []*Envelope{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil envelope")
	}
}

func TestJSONMergeStrategy_Merge_DisjointVars(t *testing.T) {
	s := NewJSONMergeStrategy(JSONMergeConfig{})

	env1 := NewEnvelope().WithVar("a", 1)
	env2 := NewEnvelope().WithVar("b", 2)
	env3 := NewEnvelope().WithVar("c", 3)

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2, env3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a, ok := result.GetVar("a")
	if !ok || a != 1 {
		t.Errorf("expected a=1, got %v", a)
	}
	b, ok := result.GetVar("b")
	if !ok || b != 2 {
		t.Errorf("expected b=2, got %v", b)
	}
	c, ok := result.GetVar("c")
	if !ok || c != 3 {
		t.Errorf("expected c=3, got %v", c)
	}
}

func TestJSONMergeStrategy_Merge_OverwriteConflict_LastWins(t *testing.T) {
	s := NewJSONMergeStrategy(JSONMergeConfig{
		ConflictPolicy: "last",
	})

	env1 := NewEnvelope().WithVar("key", "first")
	env2 := NewEnvelope().WithVar("key", "second")
	env3 := NewEnvelope().WithVar("key", "third")

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2, env3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, _ := result.GetVar("key")
	if v != "third" {
		t.Errorf("expected 'third', got %v", v)
	}
}

func TestJSONMergeStrategy_Merge_OverwriteConflict_FirstWins(t *testing.T) {
	s := NewJSONMergeStrategy(JSONMergeConfig{
		ConflictPolicy: "first",
	})

	env1 := NewEnvelope().WithVar("key", "first")
	env2 := NewEnvelope().WithVar("key", "second")

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, _ := result.GetVar("key")
	if v != "first" {
		t.Errorf("expected 'first', got %v", v)
	}
}

func TestJSONMergeStrategy_Merge_ConflictError(t *testing.T) {
	s := NewJSONMergeStrategy(JSONMergeConfig{
		ConflictPolicy: "error",
	})

	env1 := NewEnvelope().WithVar("key", "first")
	env2 := NewEnvelope().WithVar("key", "second")

	_, err := s.Merge(context.Background(), []*Envelope{env1, env2})
	if err == nil {
		t.Fatal("expected error for key conflict")
	}
}

func TestJSONMergeStrategy_Merge_DeepMerge(t *testing.T) {
	s := NewJSONMergeStrategy(JSONMergeConfig{
		DeepMerge:      true,
		ConflictPolicy: "last",
	})

	env1 := NewEnvelope().WithVar("config", map[string]any{
		"host": "localhost",
		"port": 8080,
	})
	env2 := NewEnvelope().WithVar("config", map[string]any{
		"port":    9090,
		"timeout": 30,
	})

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	config, _ := result.GetVar("config")
	configMap := config.(map[string]any)

	if configMap["host"] != "localhost" {
		t.Errorf("expected host=localhost, got %v", configMap["host"])
	}
	if configMap["port"] != 9090 {
		t.Errorf("expected port=9090, got %v", configMap["port"])
	}
	if configMap["timeout"] != 30 {
		t.Errorf("expected timeout=30, got %v", configMap["timeout"])
	}
}

func TestJSONMergeStrategy_Merge_MergesArtifacts(t *testing.T) {
	s := NewJSONMergeStrategy(JSONMergeConfig{})

	env1 := NewEnvelope()
	env1.AppendArtifact(Artifact{ID: "a1", Type: "doc"})

	env2 := NewEnvelope()
	env2.AppendArtifact(Artifact{ID: "a2", Type: "doc"})

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Artifacts) != 2 {
		t.Errorf("expected 2 artifacts, got %d", len(result.Artifacts))
	}
}

func TestJSONMergeStrategy_Merge_MergesMessages(t *testing.T) {
	s := NewJSONMergeStrategy(JSONMergeConfig{})

	env1 := NewEnvelope()
	env1.AppendMessage(Message{Role: "user", Content: "Hello"})

	env2 := NewEnvelope()
	env2.AppendMessage(Message{Role: "assistant", Content: "Hi"})

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result.Messages))
	}
}

func TestJSONMergeStrategy_Merge_VarsOnly(t *testing.T) {
	s := NewJSONMergeStrategy(JSONMergeConfig{
		VarsOnly: true,
	})

	env1 := NewEnvelope().WithVar("a", 1)
	env1.AppendArtifact(Artifact{ID: "a1"})

	env2 := NewEnvelope().WithVar("b", 2)
	env2.AppendArtifact(Artifact{ID: "a2"})

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Vars should be merged
	if _, ok := result.GetVar("a"); !ok {
		t.Error("expected var 'a' to exist")
	}
	if _, ok := result.GetVar("b"); !ok {
		t.Error("expected var 'b' to exist")
	}

	// Artifacts from env2 should NOT be merged (VarsOnly=true)
	// Only env1's artifacts should exist (from the clone)
	if len(result.Artifacts) != 1 {
		t.Errorf("expected 1 artifact (VarsOnly=true), got %d", len(result.Artifacts))
	}
}

// ConcatMergeStrategy tests

func TestConcatMergeStrategy_Name(t *testing.T) {
	s := NewConcatMergeStrategy(ConcatMergeConfig{VarName: "text"})
	if s.Name() != "concat_merge" {
		t.Errorf("expected 'concat_merge', got %q", s.Name())
	}
}

func TestConcatMergeStrategy_Merge_Basic(t *testing.T) {
	s := NewConcatMergeStrategy(ConcatMergeConfig{
		VarName:   "result",
		Separator: "\n",
	})

	env1 := NewEnvelope().WithVar("result", "Line 1")
	env2 := NewEnvelope().WithVar("result", "Line 2")
	env3 := NewEnvelope().WithVar("result", "Line 3")

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2, env3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, _ := result.GetVar("result")
	expected := "Line 1\nLine 2\nLine 3"
	if v != expected {
		t.Errorf("expected %q, got %q", expected, v)
	}
}

func TestConcatMergeStrategy_Merge_CustomSeparator(t *testing.T) {
	s := NewConcatMergeStrategy(ConcatMergeConfig{
		VarName:   "items",
		Separator: ", ",
	})

	env1 := NewEnvelope().WithVar("items", "apple")
	env2 := NewEnvelope().WithVar("items", "banana")
	env3 := NewEnvelope().WithVar("items", "cherry")

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2, env3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, _ := result.GetVar("items")
	if v != "apple, banana, cherry" {
		t.Errorf("expected 'apple, banana, cherry', got %q", v)
	}
}

func TestConcatMergeStrategy_Merge_DifferentOutputKey(t *testing.T) {
	s := NewConcatMergeStrategy(ConcatMergeConfig{
		VarName:   "source",
		OutputKey: "combined",
		Separator: "-",
	})

	env1 := NewEnvelope().WithVar("source", "A")
	env2 := NewEnvelope().WithVar("source", "B")

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, _ := result.GetVar("combined")
	if v != "A-B" {
		t.Errorf("expected 'A-B', got %q", v)
	}
}

func TestConcatMergeStrategy_Merge_MissingVar(t *testing.T) {
	s := NewConcatMergeStrategy(ConcatMergeConfig{
		VarName: "text",
	})

	env1 := NewEnvelope().WithVar("text", "A")
	env2 := NewEnvelope() // Missing "text" var
	env3 := NewEnvelope().WithVar("text", "C")

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2, env3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, _ := result.GetVar("text")
	if v != "A\nC" {
		t.Errorf("expected 'A\\nC', got %q", v)
	}
}

func TestConcatMergeStrategy_Merge_NonStringValue(t *testing.T) {
	s := NewConcatMergeStrategy(ConcatMergeConfig{
		VarName:   "value",
		Separator: "-",
	})

	env1 := NewEnvelope().WithVar("value", 42)
	env2 := NewEnvelope().WithVar("value", "text")
	env3 := NewEnvelope().WithVar("value", true)

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2, env3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, _ := result.GetVar("value")
	if v != "42-text-true" {
		t.Errorf("expected '42-text-true', got %q", v)
	}
}

// BestScoreMergeStrategy tests

func TestBestScoreMergeStrategy_Name(t *testing.T) {
	s := NewBestScoreMergeStrategy(BestScoreMergeConfig{ScoreVar: "score"})
	if s.Name() != "best_score_merge" {
		t.Errorf("expected 'best_score_merge', got %q", s.Name())
	}
}

func TestBestScoreMergeStrategy_Merge_HigherIsBetter(t *testing.T) {
	s := NewBestScoreMergeStrategy(BestScoreMergeConfig{
		ScoreVar:       "score",
		HigherIsBetter: true,
	})

	env1 := NewEnvelope().WithVar("score", 0.7).WithVar("model", "A")
	env2 := NewEnvelope().WithVar("score", 0.9).WithVar("model", "B")
	env3 := NewEnvelope().WithVar("score", 0.5).WithVar("model", "C")

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2, env3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	model, _ := result.GetVar("model")
	if model != "B" {
		t.Errorf("expected model 'B' (highest score), got %q", model)
	}
}

func TestBestScoreMergeStrategy_Merge_LowerIsBetter(t *testing.T) {
	s := NewBestScoreMergeStrategy(BestScoreMergeConfig{
		ScoreVar:       "latency",
		HigherIsBetter: false,
	})

	env1 := NewEnvelope().WithVar("latency", 100).WithVar("provider", "A")
	env2 := NewEnvelope().WithVar("latency", 50).WithVar("provider", "B")
	env3 := NewEnvelope().WithVar("latency", 150).WithVar("provider", "C")

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2, env3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	provider, _ := result.GetVar("provider")
	if provider != "B" {
		t.Errorf("expected provider 'B' (lowest latency), got %q", provider)
	}
}

func TestBestScoreMergeStrategy_Merge_NoScores(t *testing.T) {
	s := NewBestScoreMergeStrategy(BestScoreMergeConfig{
		ScoreVar: "score",
	})

	env1 := NewEnvelope().WithVar("data", "A")
	env2 := NewEnvelope().WithVar("data", "B")

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return first non-nil envelope
	data, _ := result.GetVar("data")
	if data != "A" {
		t.Errorf("expected 'A' (first envelope), got %q", data)
	}
}

func TestBestScoreMergeStrategy_Merge_IntScores(t *testing.T) {
	s := NewBestScoreMergeStrategy(BestScoreMergeConfig{
		ScoreVar:       "priority",
		HigherIsBetter: true,
	})

	env1 := NewEnvelope().WithVar("priority", 1).WithVar("name", "low")
	env2 := NewEnvelope().WithVar("priority", 3).WithVar("name", "high")

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	name, _ := result.GetVar("name")
	if name != "high" {
		t.Errorf("expected 'high', got %q", name)
	}
}

// FuncMergeStrategy tests

func TestFuncMergeStrategy_Name(t *testing.T) {
	s := NewFuncMergeStrategy("custom", nil)
	if s.Name() != "custom" {
		t.Errorf("expected 'custom', got %q", s.Name())
	}
}

func TestFuncMergeStrategy_Merge_CustomFunction(t *testing.T) {
	s := NewFuncMergeStrategy("sum_values", func(ctx context.Context, inputs []*Envelope) (*Envelope, error) {
		result := NewEnvelope()
		sum := 0
		for _, input := range inputs {
			if v, ok := input.GetVar("value"); ok {
				if n, ok := v.(int); ok {
					sum += n
				}
			}
		}
		result.SetVar("total", sum)
		return result, nil
	})

	env1 := NewEnvelope().WithVar("value", 10)
	env2 := NewEnvelope().WithVar("value", 20)
	env3 := NewEnvelope().WithVar("value", 30)

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2, env3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	total, _ := result.GetVar("total")
	if total != 60 {
		t.Errorf("expected 60, got %v", total)
	}
}

func TestFuncMergeStrategy_Merge_NilFunction(t *testing.T) {
	s := NewFuncMergeStrategy("passthrough", nil)

	env1 := NewEnvelope().WithVar("key", "value")
	env2 := NewEnvelope().WithVar("other", "data")

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return clone of first envelope
	v, ok := result.GetVar("key")
	if !ok || v != "value" {
		t.Errorf("expected key='value', got %v", v)
	}
}

func TestFuncMergeStrategy_Merge_Error(t *testing.T) {
	s := NewFuncMergeStrategy("failing", func(ctx context.Context, inputs []*Envelope) (*Envelope, error) {
		return nil, errors.New("merge failed")
	})

	env1 := NewEnvelope()
	_, err := s.Merge(context.Background(), []*Envelope{env1})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// AllMergeStrategy tests

func TestAllMergeStrategy_Name(t *testing.T) {
	s := NewAllMergeStrategy(nil, "results", nil)
	if s.Name() != "all_merge" {
		t.Errorf("expected 'all_merge', got %q", s.Name())
	}
}

func TestAllMergeStrategy_Merge_CollectsAllVars(t *testing.T) {
	s := NewAllMergeStrategy(nil, "collected", nil)

	env1 := NewEnvelope().WithVar("a", 1).WithVar("b", 2)
	env2 := NewEnvelope().WithVar("c", 3).WithVar("d", 4)

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	collected, ok := result.GetVar("collected")
	if !ok {
		t.Fatal("expected 'collected' var to exist")
	}

	collectedSlice := collected.([]map[string]any)
	if len(collectedSlice) != 2 {
		t.Errorf("expected 2 items in collected, got %d", len(collectedSlice))
	}

	// Check first item has env1's vars
	if collectedSlice[0]["a"] != 1 {
		t.Errorf("expected a=1 in first item, got %v", collectedSlice[0]["a"])
	}
}

func TestAllMergeStrategy_Merge_CollectsSpecificVars(t *testing.T) {
	s := NewAllMergeStrategy(nil, "scores", []string{"score"})

	env1 := NewEnvelope().WithVar("score", 0.9).WithVar("other", "ignored")
	env2 := NewEnvelope().WithVar("score", 0.7).WithVar("other", "also_ignored")

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	collected, ok := result.GetVar("scores")
	if !ok {
		t.Fatal("expected 'scores' var to exist")
	}

	collectedSlice := collected.([]map[string]any)
	// Check that only 'score' was collected
	if _, exists := collectedSlice[0]["other"]; exists {
		t.Error("expected 'other' to not be collected")
	}
	if collectedSlice[0]["score"] != 0.9 {
		t.Errorf("expected score=0.9, got %v", collectedSlice[0]["score"])
	}
}

func TestAllMergeStrategy_Merge_UsesInnerStrategy(t *testing.T) {
	inner := NewConcatMergeStrategy(ConcatMergeConfig{
		VarName:   "text",
		Separator: "|",
	})
	s := NewAllMergeStrategy(inner, "all", nil)

	env1 := NewEnvelope().WithVar("text", "A")
	env2 := NewEnvelope().WithVar("text", "B")

	result, err := s.Merge(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Inner strategy should have concatenated the text
	text, _ := result.GetVar("text")
	if text != "A|B" {
		t.Errorf("expected 'A|B', got %q", text)
	}

	// AllMerge should have collected all vars
	all, ok := result.GetVar("all")
	if !ok {
		t.Fatal("expected 'all' var to exist")
	}
	if len(all.([]map[string]any)) != 2 {
		t.Errorf("expected 2 items collected, got %d", len(all.([]map[string]any)))
	}
}

// MergeNode with strategies integration tests

func TestMergeNode_MergeInputs_WithJSONStrategy(t *testing.T) {
	node := NewMergeNode("test", MergeNodeConfig{
		Strategy: NewJSONMergeStrategy(JSONMergeConfig{
			ConflictPolicy: "last",
		}),
	})

	env1 := NewEnvelope().WithVar("a", 1).WithVar("shared", "first")
	env2 := NewEnvelope().WithVar("b", 2).WithVar("shared", "second")

	result, err := node.MergeInputs(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a, _ := result.GetVar("a")
	b, _ := result.GetVar("b")
	shared, _ := result.GetVar("shared")

	if a != 1 || b != 2 {
		t.Errorf("expected a=1, b=2, got a=%v, b=%v", a, b)
	}
	if shared != "second" {
		t.Errorf("expected shared='second', got %v", shared)
	}
}

func TestMergeNode_MergeInputs_WithConcatStrategy(t *testing.T) {
	node := NewMergeNode("test", MergeNodeConfig{
		Strategy: NewConcatMergeStrategy(ConcatMergeConfig{
			VarName:   "response",
			Separator: " ",
		}),
	})

	env1 := NewEnvelope().WithVar("response", "Hello")
	env2 := NewEnvelope().WithVar("response", "World")

	result, err := node.MergeInputs(context.Background(), []*Envelope{env1, env2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	response, _ := result.GetVar("response")
	if response != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", response)
	}
}

func TestMergeNode_MergeInputs_WithBestScoreStrategy(t *testing.T) {
	node := NewMergeNode("test", MergeNodeConfig{
		Strategy: NewBestScoreMergeStrategy(BestScoreMergeConfig{
			ScoreVar:       "confidence",
			HigherIsBetter: true,
		}),
	})

	env1 := NewEnvelope().WithVar("confidence", 0.6).WithVar("answer", "Maybe")
	env2 := NewEnvelope().WithVar("confidence", 0.95).WithVar("answer", "Yes")
	env3 := NewEnvelope().WithVar("confidence", 0.3).WithVar("answer", "No")

	result, err := node.MergeInputs(context.Background(), []*Envelope{env1, env2, env3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	answer, _ := result.GetVar("answer")
	if answer != "Yes" {
		t.Errorf("expected 'Yes' (highest confidence), got %q", answer)
	}
}

func TestMergeNode_InterfaceCompliance(t *testing.T) {
	var _ Node = (*MergeNode)(nil)
}
