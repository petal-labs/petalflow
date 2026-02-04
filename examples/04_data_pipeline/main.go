// Example: Data Pipeline
//
// This example shows how to filter and transform data without LLMs.
// It demonstrates:
// - FilterNode for reducing data (top-N, thresholds, deduplication)
// - TransformNode for reshaping data (pick, rename, template)
// - Chaining nodes in a processing pipeline
//
// No external dependencies required.
//
// Run: go run main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/petal-labs/petalflow"
)

func main() {
	// Sample data: product reviews with scores
	reviews := []map[string]any{
		{"id": "1", "product": "Widget A", "score": 4.5, "text": "Great product!", "category": "electronics"},
		{"id": "2", "product": "Widget B", "score": 2.1, "text": "Not worth it.", "category": "electronics"},
		{"id": "3", "product": "Widget A", "score": 4.8, "text": "Excellent!", "category": "electronics"},
		{"id": "4", "product": "Gadget X", "score": 3.5, "text": "It's okay.", "category": "tools"},
		{"id": "5", "product": "Widget C", "score": 4.2, "text": "Good value.", "category": "electronics"},
		{"id": "6", "product": "Gadget X", "score": 4.0, "text": "Works well.", "category": "tools"},
	}

	// Create graph
	g := petalflow.NewGraph("data-pipeline")

	// Step 1: Filter by score threshold
	// Keep only reviews with score >= 3.5
	minScore := 3.5
	filterByScore := petalflow.NewFilterNode("filter_score", petalflow.FilterNodeConfig{
		Target:   petalflow.FilterTargetVar,
		InputVar: "reviews",
		Filters: []petalflow.FilterOp{
			{
				Type:       petalflow.FilterOpThreshold,
				ScoreField: "score",
				Min:        &minScore,
			},
		},
		OutputVar: "high_rated",
		StatsVar:  "filter_stats",
	})

	// Step 2: Keep only top 3 by score
	filterTopN := petalflow.NewFilterNode("filter_top", petalflow.FilterNodeConfig{
		Target:   petalflow.FilterTargetVar,
		InputVar: "high_rated",
		Filters: []petalflow.FilterOp{
			{
				Type:       petalflow.FilterOpTopN,
				N:          3,
				ScoreField: "score",
				Order:      "desc",
			},
		},
		OutputVar: "top_reviews",
	})

	// Step 3: Deduplicate by product (keep highest scored)
	filterDedupe := petalflow.NewFilterNode("filter_dedupe", petalflow.FilterNodeConfig{
		Target:   petalflow.FilterTargetVar,
		InputVar: "top_reviews",
		Filters: []petalflow.FilterOp{
			{
				Type:       petalflow.FilterOpDedupe,
				Field:      "product",
				Keep:       "highest_score",
				ScoreField: "score",
			},
		},
		OutputVar: "unique_products",
	})

	// Step 4: Transform to a summary format
	// Pick only the fields we need and rename them
	transform := petalflow.NewTransformNode("transform", petalflow.TransformNodeConfig{
		Transform: petalflow.TransformTemplate,
		Template: `Top Rated Products:
{{range $i, $r := .unique_products}}
{{$i | printf "%d"}}. {{index $r "product"}} - Score: {{index $r "score"}}
   "{{index $r "text"}}"
{{end}}`,
		OutputVar: "summary",
	})

	// Add nodes to graph
	g.AddNode(filterByScore)
	g.AddNode(filterTopN)
	g.AddNode(filterDedupe)
	g.AddNode(transform)

	// Connect nodes in sequence
	g.AddEdge("filter_score", "filter_top")
	g.AddEdge("filter_top", "filter_dedupe")
	g.AddEdge("filter_dedupe", "transform")

	// Set entry point
	g.SetEntry("filter_score")

	// Prepare input
	env := petalflow.NewEnvelope().WithVar("reviews", toAnySlice(reviews))

	// Run the pipeline
	runtime := petalflow.NewRuntime()
	result, err := runtime.Run(context.Background(), g, env, petalflow.DefaultRunOptions())
	if err != nil {
		fmt.Printf("Pipeline failed: %v\n", err)
		return
	}

	// Show filter statistics
	if stats, ok := result.GetVar("filter_stats"); ok {
		if s, ok := stats.(petalflow.FilterStats); ok {
			fmt.Printf("Score Filter Stats:\n")
			fmt.Printf("  Input: %d, Output: %d, Removed: %d\n\n", s.InputCount, s.OutputCount, s.Removed)
		}
	}

	// Show intermediate results
	if topReviews, ok := result.GetVar("top_reviews"); ok {
		fmt.Println("Top 3 Reviews (after score filter):")
		data, _ := json.MarshalIndent(topReviews, "  ", "  ")
		fmt.Printf("  %s\n\n", data)
	}

	// Show final summary
	summary := result.GetVarString("summary")
	fmt.Println(summary)
}

// toAnySlice converts []map[string]any to []any for FilterNode
func toAnySlice(items []map[string]any) []any {
	result := make([]any, len(items))
	for i, item := range items {
		result[i] = item
	}
	return result
}
