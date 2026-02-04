// Example: Hello World
//
// This is the simplest PetalFlow workflow. It demonstrates:
// - Creating a graph with one node
// - Using FuncNode for custom logic
// - Passing data through the envelope
// - Running the workflow with BasicRuntime
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
	// Step 1: Create a new graph
	// A graph holds nodes and edges. Give it a name for identification.
	g := petalflow.NewGraph("hello-world")

	// Step 2: Create a node
	// FuncNode wraps a Go function as a workflow node.
	// It receives an envelope, does work, and returns the modified envelope.
	greetNode := petalflow.NewFuncNode("greet", func(ctx context.Context, env *petalflow.Envelope) (*petalflow.Envelope, error) {
		// Read input from the envelope
		name := env.GetVarString("name")
		if name == "" {
			name = "World"
		}

		// Transform the input
		greeting := fmt.Sprintf("Hello, %s!", strings.ToUpper(name))

		// Store the result back in the envelope
		env.SetVar("greeting", greeting)

		return env, nil
	})

	// Step 3: Add the node to the graph
	if err := g.AddNode(greetNode); err != nil {
		fmt.Printf("Failed to add node: %v\n", err)
		return
	}

	// Step 4: Set the entry point
	// The entry node is where execution begins.
	if err := g.SetEntry("greet"); err != nil {
		fmt.Printf("Failed to set entry: %v\n", err)
		return
	}

	// Step 5: Create an envelope with input data
	// The envelope carries data between nodes.
	// WithVar is a fluent method for setting variables.
	env := petalflow.NewEnvelope().WithVar("name", "petalflow")

	// Step 6: Create a runtime and execute
	// The runtime manages graph execution, including ordering and error handling.
	runtime := petalflow.NewRuntime()
	result, err := runtime.Run(context.Background(), g, env, petalflow.DefaultRunOptions())
	if err != nil {
		fmt.Printf("Workflow failed: %v\n", err)
		return
	}

	// Step 7: Read the output
	greeting := result.GetVarString("greeting")
	fmt.Println(greeting)
	// Output: Hello, PETALFLOW!
}
