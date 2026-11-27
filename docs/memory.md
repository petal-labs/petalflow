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

## Graph retrieval (Cortex v2)

Graph-capable providers (such as Cortex v2 in `search_mode: "graph"`) can
implement the optional `memory.GraphProvider` interface to return compact
entity/relationship `GraphContext` alongside retrieved documents, or attach a
`*memory.GraphContext` under `Document.Metadata[memory.MetadataKeyGraphContext]`:

- Set `RetrievalMode: memory.RetrievalModeGraph` (`"vector"`, `"text"`,
  `"hybrid"`, or `"graph"`), `IncludeGraphContext: true`, and `MaxHops` (`1` or
  `2`) on `MemoryNodeConfig` (or in YAML config).
- `memory.AssembleWithGraph` enforces `ContextBudget.MaxTokens` across both
  rendered graph context (capped at 25% of `MaxTokens`) and documents so
  combined output never exceeds the budget.
- When returned, the assembled `*memory.GraphContext` is rendered into
  `output_var` and exposed structurally on `<output_var>_graph`, while
  `memory.read` events record only content-free `retrieval_mode`,
  `graph_entities`, and `graph_relationships` counts.

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
      retrieval_mode: graph        # optional: vector, text, hybrid, graph
      include_graph_context: true  # optional: attach compact graph triples
      max_hops: 2                  # optional: 1 or 2 for graph mode
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
