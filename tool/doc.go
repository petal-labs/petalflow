// Package tool defines the contract boundary for external tool integration.
//
// The package is intentionally split by concern:
//   - manifest: transport-agnostic tool/action schemas
//   - registry: registration metadata and storage interfaces
//   - adapter: invocation interface and transport adapters
//   - validate: validation diagnostics and pipelines
//   - health: health check status models and probing contracts
//
// This package currently provides skeleton interfaces and data models that
// subsequent implementation tasks will fill in.
package tool
