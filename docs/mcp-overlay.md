# MCP Overlay Guide

MCP overlays let you refine discovered MCP tools without modifying the MCP server itself.

Typical uses:

- Rename/group actions
- Mark actions as `llm_callable` or `standalone`
- Provide typed input/output schemas
- Map config keys to environment variables
- Define health strategy overrides

## Register MCP Tool with Overlay

```bash
petalflow tools register s3_fetch \
  --type mcp \
  --transport-mode stdio \
  --command go \
  --arg run \
  --arg ./examples/07_mcp_overlay/mock_mcp_server.go \
  --overlay ./examples/07_mcp_overlay/s3_fetch.overlay.yaml
```

Inspect merged action definitions:

```bash
petalflow tools inspect s3_fetch --actions
```

## Overlay Example

```yaml
overlay_version: "1.0"

group_actions:
  list: list_objects
  download: get_object

action_modes:
  list: llm_callable
  download: standalone

output_schemas:
  list:
    items:
      type: array
      items:
        type: object
        properties:
          key:
            type: string

config:
  region:
    type: string
    required: true
  access_key:
    type: string
    sensitive: true
    env_var: AWS_ACCESS_KEY_ID

health:
  strategy: ping
  interval_seconds: 30
  unhealthy_threshold: 2
```

## Overlay Fields

| Field | Description |
| --- | --- |
| `overlay_version` | Must be `"1.0"` |
| `group_actions` | Map exposed action name -> underlying MCP tool name |
| `action_modes` | `llm_callable` or `standalone` per action |
| `input_overrides` | Override action input schemas |
| `output_schemas` | Override action output schemas |
| `description_overrides` | Override action descriptions |
| `config` | Add/override config fields (supports `env_var`) |
| `metadata` | Optional manifest metadata overrides |
| `health` | Override health strategy and cadence |

## Refresh and Overlay Updates

```bash
# Re-discover from MCP server
petalflow tools refresh s3_fetch

# Change overlay path and refresh manifest
petalflow tools overlay s3_fetch --set ./examples/07_mcp_overlay/s3_fetch.overlay.yaml
```

## Health Strategies

`health.strategy` options:

- `process`: stdio process availability check
- `connection`: endpoint connectivity check
- `ping`: initialize + `tools/list` ping
- `endpoint`: explicit HTTP health endpoint check

For `endpoint`, set `health.endpoint`.

## Notes on MCP Transport Modes

- `stdio`: launches MCP server as a local subprocess
- `sse`: targets an HTTP MCP endpoint

Current `sse` behavior uses request/response JSON-RPC over HTTP while preserving compatibility with SSE-capable endpoints.

## Validate via Example

See the runnable end-to-end example:

- [`examples/07_mcp_overlay`](../examples/07_mcp_overlay)
