package daemon

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/petal-labs/petalflow/tool"
)

// MemoryToolStore is an in-memory tool registration store for daemon mode.
type MemoryToolStore struct {
	mu    sync.RWMutex
	items map[string]tool.ToolRegistration
}

// NewMemoryToolStore creates an empty in-memory tool store.
func NewMemoryToolStore() *MemoryToolStore {
	return &MemoryToolStore{
		items: make(map[string]tool.ToolRegistration),
	}
}

// List returns all registrations in deterministic name order.
func (s *MemoryToolStore) List(ctx context.Context) ([]tool.ToolRegistration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.items))
	for name := range s.items {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]tool.ToolRegistration, 0, len(names))
	for _, name := range names {
		out = append(out, cloneRegistration(s.items[name]))
	}
	return out, nil
}

// Get returns one registration by name.
func (s *MemoryToolStore) Get(ctx context.Context, name string) (tool.ToolRegistration, bool, error) {
	if err := ctx.Err(); err != nil {
		return tool.ToolRegistration{}, false, err
	}

	clean := strings.TrimSpace(name)
	s.mu.RLock()
	defer s.mu.RUnlock()
	reg, ok := s.items[clean]
	if !ok {
		return tool.ToolRegistration{}, false, nil
	}
	return cloneRegistration(reg), true, nil
}

// Upsert inserts or updates one registration.
func (s *MemoryToolStore) Upsert(ctx context.Context, reg tool.ToolRegistration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	clean := strings.TrimSpace(reg.Name)
	if clean == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	reg.Name = clean
	s.items[clean] = cloneRegistration(reg)
	return nil
}

// Delete removes one registration by name.
func (s *MemoryToolStore) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	clean := strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, clean)
	return nil
}

var _ tool.Store = (*MemoryToolStore)(nil)

func cloneRegistration(in tool.ToolRegistration) tool.ToolRegistration {
	out := in
	out.Manifest = cloneManifest(in.Manifest)
	out.Config = cloneStringMap(in.Config)
	if in.Overlay != nil {
		overlay := *in.Overlay
		out.Overlay = &overlay
	}
	return out
}

func cloneManifest(in tool.ToolManifest) tool.ToolManifest {
	out := in
	out.Actions = make(map[string]tool.ActionSpec, len(in.Actions))
	for name, action := range in.Actions {
		copied := action
		copied.Inputs = cloneFieldMap(action.Inputs)
		copied.Outputs = cloneFieldMap(action.Outputs)
		if action.LLMCallable != nil {
			value := *action.LLMCallable
			copied.LLMCallable = &value
		}
		out.Actions[name] = copied
	}
	out.Config = cloneFieldMap(in.Config)
	out.Transport.Args = append([]string(nil), in.Transport.Args...)
	out.Transport.Env = cloneStringMap(in.Transport.Env)
	if in.Health != nil {
		health := *in.Health
		out.Health = &health
	}
	return out
}

func cloneFieldMap(in map[string]tool.FieldSpec) map[string]tool.FieldSpec {
	if in == nil {
		return nil
	}
	out := make(map[string]tool.FieldSpec, len(in))
	for key, value := range in {
		copied := value
		if value.Items != nil {
			item := *value.Items
			copied.Items = &item
		}
		if value.Properties != nil {
			copied.Properties = cloneFieldMap(value.Properties)
		}
		out[key] = copied
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
