package petalflow

import (
	"context"
	"fmt"
	"sync"
)

// MapNodeConfig configures a MapNode.
type MapNodeConfig struct {
	// InputVar is the variable name containing the collection to map over.
	// The collection should be a slice ([]any or []T).
	InputVar string

	// OutputVar is the variable name where mapped results will be stored.
	// Defaults to "{node_id}_output".
	OutputVar string

	// ItemVar is the variable name used to pass each item to the mapper.
	// Defaults to "item".
	ItemVar string

	// IndexVar is the optional variable name for the item's index.
	// If empty, index is not provided.
	IndexVar string

	// Concurrency controls how many items are processed in parallel.
	// Default is 1 (sequential). Set > 1 for parallel processing.
	Concurrency int

	// Mapper is a function that transforms each item.
	// If nil, MapperNode must be set.
	Mapper func(ctx context.Context, item any, index int) (any, error)

	// MapperNode is a node to execute for each item.
	// The item is passed via ItemVar in the envelope.
	// If nil, Mapper function must be set.
	MapperNode Node

	// ContinueOnError records errors and continues processing remaining items.
	ContinueOnError bool

	// PreserveOrder ensures output order matches input order even with concurrent execution.
	// Default is true.
	PreserveOrder bool
}

// MapNode applies a transformation to each item in a collection.
// It supports both function-based and node-based mapping, with optional concurrency.
type MapNode struct {
	BaseNode
	config MapNodeConfig
}

// NewMapNode creates a new MapNode with the given configuration.
func NewMapNode(id string, config MapNodeConfig) *MapNode {
	// Set defaults
	if config.OutputVar == "" {
		config.OutputVar = id + "_output"
	}
	if config.ItemVar == "" {
		config.ItemVar = "item"
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 1
	}
	// PreserveOrder defaults to true (zero value is false, so we check explicitly)
	// Note: We can't distinguish "not set" from "set to false" with bool
	// So we default to true in the implementation

	return &MapNode{
		BaseNode: NewBaseNode(id, NodeKindMap),
		config:   config,
	}
}

// Config returns the node's configuration.
func (n *MapNode) Config() MapNodeConfig {
	return n.config
}

// Run executes the map operation over the input collection.
func (n *MapNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	// Get the input collection
	inputVal, ok := env.GetVar(n.config.InputVar)
	if !ok {
		return nil, fmt.Errorf("map node %s: input variable %q not found", n.id, n.config.InputVar)
	}

	// Convert to slice
	items, err := toSlice(inputVal)
	if err != nil {
		return nil, fmt.Errorf("map node %s: %w", n.id, err)
	}

	// Check we have a mapper
	if n.config.Mapper == nil && n.config.MapperNode == nil {
		return nil, fmt.Errorf("map node %s: no mapper configured", n.id)
	}

	// Execute map operation
	var results []any
	var mapErr error

	if n.config.Concurrency == 1 {
		results, mapErr = n.mapSequential(ctx, env, items)
	} else {
		results, mapErr = n.mapConcurrent(ctx, env, items)
	}

	if mapErr != nil && !n.config.ContinueOnError {
		return nil, mapErr
	}

	// Store results
	result := env.Clone()
	result.SetVar(n.config.OutputVar, results)

	return result, nil
}

// mapSequential processes items one at a time.
func (n *MapNode) mapSequential(ctx context.Context, env *Envelope, items []any) ([]any, error) {
	results := make([]any, len(items))

	for i, item := range items {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		result, err := n.processItem(ctx, env, item, i)
		if err != nil {
			if n.config.ContinueOnError {
				results[i] = nil
				continue
			}
			return nil, fmt.Errorf("map item %d: %w", i, err)
		}
		results[i] = result
	}

	return results, nil
}

// mapConcurrent processes items with a worker pool.
func (n *MapNode) mapConcurrent(ctx context.Context, env *Envelope, items []any) ([]any, error) {
	results := make([]any, len(items))
	var resultsMu sync.Mutex
	var firstErr error
	var errOnce sync.Once

	// Work items
	type workItem struct {
		index int
		item  any
	}

	workCh := make(chan workItem, n.config.Concurrency)

	// Context with cancellation
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < n.config.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-workerCtx.Done():
					return
				case work, ok := <-workCh:
					if !ok {
						return
					}

					result, err := n.processItem(workerCtx, env, work.item, work.index)
					if err != nil {
						if n.config.ContinueOnError {
							resultsMu.Lock()
							results[work.index] = nil
							resultsMu.Unlock()
						} else {
							errOnce.Do(func() {
								firstErr = fmt.Errorf("map item %d: %w", work.index, err)
								cancel()
							})
						}
						continue
					}

					resultsMu.Lock()
					results[work.index] = result
					resultsMu.Unlock()
				}
			}
		}()
	}

	// Submit work
submitLoop:
	for i, item := range items {
		select {
		case <-workerCtx.Done():
			break submitLoop
		case workCh <- workItem{index: i, item: item}:
		}
	}
	close(workCh)

	// Wait for workers
	wg.Wait()

	return results, firstErr
}

// processItem applies the mapper to a single item.
func (n *MapNode) processItem(ctx context.Context, env *Envelope, item any, index int) (any, error) {
	if n.config.Mapper != nil {
		// Use function mapper
		return n.config.Mapper(ctx, item, index)
	}

	// Use node mapper
	// Create a cloned envelope with the item
	itemEnv := env.Clone()
	itemEnv.SetVar(n.config.ItemVar, item)
	if n.config.IndexVar != "" {
		itemEnv.SetVar(n.config.IndexVar, index)
	}

	// Update trace for child execution
	itemEnv.Trace.ParentID = itemEnv.Trace.RunID
	itemEnv.Trace.SpanID = fmt.Sprintf("%s-item-%d", n.id, index)

	// Execute the mapper node
	resultEnv, err := n.config.MapperNode.Run(ctx, itemEnv)
	if err != nil {
		return nil, err
	}

	// Extract result from the mapper node's output
	// Convention: mapper node stores result in its output key
	// We return the modified vars as the result
	return resultEnv.Vars, nil
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

// Ensure interface compliance at compile time.
var _ Node = (*MapNode)(nil)
