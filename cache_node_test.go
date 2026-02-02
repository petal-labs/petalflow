package petalflow

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCacheNode_CacheMiss(t *testing.T) {
	tests := []struct {
		name        string
		inputVars   map[string]any
		mockValue   any
		wantCallCnt int
	}{
		{
			name:        "executes wrapped node on miss",
			inputVars:   map[string]any{"query": "hello"},
			mockValue:   "result",
			wantCallCnt: 1,
		},
		{
			name:        "stores result in cache",
			inputVars:   map[string]any{"data": 123},
			mockValue:   map[string]any{"computed": true},
			wantCallCnt: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNode := NewMockNode("mock", tt.mockValue)
			store := NewMemoryCacheStore()

			node := NewCacheNode("cache1", CacheNodeConfig{
				WrappedNode: mockNode,
				Store:       store,
				OutputVar:   "cache_meta",
			})

			env := NewEnvelope()
			for k, v := range tt.inputVars {
				env.SetVar(k, v)
			}

			result, err := node.Run(context.Background(), env)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check wrapped node was called
			if mockNode.GetCallCount() != tt.wantCallCnt {
				t.Errorf("call count = %d, want %d", mockNode.GetCallCount(), tt.wantCallCnt)
			}

			// Check cache metadata
			meta, ok := result.GetVar("cache_meta")
			if !ok {
				t.Fatal("cache_meta not set")
			}
			cacheResult, ok := meta.(CacheResult)
			if !ok {
				t.Fatalf("cache_meta wrong type: %T", meta)
			}
			if cacheResult.Hit {
				t.Error("expected cache miss, got hit")
			}
			if cacheResult.Key == "" {
				t.Error("cache key is empty")
			}

			// Check result from mock was included
			if _, ok := result.GetVar("mock_result"); !ok {
				t.Error("mock_result not in result envelope")
			}

			// Verify cache now has entry
			if store.Size() != 1 {
				t.Errorf("store size = %d, want 1", store.Size())
			}
		})
	}
}

func TestCacheNode_CacheHit(t *testing.T) {
	tests := []struct {
		name      string
		inputVars map[string]any
	}{
		{
			name:      "returns cached result on hit",
			inputVars: map[string]any{"query": "hello"},
		},
		{
			name:      "does not execute wrapped node on hit",
			inputVars: map[string]any{"x": 1, "y": 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNode := NewMockNode("mock", "expensive_result")
			store := NewMemoryCacheStore()

			node := NewCacheNode("cache1", CacheNodeConfig{
				WrappedNode: mockNode,
				Store:       store,
				OutputVar:   "cache_meta",
			})

			env := NewEnvelope()
			for k, v := range tt.inputVars {
				env.SetVar(k, v)
			}

			// First call - cache miss
			_, err := node.Run(context.Background(), env)
			if err != nil {
				t.Fatalf("first call error: %v", err)
			}

			// Reset and verify
			initialCallCount := mockNode.GetCallCount()
			if initialCallCount != 1 {
				t.Fatalf("expected 1 call after first run, got %d", initialCallCount)
			}

			// Second call - same inputs, should hit cache
			result, err := node.Run(context.Background(), env)
			if err != nil {
				t.Fatalf("second call error: %v", err)
			}

			// Wrapped node should NOT be called again
			if mockNode.GetCallCount() != 1 {
				t.Errorf("wrapped node called again, count = %d", mockNode.GetCallCount())
			}

			// Check cache hit
			meta, _ := result.GetVar("cache_meta")
			cacheResult := meta.(CacheResult)
			if !cacheResult.Hit {
				t.Error("expected cache hit, got miss")
			}
		})
	}
}

func TestCacheNode_TTLExpiration(t *testing.T) {
	mockNode := NewMockNode("mock", "value")
	store := NewMemoryCacheStore()

	node := NewCacheNode("cache1", CacheNodeConfig{
		WrappedNode: mockNode,
		Store:       store,
		TTL:         50 * time.Millisecond,
		OutputVar:   "cache_meta",
	})

	env := NewEnvelope()
	env.SetVar("key", "value")

	// First call - cache miss
	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if mockNode.GetCallCount() != 1 {
		t.Errorf("expected 1 call, got %d", mockNode.GetCallCount())
	}

	// Second call immediately - should hit
	result, _ := node.Run(context.Background(), env)
	meta, _ := result.GetVar("cache_meta")
	if !meta.(CacheResult).Hit {
		t.Error("expected cache hit before TTL expiration")
	}
	if mockNode.GetCallCount() != 1 {
		t.Errorf("expected 1 call (cache hit), got %d", mockNode.GetCallCount())
	}

	// Wait for TTL to expire
	time.Sleep(60 * time.Millisecond)

	// Third call - should miss due to TTL
	result, _ = node.Run(context.Background(), env)
	meta, _ = result.GetVar("cache_meta")
	if meta.(CacheResult).Hit {
		t.Error("expected cache miss after TTL expiration")
	}
	if mockNode.GetCallCount() != 2 {
		t.Errorf("expected 2 calls after TTL expiration, got %d", mockNode.GetCallCount())
	}
}

func TestCacheNode_CacheKeyTemplate(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		inputVars map[string]any
		wantKey   string
	}{
		{
			name:      "simple var reference",
			template:  "query:{{.vars.query}}",
			inputVars: map[string]any{"query": "hello"},
			wantKey:   "cache1:query:hello",
		},
		{
			name:      "multiple vars",
			template:  "{{.vars.a}}-{{.vars.b}}",
			inputVars: map[string]any{"a": "foo", "b": "bar"},
			wantKey:   "cache1:foo-bar",
		},
		{
			name:      "input reference",
			template:  "input:{{.input}}",
			inputVars: map[string]any{},
			wantKey:   "cache1:input:test_input",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNode := NewMockNode("mock", "result")

			node := NewCacheNode("cache1", CacheNodeConfig{
				CacheKey:    tt.template,
				WrappedNode: mockNode,
				OutputVar:   "cache_meta",
			})

			env := NewEnvelope()
			env.Input = "test_input"
			for k, v := range tt.inputVars {
				env.SetVar(k, v)
			}

			result, err := node.Run(context.Background(), env)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			meta, _ := result.GetVar("cache_meta")
			cacheResult := meta.(CacheResult)
			if cacheResult.Key != tt.wantKey {
				t.Errorf("cache key = %q, want %q", cacheResult.Key, tt.wantKey)
			}
		})
	}
}

func TestCacheNode_StableHash(t *testing.T) {
	// Same inputs in different order should produce same cache key
	mockNode := NewMockNode("mock", "result")
	store := NewMemoryCacheStore()

	node := NewCacheNode("cache1", CacheNodeConfig{
		WrappedNode: mockNode,
		Store:       store,
		OutputVar:   "cache_meta",
	})

	// First call with vars in one order
	env1 := NewEnvelope()
	env1.SetVar("a", "1")
	env1.SetVar("b", "2")
	env1.SetVar("c", "3")

	result1, _ := node.Run(context.Background(), env1)
	meta1, _ := result1.GetVar("cache_meta")
	key1 := meta1.(CacheResult).Key

	// Second call with same vars in different order
	env2 := NewEnvelope()
	env2.SetVar("c", "3")
	env2.SetVar("a", "1")
	env2.SetVar("b", "2")

	result2, _ := node.Run(context.Background(), env2)
	meta2, _ := result2.GetVar("cache_meta")
	key2 := meta2.(CacheResult).Key

	if key1 != key2 {
		t.Errorf("keys should match for same inputs:\nkey1 = %s\nkey2 = %s", key1, key2)
	}

	// Should be a cache hit
	meta := meta2.(CacheResult)
	if !meta.Hit {
		t.Error("expected cache hit for same inputs in different order")
	}

	// Wrapped node should only be called once
	if mockNode.GetCallCount() != 1 {
		t.Errorf("call count = %d, want 1", mockNode.GetCallCount())
	}
}

func TestCacheNode_InputVars(t *testing.T) {
	tests := []struct {
		name      string
		inputVars []string
		env1Vars  map[string]any
		env2Vars  map[string]any
		wantHit   bool
	}{
		{
			name:      "only selected vars affect key",
			inputVars: []string{"query"},
			env1Vars:  map[string]any{"query": "hello", "timestamp": 1},
			env2Vars:  map[string]any{"query": "hello", "timestamp": 2},
			wantHit:   true,
		},
		{
			name:      "different selected vars cause miss",
			inputVars: []string{"query"},
			env1Vars:  map[string]any{"query": "hello"},
			env2Vars:  map[string]any{"query": "world"},
			wantHit:   false,
		},
		{
			name:      "unselected vars ignored",
			inputVars: []string{"a"},
			env1Vars:  map[string]any{"a": 1, "b": 100},
			env2Vars:  map[string]any{"a": 1, "b": 999},
			wantHit:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNode := NewMockNode("mock", "result")
			store := NewMemoryCacheStore()

			node := NewCacheNode("cache1", CacheNodeConfig{
				InputVars:   tt.inputVars,
				WrappedNode: mockNode,
				Store:       store,
				OutputVar:   "cache_meta",
			})

			// First call
			env1 := NewEnvelope()
			for k, v := range tt.env1Vars {
				env1.SetVar(k, v)
			}
			node.Run(context.Background(), env1)

			// Second call
			env2 := NewEnvelope()
			for k, v := range tt.env2Vars {
				env2.SetVar(k, v)
			}
			result, _ := node.Run(context.Background(), env2)

			metaVal, _ := result.GetVar("cache_meta")
			meta := metaVal.(CacheResult)
			if meta.Hit != tt.wantHit {
				t.Errorf("cache hit = %v, want %v", meta.Hit, tt.wantHit)
			}
		})
	}
}

func TestCacheNode_IncludeInput(t *testing.T) {
	mockNode := NewMockNode("mock", "result")
	store := NewMemoryCacheStore()

	node := NewCacheNode("cache1", CacheNodeConfig{
		IncludeInput: true,
		InputVars:    []string{}, // Empty to isolate input effect
		WrappedNode:  mockNode,
		Store:        store,
		OutputVar:    "cache_meta",
	})

	// First call
	env1 := NewEnvelope()
	env1.Input = "input_a"
	node.Run(context.Background(), env1)

	// Second call with same input - should hit
	env2 := NewEnvelope()
	env2.Input = "input_a"
	result, _ := node.Run(context.Background(), env2)

	metaVal, _ := result.GetVar("cache_meta")
	meta := metaVal.(CacheResult)
	if !meta.Hit {
		t.Error("expected cache hit for same input")
	}

	// Third call with different input - should miss
	env3 := NewEnvelope()
	env3.Input = "input_b"
	result, _ = node.Run(context.Background(), env3)

	metaVal, _ = result.GetVar("cache_meta")
	meta = metaVal.(CacheResult)
	if meta.Hit {
		t.Error("expected cache miss for different input")
	}
}

func TestCacheNode_IncludeArtifacts(t *testing.T) {
	mockNode := NewMockNode("mock", "result")
	store := NewMemoryCacheStore()

	node := NewCacheNode("cache1", CacheNodeConfig{
		IncludeArtifacts: true,
		InputVars:        []string{}, // Empty to isolate artifacts effect
		WrappedNode:      mockNode,
		Store:            store,
		OutputVar:        "cache_meta",
	})

	// First call with artifact
	env1 := NewEnvelope()
	env1.AppendArtifact(Artifact{ID: "art1", Type: "text", Text: "hello"})
	node.Run(context.Background(), env1)

	// Second call with same artifact - should hit
	env2 := NewEnvelope()
	env2.AppendArtifact(Artifact{ID: "art1", Type: "text", Text: "hello"})
	result, _ := node.Run(context.Background(), env2)

	metaVal, _ := result.GetVar("cache_meta")
	meta := metaVal.(CacheResult)
	if !meta.Hit {
		t.Error("expected cache hit for same artifacts")
	}

	// Third call with different artifact - should miss
	env3 := NewEnvelope()
	env3.AppendArtifact(Artifact{ID: "art2", Type: "text", Text: "world"})
	result, _ = node.Run(context.Background(), env3)

	metaVal, _ = result.GetVar("cache_meta")
	meta = metaVal.(CacheResult)
	if meta.Hit {
		t.Error("expected cache miss for different artifacts")
	}
}

func TestCacheNode_EnvelopeIsolation(t *testing.T) {
	mockNode := NewMockNode("mock", "result")
	store := NewMemoryCacheStore()

	node := NewCacheNode("cache1", CacheNodeConfig{
		WrappedNode: mockNode,
		Store:       store,
		OutputVar:   "cache_meta",
	})

	env := NewEnvelope()
	env.SetVar("original", "value")

	result, _ := node.Run(context.Background(), env)

	// Modify result
	result.SetVar("modified", "new_value")

	// Original should not be modified
	if _, ok := env.GetVar("modified"); ok {
		t.Error("original envelope was modified")
	}
	if _, ok := env.GetVar("cache_meta"); ok {
		t.Error("cache_meta leaked to original envelope")
	}
}

func TestCacheNode_ContextCancellation(t *testing.T) {
	mockNode := NewMockNode("mock", "result")

	node := NewCacheNode("cache1", CacheNodeConfig{
		WrappedNode: mockNode,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	env := NewEnvelope()

	_, err := node.Run(ctx, env)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestCacheNode_NoWrappedNode(t *testing.T) {
	node := NewCacheNode("cache1", CacheNodeConfig{
		// No WrappedNode configured
	})

	env := NewEnvelope()

	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Error("expected error for missing wrapped node")
	}
}

func TestCacheNode_WrappedNodeError(t *testing.T) {
	mockNode := NewMockNode("mock", nil)
	mockNode.ReturnError = errors.New("wrapped node failed")

	node := NewCacheNode("cache1", CacheNodeConfig{
		WrappedNode: mockNode,
	})

	env := NewEnvelope()

	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Error("expected error from wrapped node")
	}
	if !errors.Is(err, mockNode.ReturnError) {
		// Check if wrapped error message is present
		if err.Error() == "" {
			t.Error("expected wrapped error message")
		}
	}
}

func TestCacheNode_InvalidTemplate(t *testing.T) {
	mockNode := NewMockNode("mock", "result")

	node := NewCacheNode("cache1", CacheNodeConfig{
		CacheKey:    "{{.invalid syntax",
		WrappedNode: mockNode,
	})

	env := NewEnvelope()

	_, err := node.Run(context.Background(), env)
	if err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestCacheNode_ID(t *testing.T) {
	node := NewCacheNode("my_cache", CacheNodeConfig{})

	if node.ID() != "my_cache" {
		t.Errorf("ID = %q, want %q", node.ID(), "my_cache")
	}
}

func TestCacheNode_Kind(t *testing.T) {
	node := NewCacheNode("cache1", CacheNodeConfig{})

	if node.Kind() != NodeKindCache {
		t.Errorf("Kind = %q, want %q", node.Kind(), NodeKindCache)
	}
}

// MemoryCacheStore tests

func TestMemoryCacheStore_GetSet(t *testing.T) {
	store := NewMemoryCacheStore()
	ctx := context.Background()

	env := NewEnvelope()
	env.SetVar("data", "value")

	// Set
	err := store.Set(ctx, "key1", env, 0)
	if err != nil {
		t.Fatalf("Set error: %v", err)
	}

	// Get
	retrieved, found, err := store.Get(ctx, "key1")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if !found {
		t.Fatal("key not found")
	}

	val, ok := retrieved.GetVar("data")
	if !ok || val != "value" {
		t.Errorf("retrieved value = %v, want 'value'", val)
	}
}

func TestMemoryCacheStore_GetNotFound(t *testing.T) {
	store := NewMemoryCacheStore()
	ctx := context.Background()

	_, found, err := store.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if found {
		t.Error("expected not found")
	}
}

func TestMemoryCacheStore_Delete(t *testing.T) {
	store := NewMemoryCacheStore()
	ctx := context.Background()

	env := NewEnvelope()
	store.Set(ctx, "key1", env, 0)

	err := store.Delete(ctx, "key1")
	if err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	_, found, _ := store.Get(ctx, "key1")
	if found {
		t.Error("key should be deleted")
	}
}

func TestMemoryCacheStore_TTLExpiration(t *testing.T) {
	store := NewMemoryCacheStore()
	ctx := context.Background()

	env := NewEnvelope()
	store.Set(ctx, "key1", env, 50*time.Millisecond)

	// Should find immediately
	_, found, _ := store.Get(ctx, "key1")
	if !found {
		t.Error("expected to find before TTL")
	}

	// Wait for expiration
	time.Sleep(60 * time.Millisecond)

	// Should not find after TTL
	_, found, _ = store.Get(ctx, "key1")
	if found {
		t.Error("expected not to find after TTL")
	}
}

func TestMemoryCacheStore_Size(t *testing.T) {
	store := NewMemoryCacheStore()
	ctx := context.Background()

	if store.Size() != 0 {
		t.Errorf("initial size = %d, want 0", store.Size())
	}

	store.Set(ctx, "key1", NewEnvelope(), 0)
	store.Set(ctx, "key2", NewEnvelope(), 0)

	if store.Size() != 2 {
		t.Errorf("size = %d, want 2", store.Size())
	}
}

func TestMemoryCacheStore_Clear(t *testing.T) {
	store := NewMemoryCacheStore()
	ctx := context.Background()

	store.Set(ctx, "key1", NewEnvelope(), 0)
	store.Set(ctx, "key2", NewEnvelope(), 0)

	store.Clear()

	if store.Size() != 0 {
		t.Errorf("size after clear = %d, want 0", store.Size())
	}
}

func TestMemoryCacheStore_Keys(t *testing.T) {
	store := NewMemoryCacheStore()
	ctx := context.Background()

	store.Set(ctx, "charlie", NewEnvelope(), 0)
	store.Set(ctx, "alpha", NewEnvelope(), 0)
	store.Set(ctx, "bravo", NewEnvelope(), 0)

	keys := store.Keys()

	// Should be sorted
	expected := []string{"alpha", "bravo", "charlie"}
	if len(keys) != len(expected) {
		t.Fatalf("keys length = %d, want %d", len(keys), len(expected))
	}
	for i, k := range keys {
		if k != expected[i] {
			t.Errorf("keys[%d] = %q, want %q", i, k, expected[i])
		}
	}
}

func TestMemoryCacheStore_Prune(t *testing.T) {
	store := NewMemoryCacheStore()
	ctx := context.Background()

	// Add mix of expiring and non-expiring
	store.Set(ctx, "expire1", NewEnvelope(), 10*time.Millisecond)
	store.Set(ctx, "expire2", NewEnvelope(), 10*time.Millisecond)
	store.Set(ctx, "persist", NewEnvelope(), 0) // No TTL

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	pruned := store.Prune()

	if pruned != 2 {
		t.Errorf("pruned = %d, want 2", pruned)
	}
	if store.Size() != 1 {
		t.Errorf("size after prune = %d, want 1", store.Size())
	}

	// Verify persistent key still exists
	_, found, _ := store.Get(ctx, "persist")
	if !found {
		t.Error("persistent key should still exist")
	}
}

func TestMemoryCacheStore_ContextCancellation(t *testing.T) {
	store := NewMemoryCacheStore()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Get should return error
	_, _, err := store.Get(ctx, "key")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Get: expected context.Canceled, got %v", err)
	}

	// Set should return error
	err = store.Set(ctx, "key", NewEnvelope(), 0)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Set: expected context.Canceled, got %v", err)
	}

	// Delete should return error
	err = store.Delete(ctx, "key")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Delete: expected context.Canceled, got %v", err)
	}
}

// Stable hash tests

func TestStableJSONMarshal(t *testing.T) {
	// Same data with different key insertion order should produce same JSON
	data1 := map[string]any{
		"a": 1,
		"b": 2,
		"c": 3,
	}

	data2 := map[string]any{
		"c": 3,
		"a": 1,
		"b": 2,
	}

	json1, err := stableJSONMarshal(data1)
	if err != nil {
		t.Fatalf("marshal data1: %v", err)
	}

	json2, err := stableJSONMarshal(data2)
	if err != nil {
		t.Fatalf("marshal data2: %v", err)
	}

	if string(json1) != string(json2) {
		t.Errorf("JSON not stable:\njson1 = %s\njson2 = %s", json1, json2)
	}
}

func TestComputeStableHash(t *testing.T) {
	data1 := map[string]any{"x": 1, "y": 2}
	data2 := map[string]any{"y": 2, "x": 1}

	hash1, _ := computeStableHash(data1)
	hash2, _ := computeStableHash(data2)

	if hash1 != hash2 {
		t.Errorf("hashes should match:\nhash1 = %s\nhash2 = %s", hash1, hash2)
	}

	// Different data should produce different hash
	data3 := map[string]any{"x": 1, "y": 3}
	hash3, _ := computeStableHash(data3)

	if hash1 == hash3 {
		t.Error("different data should produce different hash")
	}
}

func TestComputeStableHash_NestedMaps(t *testing.T) {
	data1 := map[string]any{
		"outer": map[string]any{
			"a": 1,
			"b": 2,
		},
	}

	data2 := map[string]any{
		"outer": map[string]any{
			"b": 2,
			"a": 1,
		},
	}

	hash1, _ := computeStableHash(data1)
	hash2, _ := computeStableHash(data2)

	if hash1 != hash2 {
		t.Error("nested maps with same content should produce same hash")
	}
}

// CacheKeyBuilder tests

func TestCacheKeyBuilder(t *testing.T) {
	builder := NewCacheKeyBuilder()
	builder.Add("query", "hello")
	builder.Add("version", 2)

	key := builder.Build()

	// Should be sorted (strings are used directly, numbers JSON marshaled)
	expected := "query=hello&version=2"
	if key != expected {
		t.Errorf("key = %q, want %q", key, expected)
	}
}

func TestCacheKeyBuilder_Hash(t *testing.T) {
	builder1 := NewCacheKeyBuilder()
	builder1.Add("a", 1)
	builder1.Add("b", 2)

	builder2 := NewCacheKeyBuilder()
	builder2.Add("b", 2)
	builder2.Add("a", 1)

	hash1 := builder1.Hash()
	hash2 := builder2.Hash()

	if hash1 != hash2 {
		t.Errorf("hashes should match:\nhash1 = %s\nhash2 = %s", hash1, hash2)
	}
}

// MockNode tests

func TestMockNode_Run(t *testing.T) {
	mock := NewMockNode("mock1", "test_value")

	env := NewEnvelope()
	result, err := mock.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, ok := result.GetVar("mock_result")
	if !ok || val != "test_value" {
		t.Errorf("mock_result = %v, want 'test_value'", val)
	}

	if mock.GetCallCount() != 1 {
		t.Errorf("call count = %d, want 1", mock.GetCallCount())
	}
}

func TestMockNode_ReturnError(t *testing.T) {
	mock := NewMockNode("mock1", nil)
	mock.ReturnError = errors.New("test error")

	env := NewEnvelope()
	_, err := mock.Run(context.Background(), env)
	if !errors.Is(err, mock.ReturnError) {
		t.Errorf("expected error %v, got %v", mock.ReturnError, err)
	}
}
