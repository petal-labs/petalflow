# Example: CLI Workflow

This example demonstrates using the `petalflow` CLI to validate, compile, and run workflow files without writing Go code.

## Files

| File | Format | Description |
|------|--------|-------------|
| `research.agent.yaml` | Agent/Task | Two-agent research pipeline (sequential) |
| `greeting.graph.json` | Graph IR | Simple greeting transform node |

## Usage

### Validate a workflow

```bash
# Validate the agent workflow
petalflow validate research.agent.yaml

# Validate with JSON output
petalflow validate research.agent.yaml --format json

# Validate the graph IR
petalflow validate greeting.graph.json
```

### Compile agent workflow to graph IR

```bash
# Compile to stdout
petalflow compile research.agent.yaml

# Compile to a file
petalflow compile research.agent.yaml -o compiled.json

# Validate only (no compiled output)
petalflow compile research.agent.yaml --validate-only
```

### Run a workflow

```bash
# Dry run (validate + compile only)
petalflow run research.agent.yaml --dry-run

# Run with input data
petalflow run greeting.graph.json --input '{"name": "World"}'

# Run with input from a file
petalflow run greeting.graph.json --input-file input.json

# Run with a provider API key
petalflow run research.agent.yaml --provider-key anthropic=sk-ant-... --input '{"topic": "Go generics"}'

# Run with a custom timeout
petalflow run research.agent.yaml --timeout 2m --provider-key anthropic=sk-ant-...
```

## Requirements

- `greeting.graph.json` requires no external services
- `research.agent.yaml` requires an Anthropic API key (via `--provider-key` or `ANTHROPIC_API_KEY` env var)
