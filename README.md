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
Daemon API and migration notes: [`docs/daemon-api.md`](./docs/daemon-api.md)
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

## PetalFlow Designer

PetalFlow Designer is a browser-based UI for building, running, and inspecting workflows visually. It connects to the PetalFlow daemon (`petalflow serve`) and provides:

- **Dual-mode workflow editor** — build with Agent/Task forms or an interactive React Flow graph canvas
- **Tool registry** — register and manage MCP, HTTP, and Stdio tools
- **Live runner** — execute workflows with real-time streaming output, human-in-the-loop review gates, and cancel support
- **Trace viewer** — Gantt-style timeline of every node execution with LLM token counts, tool calls, and timing
- **Workflow library** — save, search, duplicate, export, and import workflows

### Getting Started

**1. Install dependencies**

```bash
cd ui && npm install
```

**2. Start in development mode**

This launches the Go daemon and Vite dev server together (Vite proxies `/api/*` to the daemon):

```bash
make dev
```

Then open [http://localhost:5173](http://localhost:5173).

Daemon data (workflows, auth/settings, providers, tool registrations, and run events) is persisted in SQLite at `~/.petalflow/petalflow.db` by default. Set `PETALFLOW_DB_PATH` or `petalflow serve --db-path <path>` to override.

**3. First-run setup**

On first launch the UI walks you through:

1. **Create admin account** — set a username and password
2. **Configure LLM providers** — add at least one provider key (Anthropic, OpenAI, etc.)
3. **Register tools** *(optional)* — connect MCP servers, HTTP APIs, or Stdio tools
4. **Build your first workflow** — pick a template or start from scratch

**4. Production build**

```bash
make build
```

This compiles the React SPA into `ui/dist/` and embeds it in the Go binary via `embed.FS`. Run the single binary with `petalflow serve` — no separate web server needed.

### Keyboard Shortcuts

Press `?` anywhere in the app to see all available shortcuts, or click the `?` button in the navigation bar.

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
