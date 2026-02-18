# ADR 0001: Tool Package Boundaries

- Status: Accepted
- Date: 2026-02-09
- Related issue: [#24](https://github.com/petal-labs/petalflow/issues/24)
- Related implementation work: tool package foundation and follow-up issues linked from [#24](https://github.com/petal-labs/petalflow/issues/24)

## Context

PetalFlow needs a tool contract layer that supports native, HTTP, stdio, and MCP-backed tools without leaking transport-specific concerns into compile-time validation and registry operations. The first implementation task requires shipping structure and boundaries before functional behavior.

We need package boundaries that:

1. Keep manifest modeling, registration, invocation, validation, and health concerns separate.
2. Allow CLI and daemon flows to share the same contract layer.
3. Avoid locking into transport-specific implementation details in the foundation step.

## Decision

Create a new `tool` package with boundary-specific files and public contracts:

- `manifest.go`: tool/action/config/transport schema models.
- `registry.go`: registration model and persistence interface for CLI and daemon stores.
- `adapter.go`: transport-agnostic invocation contract and adapter factory.
- `native_adapter.go`: in-process adapter skeleton for native tools.
- `http_adapter.go`: HTTP adapter skeleton.
- `stdio_adapter.go`: stdio adapter skeleton.
- `validate.go`: structured diagnostics and validator pipeline interfaces.
- `health.go`: health status model and probing/monitor interfaces.

The package foundation in this ADR established boundaries first, with production behavior (schema validation, persistence, transport I/O, retries, health loops) implemented incrementally in follow-up issues.

## Dependency Rules

Within the `tool` package, boundaries follow these rules:

1. Manifest types are origin-agnostic and do not depend on adapter or store implementations.
2. Registry records embed manifests but do not depend on concrete adapters.
3. Adapters consume registration/manifest data and expose a shared invocation interface.
4. Validation consumes manifests and registration metadata and returns structured diagnostics.
5. Health consumes registration context and produces normalized health reports.

## Consequences

Positive:

1. Future tasks can implement each concern incrementally with minimal refactoring.
2. Compiler/runtime integration can target stable contracts early.
3. CLI and daemon paths can converge on shared interfaces.

Tradeoffs:

1. Initial implementation shipped boundary-first structure, with full behavior added incrementally after this decision.
2. Additional ADRs may be needed if we later split this package into subpackages for stricter compile-time dependency enforcement.

## Alternatives Considered

1. Single monolithic implementation in one file: rejected because it blurs boundaries and increases future refactor cost.
2. Immediate deep subpackage split (`tool/manifest`, `tool/adapter`, etc.): deferred to keep initial API churn low while the contract is still evolving.
