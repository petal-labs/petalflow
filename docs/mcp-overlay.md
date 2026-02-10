# MCP Adapter and Overlay Guide

This guide covers the Phase 2 MCP workflow in PetalFlow:

- Register MCP servers over stdio or endpoint mode.
- Build and apply overlays for grouped actions + typed outputs.
- Refresh discovered capabilities after MCP server changes.
- Run health checks with strategy-based behavior.

## Register an MCP Tool (stdio)

```bash
petalflow tools register s3_fetch \
  --type mcp \
  --transport-mode stdio \
  --command go \
  --arg run \
  --arg ./examples/07_mcp_overlay/mock_mcp_server.go \
  --overlay ./examples/07_mcp_overlay/s3_fetch.overlay.yaml
```

## Register an MCP Tool (endpoint mode)

```bash
petalflow tools register github_tools \
  --type mcp \
  --transport-mode sse \
  --endpoint http://localhost:9803/mcp
```

## Refresh and Overlay Updates

```bash
# Re-run discovery (initialize + tools/list)
petalflow tools refresh s3_fetch

# Update overlay path and refresh derived manifest
petalflow tools overlay s3_fetch --set ./examples/07_mcp_overlay/s3_fetch.overlay.yaml
```

## Health Strategies

```bash
# Single tool health check
petalflow tools health s3_fetch

# All MCP tools
petalflow tools health --all
```

Overlay health strategy options:

- `process`: initialize-only process/stdio availability check.
- `connection`: initialize-only endpoint connectivity check.
- `ping`: initialize + `tools/list` ping check.
- `endpoint`: HTTP endpoint check from overlay `health.endpoint`.

## Compatibility Matrix (Phase 2)

| Target Server | Transport | Discovery (`initialize` + `tools/list`) | Invocation (`tools/call`) | Overlay Merge | Notes |
| --- | --- | --- | --- | --- | --- |
| GitHub MCP (`@modelcontextprotocol/server-github`) | stdio | Yes | Yes | Yes | Validate PAT config via overlay `env_var` mapping. |
| Filesystem MCP | stdio | Yes | Yes | Yes | Useful for local file listing/read/write action grouping. |
| Cloud Provider MCP (S3/GCS/Azure variant) | stdio or endpoint | Yes | Yes | Yes | Typed outputs strongly recommended via overlay. |

Known gaps in current implementation:

- Endpoint mode currently uses request/response HTTP transport abstraction and does not yet consume streaming SSE event framing.
- Connection pooling for high-concurrency MCP workloads is deferred.
- Overlay-driven advanced input coercion beyond schema merge is deferred.
- MCP resources/prompts are out of scope for this phase.
