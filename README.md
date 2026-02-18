# PetalFlow

[![codecov](https://codecov.io/gh/petal-labs/petalflow/graph/badge.svg?token=O26NV7IVRR)](https://codecov.io/gh/petal-labs/petalflow)

PetalFlow is an open-source Go workflow runtime for building AI agent systems as explicit, testable graphs.
It helps you move from prompt experiments to production workflows with clear execution, reusable tools, webhook support, scheduling, event streams, and an HTTP daemon API.

## Why PetalFlow

Use PetalFlow when you want your AI workflows to behave like software systems, not black boxes.

- Build workflows as graphs with explicit nodes and edges.
- Combine LLM steps, tool calls, routing, transforms, human gates, and webhooks.
- Run workflows from Go code, the CLI, or an HTTP daemon.
- Persist workflows, schedules, tools, and events in SQLite.
- Stream and inspect runtime events for debugging and observability.
- Export traces and metrics with OpenTelemetry.

## Installation

### Library

```bash
go get github.com/petal-labs/petalflow
```

### CLI

```bash
go install github.com/petal-labs/petalflow/cmd/petalflow@latest
```

## Quickstart (5 Minutes)

### 1. Run a workflow with no external services

```bash
petalflow run examples/06_cli_workflow/greeting.graph.json \
  --input '{"name":"World"}'
```

This executes a simple Graph IR workflow and prints the output envelope.

### 2. Validate and compile an Agent/Task workflow

```bash
petalflow validate examples/06_cli_workflow/research.agent.yaml
petalflow compile examples/06_cli_workflow/research.agent.yaml --output /tmp/research.graph.json
```

### 3. Run Agent/Task workflow with a provider key

```bash
export PETALFLOW_PROVIDER_ANTHROPIC_API_KEY=sk-ant-...
petalflow run examples/06_cli_workflow/research.agent.yaml \
  --input '{"topic":"Go concurrency patterns"}'
```

## What You Can Build with PetalFlow

- Customer support triage: classify inbound tickets, route by urgency, auto-draft replies.
- Research and writing pipelines: gather information, summarize findings, draft final output.
- Tool-driven automation: call internal APIs, databases, and MCP tools as workflow steps.
- Human-in-the-loop approvals: pause at critical steps for explicit review.
- Webhook automations: receive inbound events (`webhook_trigger`) and send outbound notifications (`webhook_call`).
- Scheduled workflows: run recurring jobs via cron in daemon mode.

## SDK Quickstart (Go)

```go
package main

import (
	"context"
	"fmt"

	"github.com/petal-labs/petalflow"
)

func main() {
	g := petalflow.NewGraph("hello")

	greet := petalflow.NewFuncNode("greet", func(ctx context.Context, env *petalflow.Envelope) (*petalflow.Envelope, error) {
		name := env.GetVarString("name")
		env.SetVar("greeting", fmt.Sprintf("Hello, %s!", name))
		return env, nil
	})

	g.AddNode(greet)
	g.SetEntry("greet")

	env := petalflow.NewEnvelope().WithVar("name", "World")

	rt := petalflow.NewRuntime()
	result, err := rt.Run(context.Background(), g, env, petalflow.DefaultRunOptions())
	if err != nil {
		panic(err)
	}

	fmt.Println(result.GetVarString("greeting"))
}
```

## CLI Overview

PetalFlow CLI supports two workflow formats:

- `Agent/Task` (YAML/JSON): high-level authoring format.
- `Graph IR` (JSON): low-level runtime graph format.

### Core Commands

```bash
# Validate a workflow file
petalflow validate workflow.yaml

# Compile Agent/Task to Graph IR
petalflow compile workflow.yaml --output compiled.graph.json

# Run either Agent/Task or Graph IR
petalflow run workflow.yaml --input '{"topic":"AI agents"}'

# Start daemon API
petalflow serve --host 0.0.0.0 --port 8080
```

### Provider Credentials

Provider resolution order:

1. `--provider-key` flags
2. Environment variables
3. `~/.petalflow/config.json` (or `PETALFLOW_CONFIG`)

Examples:

```bash
export PETALFLOW_PROVIDER_OPENAI_API_KEY=sk-...
export PETALFLOW_PROVIDER_ANTHROPIC_API_KEY=sk-ant-...

petalflow run workflow.yaml \
  --provider-key openai=sk-... \
  --input '{"topic":"Release notes"}'
```

## Agent/Task Workflows (Simple Explanation)

Think of Agent/Task as a project plan for AI work:

- `agent` = who does the work (role + model + provider)
- `task` = what work gets done
- `execution` = in what order tasks run

### Real-World Mental Model

- Research brief:
  One agent researches a topic, another writes a polished summary.
- Incident response:
  One agent classifies severity, another drafts a mitigation plan.
- Content operations:
  One agent outlines, another edits for tone and style.

### Minimal Agent/Task Example

```yaml
version: "1.0"
kind: agent_workflow
id: research_workflow
name: Research Assistant

agents:
  researcher:
    role: Research Analyst
    goal: Gather useful facts about a topic
    provider: anthropic
    model: claude-sonnet-4-20250514

  writer:
    role: Technical Writer
    goal: Turn findings into a concise report
    provider: anthropic
    model: claude-sonnet-4-20250514

tasks:
  research:
    description: Research {{input.topic}} and summarize key points.
    agent: researcher
    expected_output: Structured notes

  write_report:
    description: Write a short report from {{tasks.research.output}}.
    agent: writer
    expected_output: Final report

execution:
  strategy: sequential
  task_order:
    - research
    - write_report
```

## Daemon API

Start daemon mode:

```bash
petalflow serve --host 0.0.0.0 --port 8080
```

Common endpoints:

- `POST /api/workflows/agent` create workflow from Agent/Task
- `POST /api/workflows/graph` create workflow from Graph IR
- `POST /api/workflows/{id}/run` run a workflow
- `GET /api/workflows/{id}/schedules` list cron schedules
- `POST /api/workflows/{id}/schedules` create cron schedule
- `GET /api/runs/{run_id}/events` fetch persisted run events

See full API docs: [`docs/daemon-api.md`](./docs/daemon-api.md)

## Events and OpenTelemetry

PetalFlow emits structured runtime events like:

- `run.started`, `run.finished`
- `node.started`, `node.finished`, `node.failed`
- `tool.call`, `tool.result`
- `route.decision`

### Event Streaming and Retrieval

- CLI: `petalflow run --stream` streams node output events.
- Daemon: run events are persisted and available at `GET /api/runs/{run_id}/events`.

### OpenTelemetry Integration (SDK)

```go
package main

import (
	"context"

	"github.com/petal-labs/petalflow"
	petalotel "github.com/petal-labs/petalflow/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func runWithTelemetry(ctx context.Context, g petalflow.Graph, env *petalflow.Envelope) error {
	tracerProvider := sdktrace.NewTracerProvider()
	meterProvider := sdkmetric.NewMeterProvider()

	tracing := petalotel.NewTracingHandler(tracerProvider.Tracer("petalflow"))
	metrics, err := petalotel.NewMetricsHandler(meterProvider.Meter("petalflow"))
	if err != nil {
		return err
	}

	opts := petalflow.DefaultRunOptions()
	opts.EventHandler = petalflow.MultiEventHandler(tracing.Handle, metrics.Handle)
	opts.EventEmitterDecorator = func(emit petalflow.EventEmitter) petalflow.EventEmitter {
		return petalotel.EnrichEmitter(emit, tracing)
	}

	_, err = petalflow.NewRuntime().Run(ctx, g, env, opts)
	return err
}
```

## Webhooks

PetalFlow supports both directions of webhook automation:

- `webhook_trigger`: start a workflow from an inbound HTTP webhook
- `webhook_call`: send outbound HTTP webhook requests from a workflow

See full walk-through: [`examples/08_webhooks`](./examples/08_webhooks)

## Tools and MCP

PetalFlow includes a tool registry and MCP integration for attaching external capabilities to workflows.

- CLI guide: [`docs/tools-cli.md`](./docs/tools-cli.md)
- MCP overlays: [`docs/mcp-overlay.md`](./docs/mcp-overlay.md)

## Examples

- [`examples/01_hello_world`](./examples/01_hello_world)
- [`examples/02_iris_integration`](./examples/02_iris_integration)
- [`examples/03_sentiment_router`](./examples/03_sentiment_router)
- [`examples/04_data_pipeline`](./examples/04_data_pipeline)
- [`examples/05_rag_workflow`](./examples/05_rag_workflow)
- [`examples/06_cli_workflow`](./examples/06_cli_workflow)
- [`examples/07_mcp_overlay`](./examples/07_mcp_overlay)
- [`examples/08_webhooks`](./examples/08_webhooks)

## Testing

```bash
# Root module tests
go test ./... -count=1

# Integration tests (requires provider key)
export OPENAI_API_KEY=sk-...
go test -tags=integration ./tests/integration/... -count=1 -v
```

## Documentation

Repo docs live in [`docs/`](./docs).

## License

MIT. See [LICENSE](./LICENSE).
