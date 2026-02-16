# PetalFlow

[![codecov](https://codecov.io/gh/petal-labs/petalflow/graph/badge.svg?token=O26NV7IVRR)](https://codecov.io/gh/petal-labs/petalflow)

A lightweight Go workflow graph runtime for building AI agent workflows. Chain LLM calls, tools, routers, and data transformations into directed graphs.

## Installation

Library:

```bash
go get github.com/petal-labs/petalflow
```

CLI:

```bash
go install github.com/petal-labs/petalflow/cmd/petalflow@latest
```

## Quick Start (5 minutes)

Run a graph with no external services:

```bash
petalflow run examples/06_cli_workflow/greeting.graph.json --input '{"name":"World"}'
```

Run an Agent/Task workflow with Anthropic:

```bash
export PETALFLOW_PROVIDER_ANTHROPIC_API_KEY=sk-ant-...
petalflow run examples/06_cli_workflow/research.agent.yaml --input '{"topic":"Go concurrency patterns"}'
```

## Execution Modes

| Mode | Best for | Entry point |
|------|----------|-------------|
| Go SDK | Programmatic graph construction in Go apps | `petalflow.NewGraph(...)` + `runtime.Run(...)` |
| CLI | File-driven validation/compile/run workflows | `petalflow validate|compile|run ...` |
| Daemon API | Tool catalog and workflow execution over HTTP | `petalflow serve --host 0.0.0.0 --port 8080` |

Daemon docs and endpoints: [`docs/daemon-api.md`](./docs/daemon-api.md)

## Provider Setup

Provider resolution priority is:
1. `--provider-key` CLI flags
2. environment variables
3. `~/.petalflow/config.json` (or `PETALFLOW_CONFIG`)

Environment variable pattern:

```bash
PETALFLOW_PROVIDER_<PROVIDER>_API_KEY=...
PETALFLOW_PROVIDER_<PROVIDER>_BASE_URL=...
```

Examples:

```bash
export PETALFLOW_PROVIDER_OPENAI_API_KEY=sk-...
export PETALFLOW_PROVIDER_OPENAI_BASE_URL=https://api.openai.com/v1
export PETALFLOW_PROVIDER_ANTHROPIC_API_KEY=sk-ant-...
```

CLI override example:

```bash
petalflow run workflow.yaml \
  --provider-key openai=sk-... \
  --input '{"topic":"LLM eval design"}'
```

Optional config file (`~/.petalflow/config.json`):

```json
{
  "providers": {
    "openai": {
      "api_key": "sk-...",
      "base_url": "https://api.openai.com/v1"
    },
    "anthropic": {
      "api_key": "sk-ant-..."
    }
  }
}
```

## Tool Execution (CLI/Daemon)

Standalone tool nodes execute only when the referenced tool action exists in the runtime tool registry.
For file/compiled workflows, that means:
1. Register tools (`petalflow tools register ...`).
2. Run with the same tool store (`--store-path` if not using the default).
3. Ensure action names used by agents are valid (`tool_name.action_name`).

Useful commands:

```bash
petalflow tools list
petalflow tools inspect <tool_name> --actions
petalflow tools test <tool_name> <action_name> --input key=value
```

Tooling quickstart: [`docs/tools-cli.md`](./docs/tools-cli.md)  
MCP overlays: [`docs/mcp-overlay.md`](./docs/mcp-overlay.md)

## Node Support Matrix (Graph IR via CLI/Server)

| Node type | Registered in catalog | Hydrated and executable | Notes |
|-----------|-----------------------|--------------------------|-------|
| `llm_prompt`, `llm_router` | Yes | Yes | Requires configured provider credentials |
| `rule_router`, `filter`, `transform`, `gate`, `guardian`, `sink` | Yes | Yes | Executable from Graph IR config |
| `merge`, `conditional`, `noop`, `tool` | Yes | Yes | `tool` requires valid `config.tool_name` and tool registry entry |
| `tool_name.action_name` | Dynamic | Yes | Compiled from agent tool actions and resolved from tool registry |
| `human` | Yes | CLI: Yes, Server: Depends | Requires a `HumanHandler`; CLI wires one, server integrations must provide one |
| `map`, `cache` | Yes | Partial | Require runtime bindings not encodable directly in Graph IR config |
| `func` | Yes | Explicit no-op in Graph IR | Custom Go callbacks must be wired in SDK code, not serialized config |

## SDK Quick Start

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

## Testing

```bash
# Unit and package tests (no external provider calls)
go test ./... -count=1
```

```bash
# External integration tests (OpenAI required)
export OPENAI_API_KEY=sk-...
go test -tags=integration ./tests/integration/... -count=1 -v
```

Optional provider matrix expansion:

```bash
# Enables Anthropic integration tests in the same matrix
export ANTHROPIC_API_KEY=...
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

- **Agent/Task** (YAML or JSON): high-level format defining agents, tasks, and strategy.
- **Graph IR** (JSON): low-level graph definition consumed by the runtime.

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

Phase 4 hardening behaviors (health/retries/secrets/observability): [`docs/phase4-hardening.md`](./docs/phase4-hardening.md)

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
