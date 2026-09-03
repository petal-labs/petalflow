# PetalFlow 1.0.0 Readiness Report

**Date:** 2026-09-02  
**Scope:** PetalFlow at `main` (`66f3d6a`), the Iris 1.0.0 upgrade, the local Cortex repository, and current workflow-runtime industry patterns.  
**Constraint:** This report is analysis only. No source code was changed.

## Executive assessment

PetalFlow has a credible workflow-runtime core and a strong foundation for a 1.0 release: graph compilation, deterministic execution, parallel map/reduce patterns, tool and MCP integration, persistence, schedules, event streams, human nodes, retries, and OpenTelemetry are already represented in the repository.

The largest gap is not missing surface area; it is contract depth. PetalFlow’s workflow and LLM abstractions still expose a narrower chat model than Iris 1.0. The Iris dependency contains structured responses, reasoning, multimodal content, built-in tools, response chaining, batch operations, richer capabilities, and middleware, but most of those capabilities cannot be expressed in a PetalFlow graph or are discarded by `irisadapter`.

My recommendation is to treat 1.0.0 as a production contract release only after the P0/P1 items below are addressed. If the goal is a smaller 1.0 milestone, explicitly define PetalFlow 1.0 as a durable graph runtime with a deliberately scoped chat adapter, and document the unsupported Iris capabilities rather than implying full Iris 1.0 coverage.

## Evidence reviewed

- Root module: `github.com/petal-labs/petalflow`, Go 1.25, Iris v1.0.0.
- Sibling Iris repository: current 1.0.0 API and capability model.
- Sibling Cortex repository: memory, knowledge, workflow-context, and entity-memory primitives.
- PetalFlow README, daemon/API/operations/tool docs, changelog, CI/release workflows, examples, and all root packages.
- Local verification: `go test ./... -count=1` passed; `irisadapter` and `examples` tests passed with an isolated Go build cache; package coverage was measured with `go test -cover ./...`.

The review is based on the code and documentation present on the review date. It is a product-readiness assessment, not a claim that every item must ship in 1.0.0.

## What is already strong

1. **Execution semantics:** the runtime has topological ordering, deterministic parallel result handling, per-node timeout/cancellation, panic recovery, event sequencing, and optional snapshots.
2. **Graph model:** the node vocabulary covers LLM, tool, router, merge, map, gate, transform, filter, cache, webhook, human, and conditional workflows.
3. **Operational primitives:** SQLite and PostgreSQL persistence, event storage, SSE, schedules, tool health, sensitive tool configuration encryption/redaction, and OTel hooks provide a useful operational base.
4. **Developer experience:** CLI validation/compilation, graph/agent APIs, examples, JSON Schema validation, and a separately testable `irisadapter` are good foundations for a stable public API.
5. **Failure visibility:** the event model, tracing hooks, dropped-event counter, and explicit runtime/node errors are better than a black-box “agent runner” abstraction.

## Capability gaps against Iris 1.0

| Iris capability | Current PetalFlow state | 1.0 recommendation |
|---|---|---|
| Chat and streaming | Supported through `LLMClient` and `irisadapter`; the request is primarily a string prompt and the stream preserves text/usage only. | Keep the simple path, but add a typed capability-aware request/response layer and preserve provider response metadata. |
| Structured output | PetalFlow validates JSON locally, while the adapter does not map `JSONSchema` to Iris’s native response-format/schema contract. Object parsing is effectively assumed. | Map strict/non-strict schema options to Iris; support any valid JSON root; validate capability support before execution. |
| Tool calling | Basic PetalFlow tool calls/results exist, but the LLM node configuration does not expose a complete Iris tool definition or tool middleware stack. | Make tool schemas, calls, results, and approval state first-class graph data. |
| Reasoning | Response types have a reasoning field, but request configuration and streaming preservation are incomplete. | Add reasoning effort/configuration and preserve summaries/status in traces and outputs with privacy controls. |
| Responses API | No provider-neutral fields for previous response IDs, response items, truncation, instructions, built-in tools, search options, or tool resources. | Add an explicit Responses-compatible execution mode or broaden the IR without forcing chat-only semantics onto it. |
| Multimodal input | `LLMMessage`/`LLMRequest` primarily carry strings; images/files/content parts are not expressible. | Add typed content parts and artifact references, with size/type/egress policy enforcement. |
| Batch API | No PetalFlow batch workflow/job abstraction. | Defer provider-specific batch nodes, but define an async batch interface for cost-sensitive fan-out workloads. |
| Token counting and cost | Usage is returned and a node-level budget check exists; there is no general preflight token estimate or cumulative run budget policy. | Add model-aware estimates, run-wide budgets, cost ceilings, and a consistent “would exceed budget” decision. |
| Retry/rate limiting/circuit breaking | LLM node retries errors with a simple linear delay; Iris has typed provider errors, retry-after data, middleware, rate limiting, and circuit breaking. | Delegate policy to Iris where possible and expose per-workflow policy controls with classified retry behavior. |
| Conversation/state | Envelope messages are transient execution state. There is no durable conversation/session contract. | Define session/thread/namespace identifiers and a pluggable memory interface; integrate Cortex through a supported adapter or MCP. |
| Telemetry/warnings | OTel exists and Iris has privacy-aware telemetry/warning hooks, but PetalFlow runtime events can include model output and error causes. | Adopt GenAI semantic conventions where applicable and classify/redact event payloads by default. |

## Cortex integration assessment

Cortex is a natural companion: Iris supplies model/provider behavior, PetalFlow supplies orchestration, and Cortex supplies durable context and knowledge. The current relationship is useful but still example-driven rather than productized.

- Cortex’s PetalFlow examples implement custom knowledge/context tools and custom nodes. This proves the integration shape, but it leaves every application to repeat wiring, error policy, namespace propagation, and observability.
- Cortex exposes public types, while key engines live under `internal/`; external PetalFlow users therefore need MCP or a supported public adapter rather than importing the engine directly.
- Cortex’s local `go.mod` still requires Iris v0.15.0 while PetalFlow requires Iris v1.0.0. This is a release-coordination risk: shared examples, provider behavior, error types, and middleware semantics can drift even if both repositories build independently.
- Cortex has conversation memory, hybrid knowledge search, workflow context, and entity memory, but PetalFlow has no standard memory/knowledge node or SPI and no first-class propagation of tenant, namespace, thread, or run context.
- PetalFlow’s MCP support should be brought to the current Streamable HTTP and authorization model before Cortex is positioned as a production integration. Current documentation describes stdio and an older request/response HTTP/SSE style.

**Recommended integration shape:** define a small public `MemoryProvider`/`KnowledgeProvider` contract in PetalFlow (or a separate integration module), carry `tenant_id`, `namespace`, `session_id`, `thread_id`, and `run_id` as validated execution context, and ship a Cortex adapter that supports retrieval, writes, citations, retention policy, and failure semantics. MCP should remain a remote interoperability option, not the only integration path.

## Priority gaps for 1.0.0

### P0 — release blockers for a production runtime

#### 1. Durable execution, resume, and human approval

Snapshots and event persistence are valuable, but they do not yet provide durable checkpoints, pending human-request persistence, deterministic resume, cancellation, or run status retrieval. Human handling is currently tied to the synchronous run request and its configured mode.

Add a persisted run state machine with checkpoint identity, node status, inputs/outputs policy, pending interaction records, resume tokens, cancellation, idempotency keys, and recovery behavior. Expose `GET run`, `cancel`, `resume`, and pending-human-action APIs. Define node idempotency requirements before allowing retry/resume around side effects.

This is the clearest industry baseline: durable checkpoints are used to enable human-in-the-loop, memory, fault tolerance, and deterministic resume in comparable orchestration runtimes. See [LangGraph persistence](https://docs.langchain.com/oss/python/langgraph/persistence) and [LangGraph human-in-the-loop](https://docs.langchain.com/oss/python/langchain/human-in-the-loop).

#### 2. HTTP security and tenant boundaries

The default server CORS origin is `*`, and the server/daemon configuration does not provide authentication, authorization, tenant scoping, request rate limits, or webhook signature/replay protection. Internal error strings and node error causes can also reach API clients or event consumers.

Before production tagging, add an explicit authn/authz middleware boundary, secure-by-default CORS, tenant/resource ownership checks on every workflow/tool/schedule/run route, webhook authentication and replay controls, bounded concurrency/rate limits, redacted public errors, and an outbound egress/SSRF policy for HTTP and MCP tools. Keep API keys and prompts out of logs and persisted events by default.

#### 3. A deliberate Iris 1.0 compatibility contract

The adapter currently converts PetalFlow requests to Iris chat messages, omits native response-format mapping, and drops or narrows response IDs, reasoning, citations, status, content parts, and streaming tool/event data. This creates a compatibility gap that is easy for users to mistake for full Iris support.

Choose and document one of two paths: (a) expand the PetalFlow IR with typed optional capabilities and preserve unknown/provider metadata safely, or (b) declare a supported Iris subset and make unsupported fields fail validation with actionable messages. Path (a) is preferable for a 1.0 platform intended to grow with Iris.

OpenAI’s current Responses guidance illustrates why a chat-only abstraction is becoming limiting: the Responses API is positioned as a unified agentic interface with built-in tools, multimodal inputs, stateful continuation, and typed items distinct from legacy messages. See [Migrate to the Responses API](https://developers.openai.com/api/docs/guides/migrate-to-responses).

#### 4. Current MCP transport and authorization

MCP support is strategically important for Cortex and external tools, but the documented HTTP behavior is not yet the current MCP transport model. Implement Streamable HTTP with session/resumability behavior where applicable, and an OAuth 2.1-compatible authorization flow for protected HTTP servers. Keep stdio credentials in process environment/configuration rather than URL/query material.

See the MCP specification for [transports](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports) and [authorization](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization).

### P1 — high-value enhancements

1. **Async run/job model:** add submit/poll/stream/cancel semantics, worker ownership, retry/backoff, dead-letter handling, run retention, and a distributed schedule lease. The current scheduler is in-process, uses local overlap protection, ignores its `Start` context, and has no multi-replica coordination.
2. **Memory and context management:** add token-aware context budgets, retrieval budgets, compaction/truncation hooks, prompt caching, and explicit context assembly events. Iris supports conversation/memory primitives; Cortex can provide durable retrieval and summarization.
3. **Evaluation and replay:** turn trace snapshots into a documented replay/evaluation workflow with redaction, deterministic fixtures, tool mocks, dataset runs, regression thresholds, and latency/cost metrics. Trace-based evaluation is now a standard way to assess end-to-end model/tool/guardrail behavior; see [OpenAI agent evals](https://developers.openai.com/api/docs/guides/agent-evals).
4. **Routing and fallback:** add provider/model capability discovery, fallback chains, health-aware routing, per-model cost metadata, and explicit behavior when a model does not support structured output, tools, reasoning, or multimodal input.
5. **Tool governance:** add per-workflow tool allowlists, dangerous-tool classification, approval requirements, input/output schema validation, timeout/concurrency budgets, and audit records. Guardian nodes are useful but should not be the only safety boundary.
6. **Observability contract:** map LLM/tool spans and metrics to current OTel GenAI conventions where applicable; define low-cardinality attributes, sampling, prompt/output opt-in, redaction, and event retention. OTel notes that GenAI telemetry requires deliberate handling of sensitive content; see [OpenTelemetry semantic conventions](https://opentelemetry.io/docs/specs/semconv/).
7. **Artifact lifecycle:** move large/binary artifacts to a pluggable blob store, retain only references in envelopes/events, enforce content-type and size limits, and define cleanup/retention behavior.
8. **API ergonomics and compatibility:** version workflow schemas and API responses, publish a compatibility matrix for Iris/Cortex/provider versions, define deprecation policy, and keep examples compiling against the exact release modules.

### P2 — valuable, but not necessary for the first 1.0

- Provider-specific batch optimizations after the generic async job contract exists.
- Voice/realtime/audio streaming.
- Multi-region active/active execution and global scheduling.
- A visual editor or plugin ecosystem, unless the 1.0 audience explicitly requires it.

## Release and quality gaps

The repository’s root tests passed locally, and the auxiliary `irisadapter` and `examples` modules also passed. However, the measured root package coverage is uneven: `bus` 64.3%, `server` 62.4%, `daemon` 51.2%, `llmprovider` 45.8%, `otel` 55.3%, `tool` 65.0%, `tool/mcp` 73.8%, and `traceflow` 10.3%. The CI workflow uploads coverage but does not enforce a threshold. The release workflow runs root tests only, without race detection, auxiliary-module tests, integration tests, or security scans.

Specific release hygiene items:

- Update `CONTRIBUTING.md`, which still says Go 1.24 while the modules and workflows require Go 1.25.
- Add a 1.0 compatibility matrix covering the root module, `irisadapter`, examples, Iris, Cortex, Go, and supported providers.
- Make CI fail on a documented coverage threshold, at least for critical packages and integration paths, rather than treating aggregate Codecov as informational.
- Add release checks for `go test -race`, build of all three modules, integration/conformance tests for SQLite/PostgreSQL and MCP, `gosec`, `govulncheck`, and dependency/license review.
- Produce reproducible release metadata: version, commit, build time, checksums, SBOM, and signed/provenance-attested artifacts. The current workflow creates binaries and SHA-256 checksums, but does not show SBOM generation or artifact signing.
- Ensure release notes identify supported/unsupported Iris 1.0 capabilities and any schema/API compatibility commitments.

## Proposed 1.0 acceptance gates

| Gate | Pass condition |
|---|---|
| Public contract | Every documented workflow/LLM field either executes correctly or fails validation as unsupported; schema versions and deprecations are documented. |
| Reliability | A killed worker can recover a run from a durable checkpoint; human approval can survive process restart; side-effecting nodes have an idempotency story. |
| Security | Authn/authz, tenant isolation, restricted CORS, webhook verification, egress controls, tool policy, and redacted errors/events are tested end-to-end. |
| Operations | Async status/cancel/resume, bounded resource use, distributed schedule coordination, retention, health/readiness, and useful metrics are documented and tested. |
| Observability | Runs can be traced without leaking prompts, responses, credentials, or high-cardinality data by default. |
| Ecosystem | Iris 1.0 compatibility is explicit; Cortex is either upgraded/aligned or the version boundary is supported and tested; current MCP HTTP/auth behavior is covered. |
| Quality | Critical packages meet the agreed coverage bar; root, auxiliary modules, race, integration, build, and security checks are part of the release gate. |
| Supply chain | Binaries have provenance/SBOM/signatures or a documented reason and follow-up for their absence. |

## Suggested sequencing

**0–30 days:** freeze and document the 1.0 contract; expand/validate Iris request and response mapping; fix CORS/error redaction; update Go/version docs; align Cortex dependency strategy; add acceptance-test scaffolding.

**30–60 days:** implement durable run state, resume/cancel/status, persisted human interactions, idempotency, and async job primitives; add tenant/auth/tool policy boundaries; bring MCP transport/auth to the current spec.

**60–90 days:** ship Cortex memory/knowledge integration, evaluation/replay workflows, capability-aware routing and budgets, OTel GenAI conventions, coverage/security/release gates, and a compatibility-tested release candidate.

## Bottom line

PetalFlow is close to being a compelling open workflow runtime, but its current 1.0 risk is the gap between the breadth of the surrounding system and the guarantees users can rely on under restart, concurrency, untrusted HTTP/tool input, human approval, and newer Iris response modes. The highest-return work is contract hardening and durable execution—not adding more node types. Once those foundations are in place, Cortex integration, evaluation, richer Iris capabilities, and provider expansion can compound the platform’s value without creating another layer of bespoke application glue.

## Selected primary sources

- [OpenAI: Migrate to the Responses API](https://developers.openai.com/api/docs/guides/migrate-to-responses)
- [OpenAI: Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
- [OpenAI: Background mode](https://developers.openai.com/api/docs/guides/background)
- [OpenAI: Compaction](https://developers.openai.com/api/docs/guides/compaction)
- [OpenAI: Batch API](https://developers.openai.com/api/docs/guides/batch)
- [OpenAI: Agent evals](https://developers.openai.com/api/docs/guides/agent-evals)
- [Model Context Protocol: Transports](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)
- [Model Context Protocol: Authorization](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization)
- [OpenTelemetry: Semantic conventions](https://opentelemetry.io/docs/specs/semconv/)
- [LangGraph: Persistence](https://docs.langchain.com/oss/python/langgraph/persistence)
- [LangGraph: Human-in-the-loop](https://docs.langchain.com/oss/python/langchain/human-in-the-loop)
