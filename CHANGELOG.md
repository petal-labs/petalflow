# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
