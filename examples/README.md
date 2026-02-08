# PetalFlow Examples

This directory contains example workflows demonstrating PetalFlow features.

## Running Examples

From this directory:

```bash
# Run any example
go run ./01_hello_world
go run ./03_sentiment_router
go run ./04_data_pipeline

# Examples requiring Ollama (install from https://ollama.ai)
ollama pull llama3.2
go run ./02_iris_integration
go run ./05_rag_workflow
```

## Examples

### 01_hello_world
**Minimal workflow with a single node.**

Demonstrates:
- Creating a graph
- Using `FuncNode` for custom logic
- Passing data through the envelope
- Running with `BasicRuntime`

No external dependencies required.

---

### 02_iris_integration
**Connect to LLMs via Iris providers.**

Demonstrates:
- Creating an Iris provider (Ollama)
- Wrapping with `irisadapter.ProviderAdapter`
- Using `LLMNode` for AI-powered steps
- Configuring prompts with templates

Requires: Ollama running locally with `llama3.2` model.

---

### 03_sentiment_router
**Conditional routing based on input content.**

Demonstrates:
- `RuleRouter` for conditional branching
- Multiple edges from router to handlers
- Setting and reading envelope variables

No external dependencies required.

---

### 04_data_pipeline
**Filter and transform data without LLMs.**

Demonstrates:
- `FilterNode` operations (top-N, threshold, dedupe)
- `TransformNode` operations (template rendering)
- Chaining nodes in a pipeline
- Filter statistics

No external dependencies required.

---

### 05_rag_workflow
**Retrieval-Augmented Generation pattern.**

Demonstrates:
- `ToolNode` for document retrieval
- `TransformNode` for context preparation
- `LLMNode` for answer generation
- Full RAG pipeline: Query → Retrieve → Generate

Requires: Ollama running locally with `llama3.2` model.

---

### 06_cli_workflow
**Using the CLI with workflow files.**

Demonstrates:
- Agent/Task YAML schema for defining multi-agent workflows
- Graph IR JSON for low-level graph definitions
- CLI commands: `validate`, `compile`, `run`
- Passing input data and provider credentials via flags

No Go code required — just workflow definition files and the `petalflow` CLI.
