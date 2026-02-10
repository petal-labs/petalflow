# Daemon Tool API

PetalFlow daemon mode exposes tool registration and catalog endpoints for UI and automation workflows.

Start the daemon:

```bash
petalflow serve --host 0.0.0.0 --port 8080
```

## Startup Tool Config Discovery

When `petalflow serve` starts, tool declarations are loaded from the first matching config path:

1. `--config /path/to/petalflow.yaml` (if provided)
2. `./petalflow.yaml`
3. `~/.petalflow/config.yaml`

Files are not merged. First match wins.

## Tool Endpoints

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/tools` | Register a tool |
| `GET` | `/api/tools` | List tools (`status`, `enabled`, `include_builtins` query filters) |
| `GET` | `/api/tools/{name}` | Get a single tool registration |
| `PUT` | `/api/tools/{name}` | Update registration fields |
| `DELETE` | `/api/tools/{name}` | Delete a registration |
| `PUT` | `/api/tools/{name}/config` | Update config values / secrets |
| `POST` | `/api/tools/{name}/test` | Invoke one action with test inputs |
| `GET` | `/api/tools/{name}/health` | Run/read health and status |
| `POST` | `/api/tools/{name}/refresh` | Re-discover MCP actions |
| `PUT` | `/api/tools/{name}/overlay` | Set MCP overlay path and refresh |
| `PUT` | `/api/tools/{name}/disable` | Disable a tool |
| `PUT` | `/api/tools/{name}/enable` | Enable a tool |

Example registration:

```json
{
  "name": "s3_fetch",
  "type": "mcp",
  "transport": {
    "mode": "stdio",
    "command": "./tools/s3-mcp-server"
  },
  "config": {
    "region": "us-west-2"
  },
  "overlay_path": "./tools/s3_fetch.overlay.yaml"
}
```

## Security and Error Semantics

- Sensitive tool config values are always masked in API responses (`**********`).
- File-backed stores persist sensitive config values encrypted at rest.
- Invocation/adapter failures are returned as structured error payloads:

```json
{
  "error": {
    "code": "UPSTREAM_FAILURE",
    "message": "tool: HTTP invoke returned status 503: busy",
    "details": {
      "retryable": true,
      "details": {
        "http_status": 503
      }
    }
  }
}
```

## Background Health Loop

- `petalflow serve` runs a background health scheduler.
- Check cadence is controlled per tool by `manifest.health.interval_seconds`.
- Unhealthy transitions respect `manifest.health.unhealthy_threshold`.
- `GET /api/tools/{name}/health` still triggers an explicit on-demand check.

## Unified Catalog Endpoint

`GET /api/node-types` returns:

- built-in PetalFlow node types
- external tool action node types (`tool_name.action_name`)

Each external action includes full input/output/tool-config schemas in `config_schema`.

## Agent/Task Migration Notes

Use tool action references in `agents.<id>.tools`:

```json
{
  "tools": ["s3_fetch.list", "s3_fetch.download"],
  "tool_config": {
    "s3_fetch": {
      "region": "us-west-2"
    }
  }
}
```

Validation now checks:

1. tool exists
2. action exists on that tool
3. `tool_config` fields match the declared tool config schema

Compiler behavior:

- action is `standalone` when `llm_callable: false` or bytes schemas are present
- action is `function_call` otherwise (or when `llm_callable: true`)
