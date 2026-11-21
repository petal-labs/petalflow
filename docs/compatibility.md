# PetalFlow 1.0 compatibility contract

This document is the source of truth for supported PetalFlow contract and
dependency combinations. A workflow, API response, or runtime event identifies
the contract it uses; combinations not listed here are unsupported unless a
conformance test says otherwise.

## Contract versions

| Artifact | Current version | How to identify it | Compatibility rule |
| --- | --- | --- | --- |
| Agent workflow schema | `1.0.0` | top-level `schema_version` | Any valid `1.x.x`; missing versions are accepted only for the documented legacy transition |
| Graph workflow schema | `1.0.0` | top-level `schema_version` | Any valid `1.x.x`; missing versions are accepted only for the documented legacy transition |
| HTTP API | `1.0.0` | `PetalFlow-API-Version` response header | Any `1.x.x`; clients must reject an unknown major |
| Runtime events | `1.0.0` | `schema_version` on each serialized event | Any `1.x.x`; stores reject an unknown major before persistence |
| Iris adapter | `1.0.0` | `irisadapter.ContractVersion` | Any `1.x.x`; unrepresentable capabilities fail before provider dispatch |

The canonical constants are exported by the root `contract` package. Event
stores stamp an omitted version as `1.0.0` for pre-versioning in-memory callers
and migrate existing stored events to that version.

## Supported release graph

| Component | Supported combination | Status and boundary |
| --- | --- | --- |
| PetalFlow root module | current checkout; release dependency pin `v0.1.0` in auxiliary modules | Supported |
| Go | `1.25.x` (`go.mod` language version `1.25.0`) | Supported and exercised in CI |
| Iris | `v2.0.x`; repository pin `v2.0.0` | Supported by root, `irisadapter`, and examples; other major versions are unsupported |
| `irisadapter` | contract `1.x` with Iris `v2.0.x` | Supported; see [Iris compatibility](./iris-compatibility.md) |
| Examples | root `v0.1.0`, Iris `v2.0.0`, local checkout of `irisadapter` | Supported; local replacements compile against the exact checkout under test |
| Cortex | no direct Go module dependency | Supported only through a public memory provider implementation or MCP; importing Cortex internals is unsupported, and no Cortex release is certified yet |
| OpenAI | Iris `v2.0.x` provider plus advertised model capabilities | Built-in and adapter-tested; service/model availability is runtime-dependent |
| Anthropic | Iris `v2.0.x` provider plus advertised model capabilities | Built-in and adapter-tested; service/model availability is runtime-dependent |
| Ollama | Iris `v2.0.x` provider plus advertised model capabilities | Built-in and adapter-tested; model capabilities are runtime-dependent |
| Other Iris providers | Iris `v2.0.x` provider registered with Iris | Experimental until requested capabilities pass adapter checks |
| MCP stdio | current MCP initialize/tools contract | Supported |
| MCP Streamable HTTP | sessions, SSE delivery/resumption, bearer auth, and OAuth 2.1 client credentials | Supported |
| Legacy MCP SSE mode | existing request/response transport | Deprecated; migrate to `streamable_http` |

The module-graph compatibility test fails CI if Go or Iris pins drift between
modules, examples stop targeting the checkout under test, or Cortex becomes a
direct dependency. CI and release validation build and test all three Go
modules.

## Change and deprecation policy

- Patch releases may fix behavior without changing valid contract shapes.
- Minor releases may add optional fields, event kinds, endpoints, or
  capabilities. Existing valid inputs remain valid within major `1`.
- A required field, removed field/endpoint, changed meaning, or other breaking
  behavior requires a new major contract version or an explicit migration.
- A deprecated feature remains supported for at least one minor release and is
  called out in release notes with its replacement and removal target.
- Readers ignore unknown optional fields within the same major. Writers emit
  the current version. Unknown majors are rejected with an actionable error.
- Workflow migrations must produce an explicitly versioned artifact. Stored
  event migrations preserve the version they are interpreted as; they do not
  silently reinterpret an unknown major.

## Compatibility checks

The repository enforces this matrix through workflow schema validation, event
store conformance tests, API header tests, Iris adapter compatibility tests,
the module-graph test, and root/adapter/examples jobs in CI and release
workflows. Provider and Cortex integrations outside this repository must run
their public contract conformance tests before being described as supported.
