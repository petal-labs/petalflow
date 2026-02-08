// Package registry provides a global node-type registry for PetalFlow.
// It maps type names to metadata (ports, config schema, tool status) used
// by the Agent/Task validator, compiler, server API, and UI.
package registry

import "sync"

// NodeTypeDef describes a registered node type.
type NodeTypeDef struct {
	Type         string     `json:"type"`
	Category     string     `json:"category"` // "ai", "tool", "control", "data"
	DisplayName  string     `json:"display_name"`
	Description  string     `json:"description"`
	Ports        PortSchema `json:"ports"`
	ConfigSchema any        `json:"config_schema"` // JSON Schema for config validation
	IsTool       bool       `json:"is_tool"`       // usable as an agent tool
	ToolMode     string     `json:"tool_mode"`     // "function_call" | "standalone" | ""
}

// PortSchema defines the input and output ports for a node type.
type PortSchema struct {
	Inputs  []PortDef `json:"inputs"`
	Outputs []PortDef `json:"outputs"`
}

// PortDef describes a single port on a node type.
type PortDef struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "string", "object", "array", "any"
	Required bool   `json:"required"`
}

var (
	global     *Registry
	globalOnce sync.Once
)

// Global returns the singleton registry instance. On first call it
// initializes the registry and auto-registers all built-in node types.
func Global() *Registry {
	globalOnce.Do(func() {
		global = newRegistry()
		registerBuiltins(global)
	})
	return global
}

// Registry holds all known node types.
type Registry struct {
	mu    sync.RWMutex
	types map[string]NodeTypeDef
	order []string // preserves registration order
}

func newRegistry() *Registry {
	return &Registry{
		types: make(map[string]NodeTypeDef),
	}
}

// Register adds a node type definition. If a type with the same name
// already exists it is overwritten.
func (r *Registry) Register(def NodeTypeDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.types[def.Type]; !exists {
		r.order = append(r.order, def.Type)
	}
	r.types[def.Type] = def
}

// Get returns a node type definition by type name.
func (r *Registry) Get(typeName string) (NodeTypeDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.types[typeName]
	return def, ok
}

// Has returns true if the type name is registered.
func (r *Registry) Has(typeName string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.types[typeName]
	return ok
}

// HasTool checks if a tool ID exists and is marked as a tool.
// Used by the AgentTask validator for AT-004.
func (r *Registry) HasTool(toolID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.types[toolID]
	return ok && def.IsTool
}

// ToolMode returns the tool mode for a given tool ID.
// Used by the compiler to decide function_call vs standalone node.
// Returns empty string if the tool is not found.
func (r *Registry) ToolMode(toolID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.types[toolID]
	if !ok {
		return ""
	}
	return def.ToolMode
}

// All returns all registered node types in registration order.
// Used by GET /api/node-types endpoint.
func (r *Registry) All() []NodeTypeDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]NodeTypeDef, 0, len(r.order))
	for _, name := range r.order {
		result = append(result, r.types[name])
	}
	return result
}

// Len returns the number of registered node types.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.types)
}
