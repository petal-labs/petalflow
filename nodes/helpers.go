// Package nodes provides the node implementations for PetalFlow workflows.
package nodes

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/petal-labs/petalflow/core"
)

// toFloat64 attempts to convert a value to float64.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// toSlice converts various types to []any.
func toSlice(v any) ([]any, error) {
	if v == nil {
		return nil, fmt.Errorf("input is nil")
	}

	switch s := v.(type) {
	case []any:
		return s, nil
	case []string:
		result := make([]any, len(s))
		for i, item := range s {
			result[i] = item
		}
		return result, nil
	case []int:
		result := make([]any, len(s))
		for i, item := range s {
			result[i] = item
		}
		return result, nil
	case []float64:
		result := make([]any, len(s))
		for i, item := range s {
			result[i] = item
		}
		return result, nil
	case []map[string]any:
		result := make([]any, len(s))
		for i, item := range s {
			result[i] = item
		}
		return result, nil
	default:
		return nil, fmt.Errorf("input is not a slice: %T", v)
	}
}

// toMap converts a value to map[string]any if possible.
func toMap(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}

	switch m := v.(type) {
	case map[string]any:
		return m, true
	default:
		return nil, false
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

// getNestedValue retrieves a value from a nested map using dot notation.
func getNestedValue(m map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	current := any(m)

	for _, part := range parts {
		currentMap, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = currentMap[part]
		if !ok {
			return nil, false
		}
	}

	return current, true
}

// setNestedValue sets a value in a nested map using dot notation.
func setNestedValue(m map[string]any, path string, value any) {
	parts := strings.Split(path, ".")

	// Navigate to the parent, creating intermediate maps as needed
	current := m
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		if next, ok := current[part].(map[string]any); ok {
			current = next
		} else {
			next := make(map[string]any)
			current[part] = next
			current = next
		}
	}

	// Set the final value
	current[parts[len(parts)-1]] = value
}

// deleteNestedValue removes a value from a nested map using dot notation.
func deleteNestedValue(m map[string]any, path string) {
	parts := strings.Split(path, ".")

	if len(parts) == 1 {
		delete(m, parts[0])
		return
	}

	// Navigate to the parent
	current := m
	for i := 0; i < len(parts)-1; i++ {
		next, ok := current[parts[i]].(map[string]any)
		if !ok {
			return // Path doesn't exist
		}
		current = next
	}

	// Delete the final key
	delete(current, parts[len(parts)-1])
}

// deepCopyMap creates a deep copy of a map.
func deepCopyMap(m map[string]any) map[string]any {
	result := make(map[string]any, len(m))
	for k, v := range m {
		if nested, ok := v.(map[string]any); ok {
			result[k] = deepCopyMap(nested)
		} else if slice, ok := v.([]any); ok {
			result[k] = deepCopySlice(slice)
		} else {
			result[k] = v
		}
	}
	return result
}

// deepCopySlice creates a deep copy of a slice.
func deepCopySlice(s []any) []any {
	result := make([]any, len(s))
	for i, v := range s {
		if nested, ok := v.(map[string]any); ok {
			result[i] = deepCopyMap(nested)
		} else if slice, ok := v.([]any); ok {
			result[i] = deepCopySlice(slice)
		} else {
			result[i] = v
		}
	}
	return result
}

// toString converts a value to string for prompt building.
func toString(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case fmt.Stringer:
		return v.String()
	default:
		// Try JSON for complex types
		if data, err := json.Marshal(v); err == nil {
			return string(data)
		}
		return fmt.Sprintf("%v", v)
	}
}

// compare compares two values and returns -1, 0, or 1.
func compare(a, b any) int {
	// Handle numeric comparisons
	aNum, aOk := toFloat64(a)
	bNum, bOk := toFloat64(b)
	if aOk && bOk {
		if aNum < bNum {
			return -1
		}
		if aNum > bNum {
			return 1
		}
		return 0
	}

	// String comparison
	aStr := fmt.Sprintf("%v", a)
	bStr := fmt.Sprintf("%v", b)
	if aStr < bStr {
		return -1
	}
	if aStr > bStr {
		return 1
	}
	return 0
}

// containsValue checks if a value contains another value.
func containsValue(container, value any) bool {
	containerStr := fmt.Sprintf("%v", container)
	valueStr := fmt.Sprintf("%v", value)
	return strings.Contains(containerStr, valueStr)
}

// inValues checks if a value is in a list of values.
func inValues(val any, values []any) bool {
	for _, v := range values {
		if compare(val, v) == 0 {
			return true
		}
	}
	return false
}

// artifactsToAny converts []core.Artifact to []any.
func artifactsToAny(artifacts []core.Artifact) []any {
	result := make([]any, len(artifacts))
	for i, a := range artifacts {
		result[i] = a
	}
	return result
}

// anyToArtifacts converts []any back to []core.Artifact.
func anyToArtifacts(items []any) []core.Artifact {
	result := make([]core.Artifact, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case core.Artifact:
			result = append(result, v)
		case *core.Artifact:
			result = append(result, *v)
		}
	}
	return result
}

// messagesToAny converts []core.Message to []any.
func messagesToAny(messages []core.Message) []any {
	result := make([]any, len(messages))
	for i, m := range messages {
		result[i] = m
	}
	return result
}

// anyToMessages converts []any back to []core.Message.
func anyToMessages(items []any) []core.Message {
	result := make([]core.Message, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case core.Message:
			result = append(result, v)
		case *core.Message:
			result = append(result, *v)
		}
	}
	return result
}

// isTruthy determines if a value should be considered true.
func isTruthy(v any) bool {
	if v == nil {
		return false
	}

	switch val := v.(type) {
	case bool:
		return val
	case int:
		return val != 0
	case int64:
		return val != 0
	case float64:
		return val != 0
	case string:
		return val != ""
	case []any:
		return len(val) > 0
	case map[string]any:
		return len(val) > 0
	default:
		// Non-nil, non-zero value is truthy
		return true
	}
}

// flattenMap flattens a nested map into a single-level map.
func flattenMap(m map[string]any, prefix, sep string, maxDepth, currentDepth int, result map[string]any) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + sep + k
		}

		nested, isMap := v.(map[string]any)
		if isMap && (maxDepth == 0 || currentDepth < maxDepth) {
			flattenMap(nested, key, sep, maxDepth, currentDepth+1, result)
		} else {
			result[key] = v
		}
	}
}

// deepMerge recursively merges src into dst.
func deepMerge(dst, src map[string]any) {
	for k, srcVal := range src {
		if dstVal, ok := dst[k]; ok {
			// Both have this key - check if both are maps
			dstMap, dstIsMap := dstVal.(map[string]any)
			srcMap, srcIsMap := srcVal.(map[string]any)
			if dstIsMap && srcIsMap {
				deepMerge(dstMap, srcMap)
				continue
			}
		}
		// Otherwise, src overwrites dst
		dst[k] = srcVal
	}
}
