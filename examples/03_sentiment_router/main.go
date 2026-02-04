// Example: Sentiment Router
//
// This example shows how to route messages based on their content.
// It demonstrates:
// - RuleRouter for conditional branching
// - Multiple edges from a router to different handlers
// - Reading route decisions from the envelope
//
// No LLM required - uses simple keyword matching.
//
// Run: go run main.go
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/petal-labs/petalflow"
)

func main() {
	// Create graph
	g := petalflow.NewGraph("sentiment-router")

	// Step 1: Create a preprocessor node
	// This extracts keywords to help with routing.
	preprocess := petalflow.NewFuncNode("preprocess", func(ctx context.Context, env *petalflow.Envelope) (*petalflow.Envelope, error) {
		message := env.GetVarString("message")
		lower := strings.ToLower(message)

		// Set flags for the router to use
		env.SetVar("has_positive", strings.Contains(lower, "love") ||
			strings.Contains(lower, "great") ||
			strings.Contains(lower, "excellent") ||
			strings.Contains(lower, "happy"))

		env.SetVar("has_negative", strings.Contains(lower, "hate") ||
			strings.Contains(lower, "terrible") ||
			strings.Contains(lower, "awful") ||
			strings.Contains(lower, "angry"))

		return env, nil
	})

	// Step 2: Create a RuleRouter
	// Routes to different handlers based on conditions.
	router := petalflow.NewRuleRouter("route", petalflow.RuleRouterConfig{
		Rules: []petalflow.RouteRule{
			{
				Conditions: []petalflow.RouteCondition{
					{VarPath: "has_positive", Op: petalflow.OpEquals, Value: true},
				},
				Target: "positive_handler",
				Reason: "Message contains positive keywords",
			},
			{
				Conditions: []petalflow.RouteCondition{
					{VarPath: "has_negative", Op: petalflow.OpEquals, Value: true},
				},
				Target: "negative_handler",
				Reason: "Message contains negative keywords",
			},
		},
		// Where to go if no rules match
		DefaultTarget: "neutral_handler",
	})

	// Step 3: Create handler nodes for each sentiment
	positiveHandler := petalflow.NewFuncNode("positive_handler", func(ctx context.Context, env *petalflow.Envelope) (*petalflow.Envelope, error) {
		env.SetVar("response", "Thanks for the positive feedback!")
		env.SetVar("sentiment", "positive")
		return env, nil
	})

	negativeHandler := petalflow.NewFuncNode("negative_handler", func(ctx context.Context, env *petalflow.Envelope) (*petalflow.Envelope, error) {
		env.SetVar("response", "We're sorry to hear that. How can we help?")
		env.SetVar("sentiment", "negative")
		return env, nil
	})

	neutralHandler := petalflow.NewFuncNode("neutral_handler", func(ctx context.Context, env *petalflow.Envelope) (*petalflow.Envelope, error) {
		env.SetVar("response", "Thanks for reaching out!")
		env.SetVar("sentiment", "neutral")
		return env, nil
	})

	// Step 4: Add all nodes to the graph
	g.AddNode(preprocess)
	g.AddNode(router)
	g.AddNode(positiveHandler)
	g.AddNode(negativeHandler)
	g.AddNode(neutralHandler)

	// Step 5: Connect the nodes with edges
	// preprocess -> router -> handlers
	g.AddEdge("preprocess", "route")
	g.AddEdge("route", "positive_handler")
	g.AddEdge("route", "negative_handler")
	g.AddEdge("route", "neutral_handler")

	// Step 6: Set entry point
	g.SetEntry("preprocess")

	// Step 7: Test with different messages
	testMessages := []string{
		"I love this product! It's excellent!",
		"This is terrible. I hate it.",
		"Can you tell me about your return policy?",
	}

	runtime := petalflow.NewRuntime()

	for _, msg := range testMessages {
		env := petalflow.NewEnvelope().WithVar("message", msg)

		result, err := runtime.Run(context.Background(), g, env, petalflow.DefaultRunOptions())
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		sentiment := result.GetVarString("sentiment")
		response := result.GetVarString("response")

		fmt.Printf("Input:     %q\n", msg)
		fmt.Printf("Sentiment: %s\n", sentiment)
		fmt.Printf("Response:  %s\n\n", response)
	}
}
