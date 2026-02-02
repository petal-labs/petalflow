package petalflow

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewMapNode(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		node := NewMapNode("mapper", MapNodeConfig{
			InputVar: "items",
			Mapper:   func(ctx context.Context, item any, index int) (any, error) { return item, nil },
		})

		if node.ID() != "mapper" {
			t.Errorf("expected ID 'mapper', got %q", node.ID())
		}
		if node.Kind() != NodeKindMap {
			t.Errorf("expected kind %v, got %v", NodeKindMap, node.Kind())
		}

		config := node.Config()
		if config.OutputVar != "mapper_output" {
			t.Errorf("expected default OutputVar 'mapper_output', got %q", config.OutputVar)
		}
		if config.ItemVar != "item" {
			t.Errorf("expected default ItemVar 'item', got %q", config.ItemVar)
		}
		if config.Concurrency != 1 {
			t.Errorf("expected default Concurrency 1, got %d", config.Concurrency)
		}
	})

	t.Run("custom config", func(t *testing.T) {
		node := NewMapNode("custom", MapNodeConfig{
			InputVar:    "input_list",
			OutputVar:   "output_list",
			ItemVar:     "element",
			IndexVar:    "idx",
			Concurrency: 4,
			Mapper:      func(ctx context.Context, item any, index int) (any, error) { return item, nil },
		})

		config := node.Config()
		if config.InputVar != "input_list" {
			t.Errorf("expected InputVar 'input_list', got %q", config.InputVar)
		}
		if config.OutputVar != "output_list" {
			t.Errorf("expected OutputVar 'output_list', got %q", config.OutputVar)
		}
		if config.ItemVar != "element" {
			t.Errorf("expected ItemVar 'element', got %q", config.ItemVar)
		}
		if config.IndexVar != "idx" {
			t.Errorf("expected IndexVar 'idx', got %q", config.IndexVar)
		}
		if config.Concurrency != 4 {
			t.Errorf("expected Concurrency 4, got %d", config.Concurrency)
		}
	})
}

func TestMapNode_Run_FunctionMapper(t *testing.T) {
	t.Run("basic mapping", func(t *testing.T) {
		node := NewMapNode("double", MapNodeConfig{
			InputVar: "numbers",
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				n := item.(int)
				return n * 2, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("numbers", []int{1, 2, 3, 4, 5})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output, ok := result.GetVar("double_output")
		if !ok {
			t.Fatal("expected output variable to exist")
		}

		results := output.([]any)
		expected := []int{2, 4, 6, 8, 10}
		for i, v := range results {
			if v.(int) != expected[i] {
				t.Errorf("index %d: expected %d, got %v", i, expected[i], v)
			}
		}
	})

	t.Run("string slice", func(t *testing.T) {
		node := NewMapNode("upper", MapNodeConfig{
			InputVar: "words",
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				s := item.(string)
				return fmt.Sprintf("%s_%d", s, index), nil
			},
		})

		env := NewEnvelope()
		env.SetVar("words", []string{"a", "b", "c"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["upper_output"].([]any)
		expected := []string{"a_0", "b_1", "c_2"}
		for i, v := range output {
			if v.(string) != expected[i] {
				t.Errorf("index %d: expected %q, got %v", i, expected[i], v)
			}
		}
	})

	t.Run("float64 slice", func(t *testing.T) {
		node := NewMapNode("half", MapNodeConfig{
			InputVar: "floats",
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				f := item.(float64)
				return f / 2, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("floats", []float64{10.0, 20.0, 30.0})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["half_output"].([]any)
		expected := []float64{5.0, 10.0, 15.0}
		for i, v := range output {
			if v.(float64) != expected[i] {
				t.Errorf("index %d: expected %f, got %v", i, expected[i], v)
			}
		}
	})

	t.Run("map slice", func(t *testing.T) {
		node := NewMapNode("addField", MapNodeConfig{
			InputVar: "items",
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				m := item.(map[string]any)
				m["processed"] = true
				m["index"] = index
				return m, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"name": "a"},
			{"name": "b"},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["addField_output"].([]any)
		if len(output) != 2 {
			t.Fatalf("expected 2 items, got %d", len(output))
		}

		first := output[0].(map[string]any)
		if first["processed"] != true {
			t.Error("expected processed=true")
		}
		if first["index"] != 0 {
			t.Errorf("expected index=0, got %v", first["index"])
		}
	})

	t.Run("any slice passthrough", func(t *testing.T) {
		node := NewMapNode("identity", MapNodeConfig{
			InputVar: "mixed",
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				return item, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("mixed", []any{1, "two", 3.0})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["identity_output"].([]any)
		if len(output) != 3 {
			t.Fatalf("expected 3 items, got %d", len(output))
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		node := NewMapNode("empty", MapNodeConfig{
			InputVar: "items",
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				return item, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("items", []any{})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["empty_output"].([]any)
		if len(output) != 0 {
			t.Errorf("expected empty slice, got %d items", len(output))
		}
	})
}

func TestMapNode_Run_NodeMapper(t *testing.T) {
	t.Run("basic node mapping", func(t *testing.T) {
		// Create a simple noop mapper that stores result
		mapperNode := &testMapperNode{
			id: "itemProcessor",
			transform: func(env *Envelope) *Envelope {
				item, _ := env.GetVar("item")
				n := item.(int)
				env.SetVar("result", n*10)
				return env
			},
		}

		node := NewMapNode("nodeMap", MapNodeConfig{
			InputVar:   "numbers",
			MapperNode: mapperNode,
		})

		env := NewEnvelope()
		env.SetVar("numbers", []int{1, 2, 3})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["nodeMap_output"].([]any)
		if len(output) != 3 {
			t.Fatalf("expected 3 items, got %d", len(output))
		}

		// Each result should be the vars map from the mapper node
		for i, item := range output {
			vars := item.(map[string]any)
			expected := (i + 1) * 10
			if vars["result"] != expected {
				t.Errorf("index %d: expected result=%d, got %v", i, expected, vars["result"])
			}
		}
	})

	t.Run("with index variable", func(t *testing.T) {
		mapperNode := &testMapperNode{
			id: "indexer",
			transform: func(env *Envelope) *Envelope {
				idx, _ := env.GetVar("idx")
				env.SetVar("doubled_index", idx.(int)*2)
				return env
			},
		}

		node := NewMapNode("indexed", MapNodeConfig{
			InputVar:   "items",
			IndexVar:   "idx",
			MapperNode: mapperNode,
		})

		env := NewEnvelope()
		env.SetVar("items", []string{"a", "b", "c"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["indexed_output"].([]any)
		for i, item := range output {
			vars := item.(map[string]any)
			expected := i * 2
			if vars["doubled_index"] != expected {
				t.Errorf("index %d: expected doubled_index=%d, got %v", i, expected, vars["doubled_index"])
			}
		}
	})
}

func TestMapNode_Run_Concurrent(t *testing.T) {
	t.Run("concurrent processing", func(t *testing.T) {
		var callCount atomic.Int32

		node := NewMapNode("concurrent", MapNodeConfig{
			InputVar:    "items",
			Concurrency: 4,
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				callCount.Add(1)
				time.Sleep(10 * time.Millisecond) // Simulate work
				n := item.(int)
				return n * 2, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("items", []int{1, 2, 3, 4, 5, 6, 7, 8})

		start := time.Now()
		result, err := node.Run(context.Background(), env)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// With 4 workers and 8 items at 10ms each, should take ~20-30ms not 80ms
		if elapsed > 60*time.Millisecond {
			t.Errorf("concurrent processing took too long: %v (expected < 60ms)", elapsed)
		}

		if callCount.Load() != 8 {
			t.Errorf("expected 8 calls, got %d", callCount.Load())
		}

		// Verify order is preserved
		output := result.Vars["concurrent_output"].([]any)
		expected := []int{2, 4, 6, 8, 10, 12, 14, 16}
		for i, v := range output {
			if v.(int) != expected[i] {
				t.Errorf("index %d: expected %d, got %v (order may not be preserved)", i, expected[i], v)
			}
		}
	})

	t.Run("concurrent preserves order", func(t *testing.T) {
		node := NewMapNode("ordered", MapNodeConfig{
			InputVar:      "items",
			Concurrency:   10,
			PreserveOrder: true,
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				// Variable sleep to cause out-of-order completion
				time.Sleep(time.Duration((10-index)*5) * time.Millisecond)
				return fmt.Sprintf("%d-%v", index, item), nil
			},
		})

		env := NewEnvelope()
		env.SetVar("items", []any{0, 1, 2, 3, 4, 5, 6, 7, 8, 9})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["ordered_output"].([]any)
		for i, v := range output {
			expected := fmt.Sprintf("%d-%d", i, i)
			if v.(string) != expected {
				t.Errorf("index %d: expected %q, got %v", i, expected, v)
			}
		}
	})
}

func TestMapNode_Run_ErrorHandling(t *testing.T) {
	t.Run("error stops processing", func(t *testing.T) {
		var callCount atomic.Int32

		node := NewMapNode("failing", MapNodeConfig{
			InputVar: "items",
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				callCount.Add(1)
				if index == 2 {
					return nil, errors.New("item 2 failed")
				}
				return item, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("items", []int{0, 1, 2, 3, 4})

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error")
		}
		if callCount.Load() != 3 {
			t.Errorf("expected processing to stop at 3 calls, got %d", callCount.Load())
		}
	})

	t.Run("continue on error", func(t *testing.T) {
		var callCount atomic.Int32

		node := NewMapNode("resilient", MapNodeConfig{
			InputVar:        "items",
			ContinueOnError: true,
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				callCount.Add(1)
				if index%2 == 0 {
					return nil, fmt.Errorf("item %d failed", index)
				}
				return item.(int) * 2, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("items", []int{0, 1, 2, 3, 4})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error with ContinueOnError: %v", err)
		}

		if callCount.Load() != 5 {
			t.Errorf("expected all 5 items processed, got %d", callCount.Load())
		}

		output := result.Vars["resilient_output"].([]any)
		// Items 0, 2, 4 should be nil (errors), items 1, 3 should be doubled
		if output[0] != nil {
			t.Errorf("expected nil for failed item 0, got %v", output[0])
		}
		if output[1].(int) != 2 {
			t.Errorf("expected 2 for item 1, got %v", output[1])
		}
		if output[2] != nil {
			t.Errorf("expected nil for failed item 2, got %v", output[2])
		}
		if output[3].(int) != 6 {
			t.Errorf("expected 6 for item 3, got %v", output[3])
		}
		if output[4] != nil {
			t.Errorf("expected nil for failed item 4, got %v", output[4])
		}
	})

	t.Run("concurrent error stops other workers", func(t *testing.T) {
		var callCount atomic.Int32

		// Use more items and longer sleep to ensure cancellation has time to propagate
		node := NewMapNode("concurrentFail", MapNodeConfig{
			InputVar:    "items",
			Concurrency: 2, // Fewer workers to make cancellation more observable
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				callCount.Add(1)
				// Item 0 fails immediately, others take longer
				if index == 0 {
					return nil, errors.New("early failure")
				}
				time.Sleep(50 * time.Millisecond)
				return item, nil
			},
		})

		env := NewEnvelope()
		// Use many items so cancellation can be observed
		items := make([]int, 20)
		for i := range items {
			items[i] = i
		}
		env.SetVar("items", items)

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error")
		}

		// The error should have been returned, and context cancellation should have
		// prevented some later items from processing. Due to worker pool buffering,
		// some items beyond the failing one will have started, but we shouldn't
		// process all 20 items since the early failure triggers cancellation.
		if callCount.Load() >= 20 {
			t.Errorf("expected fewer than 20 calls due to early cancellation, got %d", callCount.Load())
		}
	})

	t.Run("concurrent continue on error", func(t *testing.T) {
		var callCount atomic.Int32

		node := NewMapNode("concurrentResilient", MapNodeConfig{
			InputVar:        "items",
			Concurrency:     4,
			ContinueOnError: true,
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				callCount.Add(1)
				if index%2 == 0 {
					return nil, fmt.Errorf("item %d failed", index)
				}
				return item.(int) * 2, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("items", []int{0, 1, 2, 3, 4, 5, 6, 7})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error with ContinueOnError: %v", err)
		}

		if callCount.Load() != 8 {
			t.Errorf("expected all 8 items processed, got %d", callCount.Load())
		}

		output := result.Vars["concurrentResilient_output"].([]any)
		// Items 0, 2, 4, 6 should be nil (errors), items 1, 3, 5, 7 should be doubled
		for i, v := range output {
			if i%2 == 0 {
				if v != nil {
					t.Errorf("expected nil for failed item %d, got %v", i, v)
				}
			} else {
				expected := i * 2
				if v.(int) != expected {
					t.Errorf("expected %d for item %d, got %v", expected, i, v)
				}
			}
		}
	})
}

func TestMapNode_Run_ContextCancellation(t *testing.T) {
	t.Run("context cancelled during sequential", func(t *testing.T) {
		var callCount atomic.Int32

		node := NewMapNode("cancellable", MapNodeConfig{
			InputVar: "items",
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				callCount.Add(1)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(50 * time.Millisecond):
					return item, nil
				}
			},
		})

		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
		defer cancel()

		env := NewEnvelope()
		env.SetVar("items", []int{1, 2, 3, 4, 5})

		_, err := node.Run(ctx, env)
		if err == nil {
			t.Fatal("expected context error")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected DeadlineExceeded, got %v", err)
		}

		// Should have processed 1-2 items before timeout
		if callCount.Load() > 2 {
			t.Errorf("expected 1-2 calls before cancellation, got %d", callCount.Load())
		}
	})

	t.Run("context cancelled during concurrent", func(t *testing.T) {
		var callCount atomic.Int32

		node := NewMapNode("concurrentCancel", MapNodeConfig{
			InputVar:    "items",
			Concurrency: 4,
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				callCount.Add(1)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(100 * time.Millisecond):
					return item, nil
				}
			},
		})

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		env := NewEnvelope()
		env.SetVar("items", []int{1, 2, 3, 4, 5, 6, 7, 8})

		_, err := node.Run(ctx, env)
		if err == nil {
			t.Fatal("expected context error")
		}

		// Workers should have been cancelled
		if callCount.Load() > 8 {
			t.Errorf("expected at most 8 calls (one per item), got %d", callCount.Load())
		}
	})
}

func TestMapNode_Run_Validation(t *testing.T) {
	t.Run("missing input variable", func(t *testing.T) {
		node := NewMapNode("missing", MapNodeConfig{
			InputVar: "nonexistent",
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				return item, nil
			},
		})

		env := NewEnvelope()

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for missing input variable")
		}
	})

	t.Run("nil input", func(t *testing.T) {
		node := NewMapNode("nilInput", MapNodeConfig{
			InputVar: "items",
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				return item, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("items", nil)

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for nil input")
		}
	})

	t.Run("non-slice input", func(t *testing.T) {
		node := NewMapNode("notSlice", MapNodeConfig{
			InputVar: "item",
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				return item, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("item", "not a slice")

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for non-slice input")
		}
	})

	t.Run("no mapper configured", func(t *testing.T) {
		node := NewMapNode("noMapper", MapNodeConfig{
			InputVar: "items",
			// No Mapper or MapperNode
		})

		env := NewEnvelope()
		env.SetVar("items", []int{1, 2, 3})

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for missing mapper")
		}
	})
}

func TestMapNode_EnvelopeIsolation(t *testing.T) {
	t.Run("original envelope not modified", func(t *testing.T) {
		node := NewMapNode("isolated", MapNodeConfig{
			InputVar: "items",
			Mapper: func(ctx context.Context, item any, index int) (any, error) {
				return item.(int) * 2, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("items", []int{1, 2, 3})
		env.SetVar("original", "should remain")

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Original envelope should not have the output
		_, hasOutput := env.GetVar("isolated_output")
		if hasOutput {
			t.Error("original envelope should not have output variable")
		}

		// Original vars should still exist in result
		original, _ := result.GetVar("original")
		if original != "should remain" {
			t.Errorf("expected original var preserved, got %v", original)
		}
	})
}

// testMapperNode is a helper node for testing node-based mapping
type testMapperNode struct {
	BaseNode
	id        string
	transform func(*Envelope) *Envelope
}

func (n *testMapperNode) ID() string {
	return n.id
}

func (n *testMapperNode) Kind() NodeKind {
	return NodeKindNoop
}

func (n *testMapperNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	return n.transform(env), nil
}
