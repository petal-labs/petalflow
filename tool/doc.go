// Package tool defines the contract boundary for external tool integration.
//
// The package is intentionally split by concern:
//   - manifest: transport-agnostic tool/action schemas
//   - registry: registration metadata and storage interfaces
//   - adapter: invocation interface and transport adapters
//   - validate: validation diagnostics and pipelines
//   - health: health check status models and probing contracts
//
// The package is intentionally transport-agnostic so CLI, daemon, and runtime
// paths can share one manifest/registration contract.
package tool
