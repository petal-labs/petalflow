# PetalFlow

[![codecov](https://codecov.io/gh/petal-labs/petalflow/graph/badge.svg?token=O26NV7IVRR)](https://codecov.io/gh/petal-labs/petalflow)

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

## CLI

PetalFlow includes a CLI for working with workflow files without writing Go code. It supports two schema formats:

- **Agent/Task** (YAML or JSON) — a high-level format that defines agents, tasks, and execution strategy
- **Graph IR** (JSON) — the low-level graph definition consumed by the runtime

### Install

```bash
go install github.com/petal-labs/petalflow/cmd/petalflow@latest
```

### Commands

```bash
# Validate a workflow file
petalflow validate workflow.yaml

# Compile an agent workflow to graph IR
petalflow compile workflow.yaml -o compiled.json

# Run a workflow
petalflow run workflow.yaml --input '{"topic": "AI agents"}'

# Dry run (validate + compile only, no execution)
petalflow run workflow.yaml --dry-run

# Register and inspect tools
petalflow tools register echo_http --type http --manifest ./tools/echo_http.tool.json
petalflow tools list
petalflow tools inspect echo_http
```

Tooling quickstart and troubleshooting: [`docs/tools-cli.md`](./docs/tools-cli.md)
MCP adapter and overlay workflow: [`docs/mcp-overlay.md`](./docs/mcp-overlay.md)

### Agent/Task Schema

Define agents and tasks in YAML — the CLI compiles them to a graph automatically:

```yaml
version: "1.0"
kind: agent_workflow
id: my_workflow
name: My Workflow

agents:
  researcher:
    role: Research Analyst
    goal: Find information on a topic
    provider: anthropic
    model: claude-sonnet-4-20250514

tasks:
  research:
    description: Research {{input.topic}}
    agent: researcher
    expected_output: Summary of findings

execution:
  strategy: sequential
  task_order:
    - research
```

```bash
petalflow run workflow.yaml --provider-key anthropic=sk-ant-... --input '{"topic": "Go"}'
```

## Examples

See the [`examples/`](./examples) directory:

| Example | Description |
|---------|-------------|
| [01_hello_world](./examples/01_hello_world) | Minimal workflow with a single node |
| [02_iris_integration](./examples/02_iris_integration) | Connect to LLMs via Iris providers |
| [03_sentiment_router](./examples/03_sentiment_router) | Conditional routing based on input |
| [04_data_pipeline](./examples/04_data_pipeline) | Filter and transform data |
| [05_rag_workflow](./examples/05_rag_workflow) | Retrieval-augmented generation pattern |
| [06_cli_workflow](./examples/06_cli_workflow) | Using the CLI with workflow files |
| [07_mcp_overlay](./examples/07_mcp_overlay) | MCP discovery, overlay merge, and tool registration |

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
