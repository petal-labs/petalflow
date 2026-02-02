package petalflow

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// FilterOpType specifies the type of filter operation.
type FilterOpType string

const (
	// FilterOpTopN keeps the top N items by a score field.
	FilterOpTopN FilterOpType = "top_n"

	// FilterOpThreshold keeps items above/below a threshold.
	FilterOpThreshold FilterOpType = "threshold"

	// FilterOpDedupe removes duplicates by a field value.
	FilterOpDedupe FilterOpType = "dedupe"

	// FilterOpByType filters by the Type field (for Artifacts).
	FilterOpByType FilterOpType = "type"

	// FilterOpMatch keeps items matching a predicate.
	FilterOpMatch FilterOpType = "match"

	// FilterOpExclude excludes items matching a predicate.
	FilterOpExclude FilterOpType = "exclude"

	// FilterOpCustom uses a custom filter function.
	FilterOpCustom FilterOpType = "custom"
)

// FilterTarget specifies what collection to filter.
type FilterTarget string

const (
	// FilterTargetArtifacts filters the envelope's Artifacts slice.
	FilterTargetArtifacts FilterTarget = "artifacts"

	// FilterTargetMessages filters the envelope's Messages slice.
	FilterTargetMessages FilterTarget = "messages"

	// FilterTargetVar filters a variable (specify the var path).
	FilterTargetVar FilterTarget = "var"
)

// FilterOp defines a single filter operation to apply.
type FilterOp struct {
	// Type specifies the filter operation type.
	Type FilterOpType

	// N is the count for TopN operations.
	N int

	// ScoreField is the field path to use for scoring (TopN, threshold).
	// Supports dot notation for nested fields (e.g., "meta.confidence").
	ScoreField string

	// Order is "asc" or "desc" for TopN (default: "desc").
	Order string

	// Min is the minimum threshold value (inclusive).
	Min *float64

	// Max is the maximum threshold value (inclusive).
	Max *float64

	// Field is the field path for dedupe/match/exclude operations.
	Field string

	// Value is the value to match for match/exclude operations.
	Value any

	// Pattern is a regex pattern for match/exclude operations.
	Pattern string

	// IncludeTypes lists types to include (for FilterOpByType).
	IncludeTypes []string

	// ExcludeTypes lists types to exclude (for FilterOpByType).
	ExcludeTypes []string

	// Keep specifies which duplicate to keep: "first", "last", or "highest_score".
	Keep string

	// CustomFunc is the custom filter function for FilterOpCustom.
	// Returns true to keep the item.
	CustomFunc func(item any, index int, env *Envelope) (keep bool, err error)
}

// FilterStats tracks statistics about the filter operation.
type FilterStats struct {
	InputCount  int `json:"input_count"`
	OutputCount int `json:"output_count"`
	Removed     int `json:"removed"`
}

// FilterNodeConfig configures a FilterNode.
type FilterNodeConfig struct {
	// Target specifies what to filter: "artifacts", "messages", or "var".
	Target FilterTarget

	// InputVar is the variable path when Target is "var".
	// Supports dot notation for nested access.
	InputVar string

	// Filters is a list of filter operations to apply in order.
	Filters []FilterOp

	// OutputVar stores the filtered result.
	// If empty for "var" target, the result replaces the input variable.
	// For "artifacts"/"messages", if empty, modifies envelope in-place.
	OutputVar string

	// StatsVar stores filter statistics (FilterStats).
	StatsVar string
}

// FilterNode prunes items from collections based on configured operations.
// It supports filtering artifacts, messages, or arbitrary variable lists.
type FilterNode struct {
	BaseNode
	config FilterNodeConfig
}

// NewFilterNode creates a new FilterNode with the given configuration.
func NewFilterNode(id string, config FilterNodeConfig) *FilterNode {
	// Set defaults
	if config.Target == "" {
		config.Target = FilterTargetArtifacts
	}

	return &FilterNode{
		BaseNode: NewBaseNode(id, NodeKindFilter),
		config:   config,
	}
}

// Config returns the node's configuration.
func (n *FilterNode) Config() FilterNodeConfig {
	return n.config
}

// Run executes the filter operations on the target collection.
func (n *FilterNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	result := env.Clone()

	var items []any
	var err error

	// Extract items based on target
	switch n.config.Target {
	case FilterTargetArtifacts:
		items = artifactsToAny(result.Artifacts)
	case FilterTargetMessages:
		items = messagesToAny(result.Messages)
	case FilterTargetVar:
		if n.config.InputVar == "" {
			return nil, fmt.Errorf("filter node %s: InputVar required for var target", n.id)
		}
		val, ok := result.GetVarNested(n.config.InputVar)
		if !ok {
			return nil, fmt.Errorf("filter node %s: variable %q not found", n.id, n.config.InputVar)
		}
		items, err = toSlice(val)
		if err != nil {
			return nil, fmt.Errorf("filter node %s: %w", n.id, err)
		}
	default:
		return nil, fmt.Errorf("filter node %s: unknown target %q", n.id, n.config.Target)
	}

	inputCount := len(items)

	// Apply each filter operation in order
	for i, op := range n.config.Filters {
		items, err = n.applyFilter(ctx, result, items, op)
		if err != nil {
			return nil, fmt.Errorf("filter node %s: filter %d (%s): %w", n.id, i, op.Type, err)
		}
	}

	// Store results
	switch n.config.Target {
	case FilterTargetArtifacts:
		artifacts := anyToArtifacts(items)
		if n.config.OutputVar != "" {
			result.SetVar(n.config.OutputVar, artifacts)
		} else {
			result.Artifacts = artifacts
		}
	case FilterTargetMessages:
		messages := anyToMessages(items)
		if n.config.OutputVar != "" {
			result.SetVar(n.config.OutputVar, messages)
		} else {
			result.Messages = messages
		}
	case FilterTargetVar:
		outputVar := n.config.OutputVar
		if outputVar == "" {
			outputVar = n.config.InputVar
		}
		result.SetVar(outputVar, items)
	}

	// Store stats if configured
	if n.config.StatsVar != "" {
		result.SetVar(n.config.StatsVar, FilterStats{
			InputCount:  inputCount,
			OutputCount: len(items),
			Removed:     inputCount - len(items),
		})
	}

	return result, nil
}

// applyFilter applies a single filter operation to the items.
func (n *FilterNode) applyFilter(ctx context.Context, env *Envelope, items []any, op FilterOp) ([]any, error) {
	switch op.Type {
	case FilterOpTopN:
		return n.filterTopN(items, op)
	case FilterOpThreshold:
		return n.filterThreshold(items, op)
	case FilterOpDedupe:
		return n.filterDedupe(items, op)
	case FilterOpByType:
		return n.filterByType(items, op)
	case FilterOpMatch:
		return n.filterMatch(items, op, true)
	case FilterOpExclude:
		return n.filterMatch(items, op, false)
	case FilterOpCustom:
		return n.filterCustom(ctx, env, items, op)
	default:
		return nil, fmt.Errorf("unknown filter type: %s", op.Type)
	}
}

// filterTopN keeps the top N items by score.
func (n *FilterNode) filterTopN(items []any, op FilterOp) ([]any, error) {
	if op.N <= 0 {
		return items, nil
	}
	if len(items) <= op.N {
		return items, nil
	}

	// Extract scores
	type scored struct {
		index int
		item  any
		score float64
	}

	scoredItems := make([]scored, 0, len(items))
	for i, item := range items {
		score, err := extractFloat(item, op.ScoreField)
		if err != nil {
			// Items without scores get zero
			score = 0
		}
		scoredItems = append(scoredItems, scored{index: i, item: item, score: score})
	}

	// Sort by score
	desc := op.Order != "asc"
	sort.Slice(scoredItems, func(i, j int) bool {
		if desc {
			return scoredItems[i].score > scoredItems[j].score
		}
		return scoredItems[i].score < scoredItems[j].score
	})

	// Take top N
	result := make([]any, op.N)
	for i := 0; i < op.N; i++ {
		result[i] = scoredItems[i].item
	}

	return result, nil
}

// filterThreshold keeps items within the threshold bounds.
func (n *FilterNode) filterThreshold(items []any, op FilterOp) ([]any, error) {
	if op.Min == nil && op.Max == nil {
		return items, nil
	}

	result := make([]any, 0, len(items))
	for _, item := range items {
		score, err := extractFloat(item, op.ScoreField)
		if err != nil {
			// Items without the score field are excluded
			continue
		}

		if op.Min != nil && score < *op.Min {
			continue
		}
		if op.Max != nil && score > *op.Max {
			continue
		}

		result = append(result, item)
	}

	return result, nil
}

// filterDedupe removes duplicates by field value.
func (n *FilterNode) filterDedupe(items []any, op FilterOp) ([]any, error) {
	if op.Field == "" {
		return nil, fmt.Errorf("dedupe requires Field")
	}

	keep := op.Keep
	if keep == "" {
		keep = "first"
	}

	// Track seen values and their items
	type entry struct {
		index int
		item  any
		score float64
	}
	seen := make(map[string]entry)

	for i, item := range items {
		val, _ := extractValue(item, op.Field)
		key := fmt.Sprintf("%v", val)

		existing, exists := seen[key]
		if !exists {
			score := 0.0
			if keep == "highest_score" && op.ScoreField != "" {
				score, _ = extractFloat(item, op.ScoreField)
			}
			seen[key] = entry{index: i, item: item, score: score}
			continue
		}

		switch keep {
		case "first":
			// Keep existing, ignore new
		case "last":
			seen[key] = entry{index: i, item: item, score: existing.score}
		case "highest_score":
			score, _ := extractFloat(item, op.ScoreField)
			if score > existing.score {
				seen[key] = entry{index: i, item: item, score: score}
			}
		}
	}

	// Collect results in original order
	result := make([]any, 0, len(seen))
	indices := make([]int, 0, len(seen))
	for _, e := range seen {
		indices = append(indices, e.index)
	}
	sort.Ints(indices)

	indexToEntry := make(map[int]entry)
	for _, e := range seen {
		indexToEntry[e.index] = e
	}
	for _, idx := range indices {
		result = append(result, indexToEntry[idx].item)
	}

	return result, nil
}

// filterByType filters artifacts/items by their Type field.
func (n *FilterNode) filterByType(items []any, op FilterOp) ([]any, error) {
	if len(op.IncludeTypes) == 0 && len(op.ExcludeTypes) == 0 {
		return items, nil
	}

	includeSet := make(map[string]bool)
	for _, t := range op.IncludeTypes {
		includeSet[t] = true
	}

	excludeSet := make(map[string]bool)
	for _, t := range op.ExcludeTypes {
		excludeSet[t] = true
	}

	result := make([]any, 0, len(items))
	for _, item := range items {
		itemType := ""

		// Try to get Type field
		switch v := item.(type) {
		case Artifact:
			itemType = v.Type
		case *Artifact:
			itemType = v.Type
		case map[string]any:
			if t, ok := v["Type"].(string); ok {
				itemType = t
			} else if t, ok := v["type"].(string); ok {
				itemType = t
			}
		}

		// Apply include filter
		if len(includeSet) > 0 && !includeSet[itemType] {
			continue
		}

		// Apply exclude filter
		if excludeSet[itemType] {
			continue
		}

		result = append(result, item)
	}

	return result, nil
}

// filterMatch keeps or excludes items matching a predicate.
func (n *FilterNode) filterMatch(items []any, op FilterOp, keepMatches bool) ([]any, error) {
	var re *regexp.Regexp
	var err error

	if op.Pattern != "" {
		re, err = regexp.Compile(op.Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", op.Pattern, err)
		}
	}

	result := make([]any, 0, len(items))
	for _, item := range items {
		matches := false

		if op.Field != "" {
			val, found := extractValue(item, op.Field)
			if found {
				if re != nil {
					// Pattern match
					strVal := fmt.Sprintf("%v", val)
					matches = re.MatchString(strVal)
				} else if op.Value != nil {
					// Value match
					matches = valuesEqual(val, op.Value)
				} else {
					// Field exists
					matches = true
				}
			}
		}

		if (keepMatches && matches) || (!keepMatches && !matches) {
			result = append(result, item)
		}
	}

	return result, nil
}

// filterCustom applies a custom filter function.
func (n *FilterNode) filterCustom(ctx context.Context, env *Envelope, items []any, op FilterOp) ([]any, error) {
	if op.CustomFunc == nil {
		return nil, fmt.Errorf("custom filter requires CustomFunc")
	}

	result := make([]any, 0, len(items))
	for i, item := range items {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		keep, err := op.CustomFunc(item, i, env)
		if err != nil {
			return nil, fmt.Errorf("custom filter error at index %d: %w", i, err)
		}
		if keep {
			result = append(result, item)
		}
	}

	return result, nil
}

// extractFloat extracts a float64 value from an item at the given field path.
func extractFloat(item any, fieldPath string) (float64, error) {
	if fieldPath == "" {
		return 0, fmt.Errorf("no field path specified")
	}

	val, found := extractValue(item, fieldPath)
	if !found {
		return 0, fmt.Errorf("field %q not found", fieldPath)
	}

	switch v := val.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case int32:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("field %q is not numeric: %T", fieldPath, val)
	}
}

// extractValue extracts a value from an item at the given field path.
// Supports dot notation for nested access (e.g., "meta.confidence").
func extractValue(item any, fieldPath string) (any, bool) {
	if fieldPath == "" {
		return nil, false
	}

	parts := strings.Split(fieldPath, ".")
	current := item

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			val, ok := v[part]
			if !ok {
				return nil, false
			}
			current = val
		case Artifact:
			current = getArtifactField(v, part)
			if current == nil {
				return nil, false
			}
		case *Artifact:
			current = getArtifactField(*v, part)
			if current == nil {
				return nil, false
			}
		case Message:
			current = getMessageField(v, part)
			if current == nil {
				return nil, false
			}
		case *Message:
			current = getMessageField(*v, part)
			if current == nil {
				return nil, false
			}
		default:
			return nil, false
		}
	}

	return current, true
}

// getArtifactField gets a field value from an Artifact.
func getArtifactField(a Artifact, field string) any {
	switch field {
	case "ID", "id":
		return a.ID
	case "Type", "type":
		return a.Type
	case "MimeType", "mimeType", "mime_type":
		return a.MimeType
	case "Text", "text":
		return a.Text
	case "URI", "uri":
		return a.URI
	case "Meta", "meta":
		return a.Meta
	default:
		// Try meta field
		if a.Meta != nil {
			if val, ok := a.Meta[field]; ok {
				return val
			}
		}
		return nil
	}
}

// getMessageField gets a field value from a Message.
func getMessageField(m Message, field string) any {
	switch field {
	case "Role", "role":
		return m.Role
	case "Content", "content":
		return m.Content
	case "Name", "name":
		return m.Name
	case "Meta", "meta":
		return m.Meta
	default:
		// Try meta field
		if m.Meta != nil {
			if val, ok := m.Meta[field]; ok {
				return val
			}
		}
		return nil
	}
}

// valuesEqual compares two values for equality.
func valuesEqual(a, b any) bool {
	// Handle string comparison
	if aStr, ok := a.(string); ok {
		if bStr, ok := b.(string); ok {
			return aStr == bStr
		}
	}

	// Handle numeric comparison
	aFloat, aOk := toFloat64(a)
	bFloat, bOk := toFloat64(b)
	if aOk && bOk {
		return aFloat == bFloat
	}

	// Fall back to fmt comparison
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// Note: toFloat64 is defined in router_node.go and reused here

// artifactsToAny converts []Artifact to []any.
func artifactsToAny(artifacts []Artifact) []any {
	result := make([]any, len(artifacts))
	for i, a := range artifacts {
		result[i] = a
	}
	return result
}

// anyToArtifacts converts []any back to []Artifact.
func anyToArtifacts(items []any) []Artifact {
	result := make([]Artifact, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case Artifact:
			result = append(result, v)
		case *Artifact:
			result = append(result, *v)
		}
	}
	return result
}

// messagesToAny converts []Message to []any.
func messagesToAny(messages []Message) []any {
	result := make([]any, len(messages))
	for i, m := range messages {
		result[i] = m
	}
	return result
}

// anyToMessages converts []any back to []Message.
func anyToMessages(items []any) []Message {
	result := make([]Message, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case Message:
			result = append(result, v)
		case *Message:
			result = append(result, *v)
		}
	}
	return result
}

// Ensure interface compliance at compile time.
var _ Node = (*FilterNode)(nil)
