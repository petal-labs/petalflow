package graph

import (
	"fmt"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/registry"
)

// Diagnostic represents a validation error or warning produced by schema
// or graph validation. Used by both graph and agent validators.
type Diagnostic struct {
	Code     string `json:"code"`           // e.g. "GR-001", "AT-003"
	Severity string `json:"severity"`       // "error" or "warning"
	Message  string `json:"message"`        // human-readable description
	Path     string `json:"path,omitempty"` // JSON path to offending field
	Line     int    `json:"line,omitempty"` // source line number (0 if unavailable)
}

const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// HasErrors returns true if any diagnostic has error severity.
func HasErrors(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Errors returns only the error-severity diagnostics.
func Errors(diags []Diagnostic) []Diagnostic {
	var errs []Diagnostic
	for _, d := range diags {
		if d.Severity == SeverityError {
			errs = append(errs, d)
		}
	}
	return errs
}

// Warnings returns only the warning-severity diagnostics.
func Warnings(diags []Diagnostic) []Diagnostic {
	var warns []Diagnostic
	for _, d := range diags {
		if d.Severity == SeverityWarning {
			warns = append(warns, d)
		}
	}
	return warns
}

// GraphDefinition is the serializable intermediate representation of a workflow.
// Both the Agent/Task compiler and direct JSON/YAML loading produce this type.
// The Runtime consumes it to build an executable Graph.
type GraphDefinition struct {
	ID       string            `json:"id"`
	Version  string            `json:"version"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Nodes    []NodeDef         `json:"nodes"`
	Edges    []EdgeDef         `json:"edges"`
	Entry    string            `json:"entry,omitempty"`
}

// NodeDef is a serializable node within a GraphDefinition.
type NodeDef struct {
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	Config map[string]any `json:"config,omitempty"`
}

// EdgeDef is a serializable edge within a GraphDefinition.
type EdgeDef struct {
	Source       string `json:"source"`
	SourceHandle string `json:"sourceHandle"`
	Target       string `json:"target"`
	TargetHandle string `json:"targetHandle"`
}

// Validate checks structural integrity of the GraphDefinition.
// It checks rules that can be verified without a node registry:
//   - GR-001: edge source/target reference existing nodes
//   - GR-002: orphan nodes (warning)
//   - GR-004: topological sort (cycle detection)
//   - GR-005: duplicate node IDs
//   - GR-007: entry references existing node
//
// Registry-dependent rules (GR-003, GR-006, GR-008) require a registry
// and are checked via ValidateWithRegistry.
func (gd *GraphDefinition) Validate() []Diagnostic {
	var diags []Diagnostic

	nodeIDs := make(map[string]bool, len(gd.Nodes))

	// GR-005: duplicate node IDs
	for i, node := range gd.Nodes {
		if nodeIDs[node.ID] {
			diags = append(diags, Diagnostic{
				Code:     "GR-005",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Duplicate node ID %q", node.ID),
				Path:     fmt.Sprintf("nodes[%d].id", i),
			})
		}
		nodeIDs[node.ID] = true
	}

	// GR-001: edge source/target must reference existing nodes
	for i, edge := range gd.Edges {
		if !nodeIDs[edge.Source] {
			diags = append(diags, Diagnostic{
				Code:     "GR-001",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Edge source %q references unknown node", edge.Source),
				Path:     fmt.Sprintf("edges[%d].source", i),
			})
		}
		if !nodeIDs[edge.Target] {
			diags = append(diags, Diagnostic{
				Code:     "GR-001",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Edge target %q references unknown node", edge.Target),
				Path:     fmt.Sprintf("edges[%d].target", i),
			})
		}
	}

	// GR-007: entry must reference an existing node
	if gd.Entry != "" && !nodeIDs[gd.Entry] {
		diags = append(diags, Diagnostic{
			Code:     "GR-007",
			Severity: SeverityError,
			Message:  fmt.Sprintf("Entry node %q does not exist", gd.Entry),
			Path:     "entry",
		})
	}

	// GR-002: orphan nodes — nodes with no inbound and no outbound edges
	if len(gd.Nodes) > 1 {
		hasInbound := make(map[string]bool)
		hasOutbound := make(map[string]bool)
		for _, edge := range gd.Edges {
			hasOutbound[edge.Source] = true
			hasInbound[edge.Target] = true
		}
		for i, node := range gd.Nodes {
			if !hasInbound[node.ID] && !hasOutbound[node.ID] {
				diags = append(diags, Diagnostic{
					Code:     "GR-002",
					Severity: SeverityWarning,
					Message:  fmt.Sprintf("Node %q has no inbound or outbound edges", node.ID),
					Path:     fmt.Sprintf("nodes[%d]", i),
				})
			}
		}
	}

	// GR-004: cycle detection via topological sort (Kahn's algorithm)
	// Only run if edges reference valid nodes to avoid confusion.
	if !hasEdgeRefErrors(diags) {
		if cycle := gd.detectCycle(); cycle != "" {
			diags = append(diags, Diagnostic{
				Code:     "GR-004",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Graph contains a cycle: %s", cycle),
			})
		}
	}

	// CN-*: conditional node validation
	diags = append(diags, gd.validateConditionalNodes(nodeIDs)...)

	return diags
}

// ValidateWithRegistry runs structural validation plus registry-dependent checks:
//   - GR-003: node type must exist in the registry
//   - GR-006: source handle should map to a declared output port when static
//   - GR-008: function_call tools cannot be used as standalone graph nodes
func (gd *GraphDefinition) ValidateWithRegistry(reg *registry.Registry) []Diagnostic {
	diags := gd.Validate()
	if reg == nil {
		return diags
	}

	// Collect node definitions for edge validation.
	nodesByID := make(map[string]NodeDef, len(gd.Nodes))
	defsByNodeID := make(map[string]registry.NodeTypeDef, len(gd.Nodes))
	dynamicOutputs := map[string]bool{
		"conditional": true,
	}

	for i, node := range gd.Nodes {
		nodesByID[node.ID] = node

		def, ok := reg.Get(node.Type)
		if !ok {
			diags = append(diags, Diagnostic{
				Code:     "GR-003",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Node %q references unknown type %q", node.ID, node.Type),
				Path:     fmt.Sprintf("nodes[%d].type", i),
			})
			continue
		}
		defsByNodeID[node.ID] = def

		// function_call tools are intended for model-invoked tool use, not graph nodes.
		if def.IsTool && def.ToolMode == "function_call" {
			diags = append(diags, Diagnostic{
				Code:     "GR-008",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Node %q uses function_call tool type %q as a standalone graph node", node.ID, node.Type),
				Path:     fmt.Sprintf("nodes[%d].type", i),
			})
		}
	}

	// Validate source handles where port sets are static.
	for i, edge := range gd.Edges {
		if edge.SourceHandle == "" {
			continue
		}

		srcNode, ok := nodesByID[edge.Source]
		if !ok {
			continue
		}
		if dynamicOutputs[srcNode.Type] {
			continue
		}

		srcDef, ok := defsByNodeID[edge.Source]
		if !ok {
			continue
		}

		if !hasPortName(srcDef.Ports.Outputs, edge.SourceHandle) {
			diags = append(diags, Diagnostic{
				Code:     "GR-006",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Edge sourceHandle %q is not an output port on node %q (type %q)", edge.SourceHandle, edge.Source, srcNode.Type),
				Path:     fmt.Sprintf("edges[%d].sourceHandle", i),
			})
		}
	}

	// GR-009: webhook_trigger nodes must not have inbound edges.
	inboundCount := make(map[string]int, len(gd.Nodes))
	for _, edge := range gd.Edges {
		inboundCount[edge.Target]++
	}
	for i, node := range gd.Nodes {
		if node.Type != "webhook_trigger" {
			continue
		}
		if inboundCount[node.ID] > 0 {
			diags = append(diags, Diagnostic{
				Code:     "GR-009",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Node %q (webhook_trigger) must not have inbound edges", node.ID),
				Path:     fmt.Sprintf("nodes[%d]", i),
			})
		}
	}

	return diags
}

func hasPortName(ports []registry.PortDef, name string) bool {
	for _, port := range ports {
		if port.Name == name {
			return true
		}
	}
	return false
}

// ExprValidator is a function that checks expression syntax.
// Returns nil if valid, error describing the syntax problem otherwise.
// Set via SetExprValidator to avoid import cycles with the expr package.
type ExprValidator func(expression string) error

var registeredExprValidator ExprValidator

// SetExprValidator registers the expression syntax checker. Called once at init
// time by the conditional/expr package or a wiring package.
func SetExprValidator(v ExprValidator) {
	registeredExprValidator = v
}

// GetExprValidator returns the registered expression validator, or nil.
func GetExprValidator() ExprValidator {
	return registeredExprValidator
}

// validateConditionalNodes runs conditional-specific validation rules.
func (gd *GraphDefinition) validateConditionalNodes(nodeIDs map[string]bool) []Diagnostic {
	var diags []Diagnostic

	// Build edge lookup: source -> set of targets
	outEdges := make(map[string]map[string]bool)
	for _, edge := range gd.Edges {
		if outEdges[edge.Source] == nil {
			outEdges[edge.Source] = make(map[string]bool)
		}
		outEdges[edge.Source][edge.Target] = true
	}

	reservedPorts := map[string]bool{"error": true, "_metadata": true}

	for i, node := range gd.Nodes {
		if node.Type != "conditional" {
			continue
		}
		prefix := fmt.Sprintf("nodes[%d]", i)

		conditionsRaw, ok := node.Config["conditions"]
		if !ok {
			diags = append(diags, Diagnostic{
				Code:     "CN-006",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Conditional node %q must have at least one condition", node.ID),
				Path:     prefix + ".config.conditions",
			})
			continue
		}

		conditions, ok := conditionsRaw.([]any)
		if !ok {
			diags = append(diags, Diagnostic{
				Code:     "CN-006",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Conditional node %q: conditions must be an array", node.ID),
				Path:     prefix + ".config.conditions",
			})
			continue
		}

		if len(conditions) == 0 {
			diags = append(diags, Diagnostic{
				Code:     "CN-006",
				Severity: SeverityError,
				Message:  fmt.Sprintf("Conditional node %q must have at least one condition", node.ID),
				Path:     prefix + ".config.conditions",
			})
			continue
		}

		for j, condRaw := range conditions {
			cond, ok := condRaw.(map[string]any)
			if !ok {
				continue
			}
			condPath := fmt.Sprintf("%s.config.conditions[%d]", prefix, j)

			name, _ := cond["name"].(string)

			// CN-005: reserved port names
			if reservedPorts[name] {
				diags = append(diags, Diagnostic{
					Code:     "CN-005",
					Severity: SeverityError,
					Message:  fmt.Sprintf("Conditional node %q: condition name %q is reserved", node.ID, name),
					Path:     condPath + ".name",
				})
			}

			// CN-004: expression syntax check
			expression, _ := cond["expression"].(string)
			if expression != "" && registeredExprValidator != nil {
				if err := registeredExprValidator(expression); err != nil {
					diags = append(diags, Diagnostic{
						Code:     "CN-004",
						Severity: SeverityError,
						Message:  fmt.Sprintf("Conditional node %q: condition %q has invalid expression: %v", node.ID, name, err),
						Path:     condPath + ".expression",
					})
				}
			}

			// CN-001: condition name has no downstream edge (warning)
			if name != "" {
				edges := outEdges[node.ID]
				if len(edges) == 0 {
					diags = append(diags, Diagnostic{
						Code:     "CN-001",
						Severity: SeverityWarning,
						Message:  fmt.Sprintf("Conditional node %q: condition %q has no downstream edges", node.ID, name),
						Path:     condPath,
					})
				}
			}
		}

		// CN-003: no default branch warning
		defaultBranch, _ := node.Config["default"].(string)
		if defaultBranch == "" {
			diags = append(diags, Diagnostic{
				Code:     "CN-003",
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("Conditional node %q has no default branch — execution will error if no conditions match", node.ID),
				Path:     prefix + ".config",
			})
		}
	}

	return diags
}

// hasEdgeRefErrors returns true if diagnostics contain GR-001 errors.
func hasEdgeRefErrors(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Code == "GR-001" {
			return true
		}
	}
	return false
}

// detectCycle uses Kahn's algorithm to find cycles. Returns a description
// of the cycle if found, or empty string if the graph is acyclic.
func (gd *GraphDefinition) detectCycle() string {
	// Build adjacency and in-degree from edges
	inDegree := make(map[string]int)
	successors := make(map[string][]string)
	for _, node := range gd.Nodes {
		inDegree[node.ID] = 0
	}
	for _, edge := range gd.Edges {
		successors[edge.Source] = append(successors[edge.Source], edge.Target)
		inDegree[edge.Target]++
	}

	// Collect nodes with zero in-degree
	queue := make([]string, 0)
	for _, node := range gd.Nodes {
		if inDegree[node.ID] == 0 {
			queue = append(queue, node.ID)
		}
	}

	visited := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		visited++
		for _, succ := range successors[current] {
			inDegree[succ]--
			if inDegree[succ] == 0 {
				queue = append(queue, succ)
			}
		}
	}

	if visited < len(gd.Nodes) {
		// Collect nodes still in cycle
		var cycleNodes []string
		for _, node := range gd.Nodes {
			if inDegree[node.ID] > 0 {
				cycleNodes = append(cycleNodes, node.ID)
			}
		}
		return fmt.Sprintf("nodes involved: %v", cycleNodes)
	}
	return ""
}

// BuildOption configures how a GraphDefinition is converted to an executable Graph.
type BuildOption func(*buildConfig)

type buildConfig struct {
	nodeFactory func(NodeDef) (core.Node, error)
}

// WithNodeFactory sets the function used to instantiate live Node objects
// from NodeDef descriptors. Typically provided by the registry or hydrate package.
func WithNodeFactory(factory func(NodeDef) (core.Node, error)) BuildOption {
	return func(c *buildConfig) {
		c.nodeFactory = factory
	}
}

// ToGraph converts a GraphDefinition into an executable Graph by resolving
// node types via the provided node factory and wiring edges.
func (gd *GraphDefinition) ToGraph(opts ...BuildOption) (*BasicGraph, error) {
	cfg := &buildConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.nodeFactory == nil {
		return nil, fmt.Errorf("node factory is required: use WithNodeFactory")
	}

	g := NewGraph(gd.ID)

	// Instantiate nodes
	for _, nd := range gd.Nodes {
		node, err := cfg.nodeFactory(nd)
		if err != nil {
			return nil, fmt.Errorf("creating node %q (type %q): %w", nd.ID, nd.Type, err)
		}
		if err := g.AddNode(node); err != nil {
			return nil, fmt.Errorf("adding node %q: %w", nd.ID, err)
		}
	}

	// Wire edges (EdgeDef carries port handles; BasicGraph edges are node-to-node)
	for _, ed := range gd.Edges {
		if err := g.AddEdge(ed.Source, ed.Target); err != nil {
			return nil, fmt.Errorf("adding edge %s -> %s: %w", ed.Source, ed.Target, err)
		}
	}

	// Resolve entry node
	entry := gd.Entry
	if entry == "" && len(gd.Nodes) > 0 {
		// Default: first node with no inbound edges
		hasInbound := make(map[string]bool)
		for _, ed := range gd.Edges {
			hasInbound[ed.Target] = true
		}
		for _, nd := range gd.Nodes {
			if !hasInbound[nd.ID] {
				entry = nd.ID
				break
			}
		}
		// Fallback: first node
		if entry == "" {
			entry = gd.Nodes[0].ID
		}
	}
	if entry != "" {
		if err := g.SetEntry(entry); err != nil {
			return nil, fmt.Errorf("setting entry node %q: %w", entry, err)
		}
	}

	return g, nil
}
