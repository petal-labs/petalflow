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

## Workflow Endpoints

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/workflows/agent` | Create workflow from Agent/Task schema |
| `POST` | `/api/workflows/graph` | Create workflow from Graph IR schema |
| `GET` | `/api/workflows` | List workflows |
| `GET` | `/api/workflows/{id}` | Get workflow |
| `PUT` | `/api/workflows/{id}` | Update workflow source and recompile |
| `DELETE` | `/api/workflows/{id}` | Delete workflow |
| `POST` | `/api/workflows/{id}/run` | Execute workflow |
| `GET` | `/api/workflows/{id}/schedules` | List workflow cron schedules |
| `POST` | `/api/workflows/{id}/schedules` | Create workflow cron schedule |
| `GET` | `/api/workflows/{id}/schedules/{schedule_id}` | Get workflow cron schedule |
| `PUT` | `/api/workflows/{id}/schedules/{schedule_id}` | Update workflow cron schedule |
| `DELETE` | `/api/workflows/{id}/schedules/{schedule_id}` | Delete workflow cron schedule |
| `GET` | `/api/runs/{run_id}/events` | Fetch persisted run events |

## Workflow Run Bindings

`POST /api/workflows/{id}/run` accepts:

- `input` (`object`): initial envelope variables.
- `options.timeout` (`duration`): run timeout (default `5m`).
- `options.stream` (`bool`): stream run events via SSE.
- `options.human` (`object`): runtime human handler mode.

`options.human.mode` values:

- `strict` (default): hydrate succeeds, but human node requests fail at runtime with a clear configuration error.
- `auto_approve`: injects an auto-approve handler for all human node requests.
- `auto_reject`: injects an auto-reject handler for all human node requests.

Optional `options.human` fields for auto modes:

- `choice` (string)
- `notes` (string)
- `responded_by` (string)
- `delay` (duration)

Example run request:

```json
{
  "input": {
    "topic": "issue-113"
  },
  "options": {
    "timeout": "30s",
    "human": {
      "mode": "auto_approve",
      "responded_by": "daemon-e2e"
    }
  }
}
```

## Workflow Scheduling (Cron)

Schedules use standard Unix 5-field cron expressions:

`minute hour day-of-month month day-of-week`

Example:

```json
{
  "cron": "*/15 * * * *",
  "enabled": true,
  "input": {
    "topic": "release notes"
  },
  "options": {
    "timeout": "45s",
    "human": {
      "mode": "strict"
    }
  }
}
```

Scheduling behavior:

- Scheduling is UTC-only. Timezone prefixes (`CRON_TZ=...`, `TZ=...`) are rejected.
- Missed runs during daemon downtime are skipped (no backfill).
- If a schedule is due while its previous run is still active, that due run is skipped.
- Scheduled runs default to `options.human.mode = strict` unless explicitly overridden.
- `options.stream` is not allowed on schedules.

Known limitation:

- Multi-daemon scheduling against the same SQLite DB is not coordinated yet. Run one scheduler instance per DB for now.

### `map` and `cache` Node Binding Config

`map` and `cache` execute via embedded node bindings in `config`:

- `map.config.mapper_binding` (or alias `mapper_node`)
- `cache.config.wrapped_binding` (or alias `wrapped_node`)

Both bindings are object node definitions with:

- `type` (required)
- `id` (optional; auto-generated if omitted)
- `config` (optional object)

## Security and Error Semantics

- Sensitive tool config values are always masked in API responses (`**********`).
- SQLite-backed stores persist sensitive config values encrypted at rest.
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

## Background Workflow Scheduler

- `petalflow serve` runs a background workflow scheduler loop.
- Poll interval is configurable via `--workflow-schedule-poll`.
- Due schedules execute through the same runtime path as manual `POST /api/workflows/{id}/run` calls.

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
