package petalflow

import (
	"context"
	"fmt"
	"strings"
)

// MergeStrategy defines how multiple envelopes from parallel branches are combined.
type MergeStrategy interface {
	// Name returns the strategy identifier.
	Name() string

	// Merge combines multiple envelopes into a single envelope.
	// The order of inputs matches the order of branches as defined in the graph.
	Merge(ctx context.Context, inputs []*Envelope) (*Envelope, error)
}

// MergeNodeConfig configures a MergeNode.
type MergeNodeConfig struct {
	// Strategy determines how inputs are merged.
	// If nil, defaults to JSONMergeStrategy.
	Strategy MergeStrategy

	// OutputKey is the variable name for the merged result.
	// Defaults to "{node_id}_output".
	OutputKey string

	// ExpectedInputs is the number of inputs to wait for before merging.
	// If 0, the runtime will use the number of incoming edges.
	ExpectedInputs int
}

// MergeNode combines results from multiple parallel branches.
// It implements the Node interface and is recognized specially by the runtime.
type MergeNode struct {
	BaseNode
	config MergeNodeConfig
}

// NewMergeNode creates a new MergeNode with the given configuration.
func NewMergeNode(id string, config MergeNodeConfig) *MergeNode {
	// Set defaults
	if config.OutputKey == "" {
		config.OutputKey = id + "_output"
	}
	if config.Strategy == nil {
		config.Strategy = NewJSONMergeStrategy(JSONMergeConfig{})
	}

	return &MergeNode{
		BaseNode: NewBaseNode(id, NodeKindMerge),
		config:   config,
	}
}

// Config returns the node's configuration.
func (n *MergeNode) Config() MergeNodeConfig {
	return n.config
}

// Run executes the merge for a single envelope.
// For actual multi-input merging, the runtime calls MergeInputs directly.
// This single-envelope Run is provided for interface compliance and passthrough scenarios.
func (n *MergeNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	// When called with a single envelope, just pass it through
	// The real merge happens in MergeInputs called by the runtime
	return env, nil
}

// MergeInputs combines multiple envelopes using the configured strategy.
// This is called by the runtime when all parallel branches have completed.
func (n *MergeNode) MergeInputs(ctx context.Context, inputs []*Envelope) (*Envelope, error) {
	if len(inputs) == 0 {
		return NewEnvelope(), nil
	}

	if len(inputs) == 1 {
		return inputs[0], nil
	}

	merged, err := n.config.Strategy.Merge(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("merge strategy %q failed: %w", n.config.Strategy.Name(), err)
	}

	return merged, nil
}

// ExpectedInputs returns the number of inputs this merge node expects.
func (n *MergeNode) ExpectedInputs() int {
	return n.config.ExpectedInputs
}

// IsMergeNode is a marker interface method to help identify merge nodes.
func (n *MergeNode) IsMergeNode() bool {
	return true
}

// --- Built-in Merge Strategies ---

// JSONMergeConfig configures the JSONMergeStrategy.
type JSONMergeConfig struct {
	// DeepMerge enables recursive merging of nested maps.
	// When false (default), later values overwrite earlier ones.
	DeepMerge bool

	// ConflictPolicy determines how to handle key conflicts.
	// Options: "last" (default), "first", "error"
	ConflictPolicy string

	// VarsOnly when true only merges Vars, ignoring other envelope fields.
	VarsOnly bool
}

// JSONMergeStrategy merges envelope Vars by combining their maps.
type JSONMergeStrategy struct {
	config JSONMergeConfig
}

// NewJSONMergeStrategy creates a new JSON merge strategy.
func NewJSONMergeStrategy(config JSONMergeConfig) *JSONMergeStrategy {
	if config.ConflictPolicy == "" {
		config.ConflictPolicy = "last"
	}
	return &JSONMergeStrategy{config: config}
}

// Name returns "json_merge".
func (s *JSONMergeStrategy) Name() string {
	return "json_merge"
}

// Merge combines envelopes by merging their Vars maps.
func (s *JSONMergeStrategy) Merge(ctx context.Context, inputs []*Envelope) (*Envelope, error) {
	if len(inputs) == 0 {
		return NewEnvelope(), nil
	}

	// Start with a clone of the first envelope to preserve trace and other fields
	result := inputs[0].Clone()

	// Merge vars from all subsequent envelopes
	for i := 1; i < len(inputs); i++ {
		input := inputs[i]
		if input == nil || input.Vars == nil {
			continue
		}

		if err := s.mergeVars(result.Vars, input.Vars); err != nil {
			return nil, err
		}

		// Merge other envelope fields unless VarsOnly is set
		if !s.config.VarsOnly {
			result.Artifacts = append(result.Artifacts, input.Artifacts...)
			result.Messages = append(result.Messages, input.Messages...)
			result.Errors = append(result.Errors, input.Errors...)
		}
	}

	return result, nil
}

// mergeVars merges source vars into dest vars.
func (s *JSONMergeStrategy) mergeVars(dest, src map[string]any) error {
	for k, v := range src {
		existing, exists := dest[k]
		if !exists {
			dest[k] = v
			continue
		}

		// Handle conflict based on policy
		switch s.config.ConflictPolicy {
		case "error":
			return fmt.Errorf("key conflict: %q exists in multiple branches", k)
		case "first":
			// Keep existing, don't overwrite
			continue
		case "last":
			// Check if we should deep merge nested maps
			if s.config.DeepMerge {
				destMap, destIsMap := existing.(map[string]any)
				srcMap, srcIsMap := v.(map[string]any)
				if destIsMap && srcIsMap {
					if err := s.mergeVars(destMap, srcMap); err != nil {
						return err
					}
					continue
				}
			}
			// Overwrite with new value
			dest[k] = v
		default:
			// Default to "last" behavior
			dest[k] = v
		}
	}
	return nil
}

// ConcatMergeConfig configures the ConcatMergeStrategy.
type ConcatMergeConfig struct {
	// VarName is the variable to concatenate from each input.
	VarName string

	// OutputKey is where to store the concatenated result.
	// If empty, uses VarName.
	OutputKey string

	// Separator is placed between concatenated values.
	// Defaults to newline.
	Separator string
}

// ConcatMergeStrategy concatenates string values from a specific variable.
type ConcatMergeStrategy struct {
	config ConcatMergeConfig
}

// NewConcatMergeStrategy creates a new concatenation merge strategy.
func NewConcatMergeStrategy(config ConcatMergeConfig) *ConcatMergeStrategy {
	if config.Separator == "" {
		config.Separator = "\n"
	}
	if config.OutputKey == "" {
		config.OutputKey = config.VarName
	}
	return &ConcatMergeStrategy{config: config}
}

// Name returns "concat_merge".
func (s *ConcatMergeStrategy) Name() string {
	return "concat_merge"
}

// Merge concatenates the configured variable from all inputs.
func (s *ConcatMergeStrategy) Merge(ctx context.Context, inputs []*Envelope) (*Envelope, error) {
	if len(inputs) == 0 {
		return NewEnvelope(), nil
	}

	// Collect all string values
	var values []string
	for _, input := range inputs {
		if input == nil {
			continue
		}
		v, ok := input.GetVar(s.config.VarName)
		if !ok {
			continue
		}
		str, ok := v.(string)
		if !ok {
			// Try to convert to string
			str = fmt.Sprintf("%v", v)
		}
		values = append(values, str)
	}

	// Start with a clone of the first envelope
	result := inputs[0].Clone()

	// Store concatenated result
	result.SetVar(s.config.OutputKey, strings.Join(values, s.config.Separator))

	// Merge other envelope fields from all inputs
	for i := 1; i < len(inputs); i++ {
		input := inputs[i]
		if input == nil {
			continue
		}
		result.Artifacts = append(result.Artifacts, input.Artifacts...)
		result.Messages = append(result.Messages, input.Messages...)
		result.Errors = append(result.Errors, input.Errors...)
	}

	return result, nil
}

// BestScoreMergeConfig configures the BestScoreMergeStrategy.
type BestScoreMergeConfig struct {
	// ScoreVar is the variable name containing the numeric score.
	ScoreVar string

	// HigherIsBetter when true selects the highest score, otherwise lowest.
	HigherIsBetter bool
}

// BestScoreMergeStrategy selects the envelope with the best score.
type BestScoreMergeStrategy struct {
	config BestScoreMergeConfig
}

// NewBestScoreMergeStrategy creates a new best-score merge strategy.
func NewBestScoreMergeStrategy(config BestScoreMergeConfig) *BestScoreMergeStrategy {
	return &BestScoreMergeStrategy{config: config}
}

// Name returns "best_score_merge".
func (s *BestScoreMergeStrategy) Name() string {
	return "best_score_merge"
}

// Merge selects the envelope with the best score.
func (s *BestScoreMergeStrategy) Merge(ctx context.Context, inputs []*Envelope) (*Envelope, error) {
	if len(inputs) == 0 {
		return NewEnvelope(), nil
	}

	var bestEnv *Envelope
	var bestScore float64
	first := true

	for _, input := range inputs {
		if input == nil {
			continue
		}

		v, ok := input.GetVar(s.config.ScoreVar)
		if !ok {
			continue
		}

		score, ok := toFloat64(v)
		if !ok {
			continue
		}

		if first {
			bestEnv = input
			bestScore = score
			first = false
			continue
		}

		if s.config.HigherIsBetter {
			if score > bestScore {
				bestEnv = input
				bestScore = score
			}
		} else {
			if score < bestScore {
				bestEnv = input
				bestScore = score
			}
		}
	}

	if bestEnv == nil {
		// No valid scores found, return first non-nil envelope
		for _, input := range inputs {
			if input != nil {
				return input.Clone(), nil
			}
		}
		return NewEnvelope(), nil
	}

	return bestEnv.Clone(), nil
}

// toFloat64 is defined in router_node.go

// FuncMergeStrategy wraps a custom function as a MergeStrategy.
type FuncMergeStrategy struct {
	name string
	fn   func(ctx context.Context, inputs []*Envelope) (*Envelope, error)
}

// NewFuncMergeStrategy creates a custom merge strategy from a function.
func NewFuncMergeStrategy(name string, fn func(ctx context.Context, inputs []*Envelope) (*Envelope, error)) *FuncMergeStrategy {
	return &FuncMergeStrategy{
		name: name,
		fn:   fn,
	}
}

// Name returns the strategy name.
func (s *FuncMergeStrategy) Name() string {
	return s.name
}

// Merge executes the wrapped function.
func (s *FuncMergeStrategy) Merge(ctx context.Context, inputs []*Envelope) (*Envelope, error) {
	if s.fn == nil {
		if len(inputs) > 0 {
			return inputs[0].Clone(), nil
		}
		return NewEnvelope(), nil
	}
	return s.fn(ctx, inputs)
}

// AllMergeStrategy runs all inputs through another strategy and also collects
// all envelopes into a slice variable for inspection.
type AllMergeStrategy struct {
	inner       MergeStrategy
	collectKey  string
	collectVars []string
}

// NewAllMergeStrategy creates a strategy that collects all inputs while applying another strategy.
func NewAllMergeStrategy(inner MergeStrategy, collectKey string, collectVars []string) *AllMergeStrategy {
	if inner == nil {
		inner = NewJSONMergeStrategy(JSONMergeConfig{})
	}
	if collectKey == "" {
		collectKey = "all_inputs"
	}
	return &AllMergeStrategy{
		inner:       inner,
		collectKey:  collectKey,
		collectVars: collectVars,
	}
}

// Name returns "all_merge".
func (s *AllMergeStrategy) Name() string {
	return "all_merge"
}

// Merge applies the inner strategy and also collects specified vars from all inputs.
func (s *AllMergeStrategy) Merge(ctx context.Context, inputs []*Envelope) (*Envelope, error) {
	result, err := s.inner.Merge(ctx, inputs)
	if err != nil {
		return nil, err
	}

	// Collect specified vars from all inputs
	var collected []map[string]any
	for _, input := range inputs {
		if input == nil {
			continue
		}
		item := make(map[string]any)
		if len(s.collectVars) == 0 {
			// Collect all vars
			for k, v := range input.Vars {
				item[k] = v
			}
		} else {
			// Collect only specified vars
			for _, varName := range s.collectVars {
				if v, ok := input.GetVar(varName); ok {
					item[varName] = v
				}
			}
		}
		collected = append(collected, item)
	}

	result.SetVar(s.collectKey, collected)
	return result, nil
}

// Ensure interface compliance at compile time.
var (
	_ Node          = (*MergeNode)(nil)
	_ MergeStrategy = (*JSONMergeStrategy)(nil)
	_ MergeStrategy = (*ConcatMergeStrategy)(nil)
	_ MergeStrategy = (*BestScoreMergeStrategy)(nil)
	_ MergeStrategy = (*FuncMergeStrategy)(nil)
	_ MergeStrategy = (*AllMergeStrategy)(nil)
)
