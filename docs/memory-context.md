# Memory and context management

PetalFlow gives workflows explicit, bounded, and durable control over
conversation memory and retrieved knowledge through the `memory` package:

- a **scope** (`tenant_id`, `namespace`, `session_id`, `thread_id`, `run_id`)
  that the runtime validates and propagates to every node;
- a small **provider contract** (`memory.MemoryProvider`,
  `memory.KnowledgeProvider`) that durable backends such as Cortex implement;
- **token and artifact budgets** with a compaction hook, applied when context
  is assembled for a model call;
- **`memory_recall`** / **`memory_store`** nodes and an `llm_prompt` option that
  sends recalled history as prior turns;
- **events** that make reads, writes, and assembly observable without recording
  content.

PetalFlow ships a process-local `memory.InMemoryProvider` for tests and local
development. It is not durable.

## Scope propagation

A run declares whose memory it may touch through `runtime.RunOptions.Scope`
(or, over HTTP, `options.namespace`, `options.session_id`, `options.thread_id`):

```go
opts := runtime.DefaultRunOptions()
opts.TenantID = identity.TenantID // ownership boundary
opts.Scope = memory.Scope{Namespace: "support", SessionID: "sess-1"}
```

The runtime normalizes the scope, fills `RunID`, inherits `TenantID` from
`RunOptions.TenantID`, rejects a malformed scope before any node executes, and
attaches it to every node context. Nodes and providers read it with
`memory.ScopeFromContext(ctx)`; nodes never invent identifiers.

- `Namespace` is required to address any memory partition.
- `SessionID` is additionally required for conversation memory; knowledge-only
  retrieval works with a namespace alone.
- `ThreadID`, when set, is the conversation container (`Scope.ConversationKey()`);
  otherwise the session is.
- The HTTP API takes the tenant from the authenticated identity, never from the
  request body, and a scope in the body that fails validation returns
  `400 INVALID_SCOPE`.
- Durable runs persist the scope on the `RunRecord`; `Resume` restores it, and a
  resume request that names a different scope is rejected with
  `runtime.ErrScopeMismatch` (`409 SCOPE_MISMATCH` over HTTP).
- `run.started` carries `namespace`, `session_id`, and `thread_id` so traces can
  be correlated per session. Tenant IDs are not written to events.

## Provider contract

```go
type MemoryProvider interface {
    Recall(ctx context.Context, req RecallRequest) (RecallResult, error)
    Remember(ctx context.Context, req RememberRequest) (RememberResult, error)
}

type KnowledgeProvider interface {
    Search(ctx context.Context, req SearchRequest) (SearchResult, error)
}
```

Requirements for an implementation:

- Partition data by `Scope.TenantID` and `Scope.Namespace`; key conversations by
  `Scope.ConversationKey()`.
- Return `Recall` results oldest first, bounded by `Limit` (apply a finite
  default when `Limit` is zero); set `Truncated` when more history exists.
- Return `Search` hits in descending `Score` order, bounded by `TopK`; each hit
  is a `core.Artifact` carrying text or a URI.
- Wrap outage-class failures (transport errors, timeouts, backend down) with
  `memory.Unavailable(err)` so `errors.Is(err, memory.ErrUnavailable)` holds.
  Validation and programming errors must **not** be wrapped; nodes never
  degrade on those.
- Optionally implement `Name() string` for a low-cardinality label used in
  events.

### Cortex and other durable backends

The adapter lives with the backend, not in PetalFlow, so PetalFlow never imports
backend internals. Cortex maps naturally: `Scope.Namespace` → Cortex namespace,
`Scope.ConversationKey()` → Cortex thread, `Scope.TenantID` → tenant, and
knowledge search results → `SearchHit` artifacts with `Meta["title"]`,
`Meta["source"]`, and the chunk score.

Any implementation can be verified without touching PetalFlow internals by
running the conformance suite from its own module:

```go
func TestCortexConformance(t *testing.T) {
    memorytest.RunMemoryProvider(t, func(t *testing.T) memory.MemoryProvider {
        return newCortexAdapter(t) // talks to Cortex through its public API
    })
    memorytest.RunKnowledgeProvider(t, func(t *testing.T, scope memory.Scope, docs []core.Artifact) memory.KnowledgeProvider {
        adapter := newCortexAdapter(t)
        ingest(t, adapter, scope, docs)
        return adapter
    })
}
```

The suite checks ordering, limits, tenant/namespace/session/thread isolation,
error classification, and cancellation behavior.

## Budgets and context assembly

`memory.Budget` bounds how much context enters a prompt:

| Field          | Meaning                                                             |
|----------------|---------------------------------------------------------------------|
| `MaxTokens`    | Estimated tokens of system prompt + prompt + artifacts + history     |
| `MaxMessages`  | Most recent history messages retained                               |
| `MaxArtifacts` | Highest-relevance retrieved artifacts retained                      |

`memory.Assemble` allocates deterministically: the system prompt and current
prompt are reserved first (`memory.ErrBudgetExceeded` if they alone do not
fit), artifacts are admitted in relevance order, and history receives the
remainder. Over-budget history goes through a `memory.Compactor`; the default
`memory.TruncateOldest` drops the oldest turns, and a backend can supply a
summarizing compactor instead. Tokens are estimated by a `memory.TokenCounter`
(default: a conservative characters/4 heuristic); supply an exact tokenizer
when one is available.

`AssemblyStats` reports how the budget was spent and includes a
`prompt_cache_key`: a hash of the stable request prefix (system prompt and
retained history). It lets you correlate provider prompt-cache behavior across
turns without recording the prefix itself; the `llm_prompt` node also passes it
to the provider as `LLMRequest.Meta["prompt_cache_key"]`.

## Nodes

### `memory_recall`

Loads history and/or knowledge for the run scope, fits them to a budget, and
exposes them downstream.

```yaml
- id: recall
  type: memory_recall
  config:
    query_var: question          # envelope var used for knowledge retrieval
    output_var: context          # rendered artifact block for prompt templates
    history_limit: 20
    top_k: 3
    budget: { max_tokens: 2000, max_messages: 10, max_artifacts: 3 }
    record_messages: true        # append retained history to envelope.Messages
    on_unavailable: continue     # default: fail
```

Other keys: `namespace`, `knowledge_namespace` (read a shared knowledge base
while keeping chat memory separate), `roles`, `collections`, `min_score`,
`timeout`, `record_content`, `use_memory`, `use_knowledge`.

Outputs: `<output_var>` (rendered context), `<output_var>_stats` (assembly
stats), `<output_var>_summary` (provider summary, if any), retrieved artifacts
appended to `envelope.Artifacts` with `Meta["score"]` and
`Meta["retrieved_by"]`, and — with `record_messages` — history appended to
`envelope.Messages` tagged `Meta["source"]="memory"`.

### `llm_prompt` with history

```yaml
- id: answer
  type: llm_prompt
  config:
    provider: anthropic
    prompt_template: "Context:\n{{.context}}\n\nQuestion: {{.question}}"
    include_messages: true
    context_budget: { max_tokens: 6000, max_messages: 20 }
```

`include_messages` sends `envelope.Messages` as prior turns (the prompt stays
the final user turn). The node emits `context.assembled` and, when history was
reduced, `context.compacted`.

### `memory_store`

```yaml
- id: store
  type: memory_store
  config:
    entries:
      - { role: user, var: question }
      - { role: assistant, var: answer }
    include_new_messages: false  # also persist messages produced this run
    on_unavailable: continue
```

Messages tagged `Meta["source"]="memory"` are never re-stored.

### Wiring providers

Go API:

```go
factory := hydrate.NewLiveNodeFactory(providers, clientFactory,
    hydrate.WithMemoryProvider(cortexAdapter),   // MemoryProvider (+ Knowledge if implemented)
    hydrate.WithKnowledgeProvider(kb),           // optional separate retrieval backend
    hydrate.WithTokenCounter(tokenizer),         // optional
    hydrate.WithCompactor(summarizer),           // optional
)
```

Daemon: set `server.ServerConfig.Memory` when embedding the server, or start
`petalflow serve --memory-backend inmemory` for a process-local development
backend. Workflows containing memory nodes fail at hydration
(`422 HYDRATE_ERROR`) when no provider is configured.

## Failure behavior

`on_unavailable` governs outage-class errors only (`memory.IsUnavailable`:
`ErrUnavailable`, timeouts, cancellation). Validation and programming errors
always fail the node.

| Policy               | Recall                                                   | Store                                              |
|----------------------|----------------------------------------------------------|----------------------------------------------------|
| `fail` (default)     | Node fails; the run fails unless `ContinueOnError`       | Node fails; the run fails unless `ContinueOnError` |
| `continue`           | Node completes with empty context and history            | Node completes; nothing is written                 |

With `continue`, the degradation is never silent: the node records a
`core.NodeError` on the envelope (`Details["status"]="unavailable"`) and the
event carries `status: "unavailable"`. Each provider call is bounded by the
node `timeout` (default 10s). PetalFlow does not claim a write succeeded unless
`Remember` returned without error.

## Events

All payloads contain identifiers, counts, latencies, and error classes — never
message or document content — unless a node sets `record_content: true`.

| Event               | Payload                                                                                                   |
|---------------------|-----------------------------------------------------------------------------------------------------------|
| `memory.recall`     | `provider`, `namespace`, `session_id`, `thread_id`, `status` (`ok`/`unavailable`/`error`), `history_count`, `retrieved_count`, `latency_ms`, `error_class` |
| `memory.store`      | `provider`, scope identifiers, `status`, `message_count`, `stored_count`, `latency_ms`, `error_class`      |
| `context.assembled` | `system_tokens`, `prompt_tokens`, `history_tokens`, `artifact_tokens`, `total_tokens`, `max_tokens`, `history_included`, `history_dropped`, `artifacts_included`, `artifacts_dropped`, `compacted`, `summarized`, `prompt_cache_key` |
| `context.compacted` | `history_dropped`, `summarized`, `max_tokens`                                                             |

`error_class` is one of `timeout`, `canceled`, `unavailable`, `invalid_scope`,
`budget_exceeded`, or `error`.
