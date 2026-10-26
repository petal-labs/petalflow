# Memory and Context Management

PetalFlow gives workflows explicit, bounded, and durable control over
conversation and retrieved context. This document covers the memory provider
contract, context budgets, scope propagation, failure semantics, and
observability.

## Provider contract

The `memory` package defines the pluggable interface implemented by backends
such as Cortex:

```go
type Provider interface {
    Retrieve(ctx context.Context, query memory.RetrievalQuery) ([]memory.Document, error)
    Write(ctx context.Context, req memory.WriteRequest) error
}
```

The contract is deliberately dependency-free: a Cortex adapter implements
`memory.Provider` in its own module and never imports PetalFlow internals
beyond the `memory` package. Implementations are verified with the conformance
suite:

```go
func TestCortexProviderConformance(t *testing.T) {
    memorytest.RunConformance(t, NewCortexProvider(cfg))
}
```

`memory.NewInMemoryProvider()` is a process-local reference implementation for
tests and local development.

## Scope: session, thread, namespace, tenant, run

Memory reads and writes are addressed by a validated `memory.Scope`:

| Field       | Required | Purpose                                   |
|-------------|----------|-------------------------------------------|
| `Namespace` | yes      | Knowledge partition                       |
| `SessionID` | yes      | Stable conversation identifier            |
| `ThreadID`  | no       | Branch of a session                       |
| `TenantID`  | no       | Caller isolation                          |
| `RunID`     | no       | Tied to a single workflow execution       |

Identifiers may contain letters, digits, `-`, `_`, and `.` (bounded length, no
path separators) so they form stable storage keys. Scopes propagate in two
ways:

- **Run-level:** set `RunOptions.MemoryScope`; the runtime validates it and
  every node sees it via `runtime.ScopeFromContext`.
- **Node-level:** a memory node's `Namespace` / `SessionID` / `ThreadID` /
  `TenantID` config fields override individual fields, rendered as templates
  against the envelope (`"{{.vars.user_id}}"`).

## Context budgets and assembly

Retrieved context is assembled under explicit budgets:

- `memory.ContextBudget{MaxTokens, MaxDocuments}` bounds the token estimate
  (default counter: ~4 chars/token; supply a model-specific
  `memory.TokenCounter` for precision) and the number of documents/artifacts.
- Retrieval budgets flow to the provider via `RetrievalQuery.Limit` /
  `MaxTokens`; the node re-enforces the budget during assembly so a
  non-compliant provider cannot overrun.
- `memory.Compactor` is the compaction/truncation hook. The default
  `TruncateCompactor` keeps ranked documents and truncates the first
  overflowing one; supply a custom implementation (e.g. summarization) for
  smarter policies.

Assembly results are injected as `chunk` artifacts (disable with
`OmitArtifacts`), a rendered context string on `output_var`, and a content-free
`<output_var>_stats` result.

## Failure behavior

Providers signal a reachable-but-down backend by wrapping
`memory.ErrUnavailable`. The node's `OnUnavailable` policy decides the
reaction:

- `fail` (default): the node returns the error and the runtime's error policy
  (`ContinueOnError`) governs the run.
- `skip`: the node continues without memory — retrieval yields no context,
  writes are dropped — and the corresponding event carries `unavailable=true`.

All other provider errors are ordinary node failures.

## Observability

Memory reads, writes, and assembly emit runtime events:

- `memory.read` — scope identifiers, query length, document count, estimated
  tokens, latency, `unavailable`.
- `memory.write` — scope identifiers, byte count, latency, `unavailable`.
- `context.assembled` — documents/tokens in and out, `truncated`, strategy.

Events are **content-free by default**: document text, queries, and written
content are never included. Setting `include_content_in_events` opts in to
attaching document text for debugging. The `llm.response` event additionally
reports `cache_read_tokens` / `cache_creation_tokens` when the provider
reports prompt-cache activity.

## Declarative workflows

Register a provider when hydrating:

```go
factory := hydrate.NewLiveNodeFactory(providers, clientFactory,
    hydrate.WithMemoryProvider(cortexAdapter))
```

Then use the `memory` node type:

```yaml
nodes:
  - id: recall
    type: memory
    config:
      query_template: "{{.input}}"
      namespace: support
      session_id: "{{.vars.session_id}}"
      max_documents: 8
      max_tokens: 2000

  - id: remember
    type: memory
    config:
      mode: write
      write_var: summary
      write_key: "summary-{{.vars.ticket}}"
      namespace: support
      session_id: "{{.vars.session_id}}"
```
