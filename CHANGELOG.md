# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **Node errors are now surfaced to callers.** The `EnvelopeJSON` wire contract
  (used by `petalflow run --format json` and the daemon HTTP API) previously
  dropped the envelope's recorded node errors entirely, so runs that continued
  past a failed node returned a success-looking result with no trace of the
  failure. `EnvelopeJSON` now includes an `errors` array, and the `pretty` CLI
  output prints an `Errors` section.

### Changed

- **Completed the `EnvelopeJSON` wire contract.** Added `input`, per-message and
  per-artifact `meta`, and trace `parent_id`/`span_id`, which were previously
  discarded during serialization.

## [0.3.0] - 2026-07-31

### Added

- **PostgreSQL persistence backends**: Optional Postgres storage alongside SQLite
  - Event store, workflow/schedule store, and tool registration store backends
  - CLI selects SQLite or Postgres automatically by DSN scheme
  - Shared `sqldialect` placeholder-rebind helper and a registration codec shared between stores
- **Event bus and streaming subsystem**
  - `EventBus` interface with in-memory (`MemBus`) implementation
  - `EventStore` interface with in-memory and `SQLiteEventStore` (WAL mode, retention pruning) backends
  - `StoreSubscriber` for event persistence and `ThrottledEmitter` for delta event coalescing
  - Monotonic per-run sequence generation and correlation fields on events
  - Dot-delimited event kinds and new lifecycle event types
- **Server-Sent Events (SSE)**: SSE handler with replay and live subscription
- **LLM streaming**
  - `StreamingLLMClient` interface and `StreamChunk` type
  - `petalflow run --stream` streaming output
  - `CompleteStream` streaming via the Iris adapter with delta event emission
- **Observability**
  - PetalTrace integration support
  - OpenTelemetry `TracingHandler` (event-to-span), `MetricsHandler` (event-to-metric), and `EnrichEmitter` for trace-context propagation
- **Webhook nodes**: Webhook node implementations with accompanying examples
- **Workflow scheduling**: UTC cron-based workflow scheduling
- **Schema versioning**: Semver `schema_version` with kind normalization and CLI/daemon coverage
- **Live node execution**: Real LLM calls, merge/human/tool node instantiation, and non-LLM node hydration wired into `petalflow run`
- **Testing**: OpenAI integration tests with a daily CI workflow and extensive daemon API / workflow lifecycle e2e coverage

### Changed

- Upgraded Iris dependency from v0.11.0 to v0.15.0
- Adopted SQLite-only persistence defaults before adding optional Postgres backends
- Refactored CLI run, server streaming, sequential runtime, and stdio adapter flows into focused helpers to reduce complexity
- Updated model names in examples to realistic current values
- Rewrote README and documentation guides; added a contributing guide

### Fixed

- Registry-aware graph validation and fail-fast handling for unsupported node hydration
- Deterministic compile ordering for edges and custom strategies
- Context cancellation handling after the stream chunk loop in the Iris adapter
- Import cycle in graph tests and duplicate provider imports
- Provider base URL wiring with multi-module CI enforcement
- gosec SQL formatting findings and golangci-lint CI failures

## [0.2.0] - 2026-02-06

### Added

- **Iris v0.11.0 Tool Features**: Full support for multi-turn tool use workflows
  - `LLMToolResult` type for representing tool execution results
  - `LLMReasoningOutput` type for capturing model reasoning output
  - `LLMMessage.ToolCalls` field for assistant messages with pending tool calls
  - `LLMMessage.ToolResults` field for tool result messages
  - `LLMRequest.Instructions` field for Responses API style prompts
  - `LLMResponse.Reasoning` field for reasoning output from supported models
  - `LLMResponse.Status` field for response completion tracking

- **Subpackage Organization**: Reorganized codebase into logical subpackages
  - `core/` - foundational types, interfaces, and envelope
  - `graph/` - graph and builder implementations
  - `runtime/` - execution runtime and event system
  - `nodes/` - all node implementations
  - Root `petalflow.go` provides backward-compatible re-exports

- **CI/CD Pipeline**: GitHub Actions workflow with lint, test, build, and security scanning
- **Test Coverage**: Increased sink_node test coverage from 50% to 95%
- **Documentation**: Added README and example workflows

### Changed

- Upgraded Iris dependency from v0.10.0 to v0.11.0
- Removed local replace directive for Iris (now using published module)

### Fixed

- CI workflow errors for golangci-lint v2 configuration
- gosec security scanner findings

## [0.1.0] - 2026-02-02

### Added

- Initial release of PetalFlow
- Core types: Envelope, Message, Artifact, TraceInfo
- Node implementations: LLM, Tool, Router, Merge, Map, Filter, Transform, Gate, Cache, Guardian, Human, Sink
- Graph builder with fluent API
- Runtime with event system and step-through debugging
- Iris adapter for provider integration
- Example workflows demonstrating key features
