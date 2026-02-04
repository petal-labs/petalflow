// Example: Iris Integration
//
// This example shows how to connect PetalFlow with Iris LLM providers.
// It demonstrates:
// - Creating an Iris provider (Ollama for local testing)
// - Wrapping it with irisadapter.ProviderAdapter
// - Using LLMNode for AI-powered workflow steps
// - Configuring prompts with templates
//
// Prerequisites:
// - Ollama running locally (https://ollama.ai)
// - A model pulled: ollama pull llama3.2
//
// Run: go run main.go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/petal-labs/iris/providers/ollama"
	"github.com/petal-labs/petalflow"
	"github.com/petal-labs/petalflow/irisadapter"
)

func main() {
	// Step 1: Create an Iris provider
	// Ollama runs locally and doesn't require API keys.
	// For production, you might use openai.New() or anthropic.New().
	provider := ollama.New(
		ollama.WithBaseURL("http://localhost:11434"),
	)

	// Step 2: Wrap the provider for PetalFlow
	// The adapter converts Iris's Provider interface to PetalFlow's LLMClient.
	client := irisadapter.NewProviderAdapter(provider)

	// Step 3: Create the graph
	g := petalflow.NewGraph("iris-demo")

	// Step 4: Create an LLMNode
	// LLMNode sends requests to the LLM and stores responses in the envelope.
	llmNode := petalflow.NewLLMNode("summarize", client, petalflow.LLMNodeConfig{
		// Model identifier - must match what's available in your Iris provider
		Model: "llama3.2",

		// System prompt sets the LLM's behavior
		System: "You are a helpful assistant. Be concise.",

		// PromptTemplate uses Go text/template syntax
		// Variables from the envelope are accessible via {{.varname}}
		PromptTemplate: "Summarize this in one sentence: {{.text}}",

		// OutputKey is where the LLM response will be stored
		OutputKey: "summary",

		// Timeout prevents hanging on slow responses
		Timeout: 30 * time.Second,

		// RecordMessages adds the conversation to envelope.Messages
		RecordMessages: true,
	})

	// Step 5: Add node and set entry
	if err := g.AddNode(llmNode); err != nil {
		fmt.Printf("Failed to add node: %v\n", err)
		os.Exit(1)
	}
	if err := g.SetEntry("summarize"); err != nil {
		fmt.Printf("Failed to set entry: %v\n", err)
		os.Exit(1)
	}

	// Step 6: Prepare input
	// The text variable matches {{.text}} in the template
	sampleText := `
	PetalFlow is a Go workflow graph runtime for building AI agent workflows.
	It provides nodes for LLM calls, tools, routers, and data transformations.
	Workflows are defined as directed graphs where data flows through an envelope
	that carries variables, messages, and artifacts between nodes.
	`
	env := petalflow.NewEnvelope().WithVar("text", sampleText)

	// Step 7: Run the workflow
	fmt.Println("Sending to LLM...")
	runtime := petalflow.NewRuntime()
	result, err := runtime.Run(context.Background(), g, env, petalflow.DefaultRunOptions())
	if err != nil {
		fmt.Printf("Workflow failed: %v\n", err)
		os.Exit(1)
	}

	// Step 8: Read results
	summary := result.GetVarString("summary")
	fmt.Printf("\nSummary:\n%s\n", summary)

	// You can also access token usage for cost tracking
	if usage, ok := result.GetVar("summary_usage"); ok {
		if u, ok := usage.(petalflow.TokenUsage); ok {
			fmt.Printf("\nTokens used: %d input, %d output\n", u.InputTokens, u.OutputTokens)
		}
	}
}
