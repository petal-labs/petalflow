package hydrate

import (
	"context"
	"testing"

	"github.com/petal-labs/petalflow/tool"
)

type testToolStore struct {
	regs map[string]tool.ToolRegistration
}

func (s *testToolStore) List(_ context.Context) ([]tool.ToolRegistration, error) {
	out := make([]tool.ToolRegistration, 0, len(s.regs))
	for _, reg := range s.regs {
		out = append(out, reg)
	}
	return out, nil
}

func (s *testToolStore) Get(_ context.Context, name string) (tool.ToolRegistration, bool, error) {
	reg, ok := s.regs[name]
	return reg, ok, nil
}

func (s *testToolStore) Upsert(_ context.Context, reg tool.ToolRegistration) error {
	if s.regs == nil {
		s.regs = make(map[string]tool.ToolRegistration)
	}
	s.regs[reg.Name] = reg
	return nil
}

func (s *testToolStore) Delete(_ context.Context, name string) error {
	delete(s.regs, name)
	return nil
}

func TestBuildActionToolRegistry_IncludesBuiltinActions(t *testing.T) {
	registry, err := BuildActionToolRegistry(context.Background(), nil)
	if err != nil {
		t.Fatalf("BuildActionToolRegistry() error = %v", err)
	}

	tmplTool, ok := registry.Get("template_render.render")
	if !ok {
		t.Fatal("expected template_render.render to be registered")
	}

	outputs, err := tmplTool.Invoke(context.Background(), map[string]any{
		"template": "Hello, {{.name}}!",
		"name":     "Ada",
	})
	if err != nil {
		t.Fatalf("template_render.render invoke error = %v", err)
	}
	if got := outputs["rendered"]; got != "Hello, Ada!" {
		t.Fatalf("rendered output = %v, want %q", got, "Hello, Ada!")
	}
}

func TestBuildActionToolRegistry_IncludesStoredActions(t *testing.T) {
	manifest := tool.NewManifest("custom_tool")
	manifest.Transport = tool.NewNativeTransport()
	manifest.Actions["execute"] = tool.ActionSpec{
		Description: "Run custom action",
	}

	store := &testToolStore{
		regs: map[string]tool.ToolRegistration{
			"custom_tool": {
				Name:     "custom_tool",
				Origin:   tool.OriginNative,
				Manifest: manifest,
				Status:   tool.StatusReady,
				Enabled:  true,
			},
		},
	}

	registry, err := BuildActionToolRegistry(context.Background(), store)
	if err != nil {
		t.Fatalf("BuildActionToolRegistry() error = %v", err)
	}

	if _, ok := registry.Get("custom_tool.execute"); !ok {
		t.Fatal("expected custom_tool.execute to be registered")
	}
}

func TestBuildActionToolRegistry_SkipsDisabledTools(t *testing.T) {
	manifest := tool.NewManifest("disabled_tool")
	manifest.Transport = tool.NewNativeTransport()
	manifest.Actions["execute"] = tool.ActionSpec{
		Description: "Run disabled action",
	}

	store := &testToolStore{
		regs: map[string]tool.ToolRegistration{
			"disabled_tool": {
				Name:     "disabled_tool",
				Origin:   tool.OriginNative,
				Manifest: manifest,
				Status:   tool.StatusDisabled,
				Enabled:  false,
			},
		},
	}

	registry, err := BuildActionToolRegistry(context.Background(), store)
	if err != nil {
		t.Fatalf("BuildActionToolRegistry() error = %v", err)
	}

	if _, ok := registry.Get("disabled_tool.execute"); ok {
		t.Fatal("disabled_tool.execute should not be registered")
	}
}
