# PetalFlow

A lightweight Go workflow graph runtime for building AI agent workflows. Chain LLM calls, tools, routers, and data transformations into directed graphs.

## Installation

```bash
go get github.com/petal-labs/petalflow
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "strings"

    "github.com/petal-labs/petalflow"
)

func main() {
    // Create a graph
    g := petalflow.NewGraph("hello")

    // Add a node that transforms input
    node := petalflow.NewFuncNode("greet", func(ctx context.Context, env *petalflow.Envelope) (*petalflow.Envelope, error) {
        name := env.GetVarString("name")
        env.SetVar("greeting", fmt.Sprintf("Hello, %s!", strings.ToUpper(name)))
        return env, nil
    })

    g.AddNode(node)
    g.SetEntry("greet")

    // Create an envelope with input data
    env := petalflow.NewEnvelope().WithVar("name", "world")

    // Run the workflow
    runtime := petalflow.NewRuntime()
    result, _ := runtime.Run(context.Background(), g, env, petalflow.DefaultRunOptions())

    fmt.Println(result.GetVarString("greeting"))
    // Output: Hello, WORLD!
}
```

## Core Concepts

### Graph
A directed graph of nodes. You add nodes, connect them with edges, and set an entry point.

### Node
A unit of execution. Each node receives an envelope, does work, and returns an envelope. Built-in node types:

| Node | Purpose |
|------|---------|
| `LLMNode` | Call an LLM with a prompt |
| `ToolNode` | Execute a tool/function |
| `RuleRouter` | Route based on conditions |
| `LLMRouter` | Route using LLM classification |
| `FilterNode` | Filter lists by criteria |
| `TransformNode` | Reshape data |
| `MergeNode` | Combine parallel branches |
| `FuncNode` | Run custom Go code |

### Envelope
The data carrier that flows between nodes. Contains:
- `Vars` - Key-value store for passing data
- `Messages` - Chat-style messages for LLM context
- `Artifacts` - Documents, files, or structured outputs
- `Trace` - Run ID and timing info

### Runtime
Executes the graph. Handles node ordering, parallel branches, retries, and step-through debugging.

## Examples

See the [`examples/`](./examples) directory:

| Example | Description |
|---------|-------------|
| [01_hello_world](./examples/01_hello_world) | Minimal workflow with a single node |
| [02_iris_integration](./examples/02_iris_integration) | Connect to LLMs via Iris providers |
| [03_sentiment_router](./examples/03_sentiment_router) | Conditional routing based on input |
| [04_data_pipeline](./examples/04_data_pipeline) | Filter and transform data |
| [05_rag_workflow](./examples/05_rag_workflow) | Retrieval-augmented generation pattern |

## LLM Integration with Iris

PetalFlow integrates with [Iris](https://github.com/petal-labs/iris) for LLM access:

```go
import (
    "github.com/petal-labs/iris/providers/openai"
    "github.com/petal-labs/petalflow"
    "github.com/petal-labs/petalflow/irisadapter"
)

// Create an Iris provider
provider := openai.New(openai.WithAPIKey("your-key"))

// Wrap it for PetalFlow
client := irisadapter.NewProviderAdapter(provider)

// Use in an LLMNode
llmNode := petalflow.NewLLMNode("chat", client, petalflow.LLMNodeConfig{
    Model:  "gpt-4",
    System: "You are a helpful assistant.",
    PromptTemplate: "{{.question}}",
    OutputKey: "answer",
})
```

## License

MIT License - see [LICENSE](LICENSE) for details.
