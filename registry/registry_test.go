package registry

import (
	"sync"
	"testing"
)

func TestGlobal_ReturnsSameInstance(t *testing.T) {
	r1 := Global()
	r2 := Global()
	if r1 != r2 {
		t.Error("Global() should return the same instance on every call")
	}
}

func TestGlobal_HasBuiltins(t *testing.T) {
	r := Global()
	if r.Len() == 0 {
		t.Fatal("Global registry should have built-in types registered")
	}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := newRegistry()
	def := NodeTypeDef{
		Type:        "test_node",
		Category:    "test",
		DisplayName: "Test Node",
		Description: "A test node",
		Ports: PortSchema{
			Inputs:  []PortDef{{Name: "in", Type: "string", Required: true}},
			Outputs: []PortDef{{Name: "out", Type: "string"}},
		},
	}

	r.Register(def)

	got, ok := r.Get("test_node")
	if !ok {
		t.Fatal("Get should find registered type")
	}
	if got.Type != "test_node" {
		t.Errorf("Type = %q, want %q", got.Type, "test_node")
	}
	if got.DisplayName != "Test Node" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Test Node")
	}
	if len(got.Ports.Inputs) != 1 {
		t.Errorf("Inputs count = %d, want 1", len(got.Ports.Inputs))
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	r := newRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("Get should return false for unregistered type")
	}
}

func TestRegistry_Has(t *testing.T) {
	r := newRegistry()
	r.Register(NodeTypeDef{Type: "exists"})

	if !r.Has("exists") {
		t.Error("Has should return true for registered type")
	}
	if r.Has("missing") {
		t.Error("Has should return false for unregistered type")
	}
}

func TestRegistry_HasTool(t *testing.T) {
	r := newRegistry()
	r.Register(NodeTypeDef{Type: "web_search", IsTool: true, ToolMode: "function_call"})
	r.Register(NodeTypeDef{Type: "transform", IsTool: false})

	if !r.HasTool("web_search") {
		t.Error("HasTool should return true for tool types")
	}
	if r.HasTool("transform") {
		t.Error("HasTool should return false for non-tool types")
	}
	if r.HasTool("nonexistent") {
		t.Error("HasTool should return false for unregistered types")
	}
}

func TestRegistry_ToolMode(t *testing.T) {
	r := newRegistry()
	r.Register(NodeTypeDef{Type: "web_search", IsTool: true, ToolMode: "function_call"})
	r.Register(NodeTypeDef{Type: "pdf_extract", IsTool: true, ToolMode: "standalone"})
	r.Register(NodeTypeDef{Type: "transform"})

	tests := []struct {
		name string
		want string
	}{
		{"web_search", "function_call"},
		{"pdf_extract", "standalone"},
		{"transform", ""},
		{"nonexistent", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.ToolMode(tt.name)
			if got != tt.want {
				t.Errorf("ToolMode(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestRegistry_All_PreservesOrder(t *testing.T) {
	r := newRegistry()
	r.Register(NodeTypeDef{Type: "alpha"})
	r.Register(NodeTypeDef{Type: "beta"})
	r.Register(NodeTypeDef{Type: "gamma"})

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("All() returned %d items, want 3", len(all))
	}
	expected := []string{"alpha", "beta", "gamma"}
	for i, want := range expected {
		if all[i].Type != want {
			t.Errorf("All()[%d].Type = %q, want %q", i, all[i].Type, want)
		}
	}
}

func TestRegistry_RegisterOverwrite(t *testing.T) {
	r := newRegistry()
	r.Register(NodeTypeDef{Type: "node", DisplayName: "Original"})
	r.Register(NodeTypeDef{Type: "node", DisplayName: "Updated"})

	got, _ := r.Get("node")
	if got.DisplayName != "Updated" {
		t.Errorf("DisplayName = %q, want %q (should overwrite)", got.DisplayName, "Updated")
	}
	// Should not duplicate in order
	if r.Len() != 1 {
		t.Errorf("Len = %d, want 1 (overwrite should not duplicate)", r.Len())
	}
}

func TestRegistry_Len(t *testing.T) {
	r := newRegistry()
	if r.Len() != 0 {
		t.Errorf("empty registry Len = %d, want 0", r.Len())
	}
	r.Register(NodeTypeDef{Type: "a"})
	r.Register(NodeTypeDef{Type: "b"})
	if r.Len() != 2 {
		t.Errorf("Len = %d, want 2", r.Len())
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := newRegistry()
	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.Register(NodeTypeDef{Type: "concurrent"})
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Get("concurrent")
			r.Has("concurrent")
			r.HasTool("concurrent")
			r.ToolMode("concurrent")
			r.All()
			r.Len()
		}()
	}

	wg.Wait()
	// If we get here without data race panic, the test passes
}

// --- Builtin registration tests ---

func TestBuiltins_AllExpectedTypesRegistered(t *testing.T) {
	r := Global()
	expected := []string{
		"llm_prompt",
		"llm_router",
		"rule_router",
		"filter",
		"transform",
		"merge",
		"tool",
		"gate",
		"guardian",
		"human",
		"map",
		"cache",
		"sink",
		"noop",
		"func",
	}

	for _, typeName := range expected {
		if !r.Has(typeName) {
			t.Errorf("built-in type %q not registered", typeName)
		}
	}
}

func TestBuiltins_Categories(t *testing.T) {
	r := Global()
	tests := []struct {
		typeName string
		category string
	}{
		{"llm_prompt", "ai"},
		{"llm_router", "ai"},
		{"rule_router", "control"},
		{"filter", "data"},
		{"transform", "data"},
		{"merge", "control"},
		{"tool", "tool"},
		{"gate", "control"},
		{"guardian", "control"},
		{"human", "control"},
		{"map", "control"},
		{"cache", "data"},
		{"sink", "data"},
		{"noop", "control"},
		{"func", "control"},
	}

	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			def, ok := r.Get(tt.typeName)
			if !ok {
				t.Fatalf("type %q not found", tt.typeName)
			}
			if def.Category != tt.category {
				t.Errorf("Category = %q, want %q", def.Category, tt.category)
			}
		})
	}
}

func TestBuiltins_ToolNodeIsMarkedAsTool(t *testing.T) {
	r := Global()

	if !r.HasTool("tool") {
		t.Error("tool type should be marked as a tool")
	}
	if r.ToolMode("tool") != "standalone" {
		t.Errorf("tool ToolMode = %q, want %q", r.ToolMode("tool"), "standalone")
	}
}

func TestBuiltins_NonToolTypes(t *testing.T) {
	r := Global()
	nonTools := []string{"llm_prompt", "filter", "transform", "merge", "gate", "noop"}
	for _, typeName := range nonTools {
		if r.HasTool(typeName) {
			t.Errorf("%q should not be marked as a tool", typeName)
		}
	}
}

func TestBuiltins_AllHavePorts(t *testing.T) {
	r := Global()
	for _, def := range r.All() {
		if len(def.Ports.Outputs) == 0 {
			t.Errorf("type %q has no output ports", def.Type)
		}
	}
}

func TestBuiltins_AllHaveDisplayName(t *testing.T) {
	r := Global()
	for _, def := range r.All() {
		if def.DisplayName == "" {
			t.Errorf("type %q has empty display name", def.Type)
		}
		if def.Description == "" {
			t.Errorf("type %q has empty description", def.Type)
		}
	}
}
