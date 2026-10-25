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

- `stdio`: launches MCP server as a local subprocess. Credentials stay in
  process environment/configuration via config-to-env mapping (`env_var`
  in the overlay); they are never placed in URLs or logs.
- `streamable_http`: targets an MCP server over the current Streamable
  HTTP transport — POST-based JSON-RPC with `Mcp-Session-Id` session
  handling, SSE response streams, server-to-client delivery over a GET
  stream with `Last-Event-ID` resumption, and automatic session
  re-initialization after expiry.
- `sse` (legacy): the older request/response JSON-RPC over HTTP mode.
  It still works, but new registrations should use `streamable_http`.

### Authorization for HTTP Modes

`streamable_http` and `sse` endpoints support two authorization options,
both sourced from registration config (sensitive config values are
encrypted at rest and redacted in API responses, logs, and events):

```json
{
  "type": "mcp",
  "mode": "streamable_http",
  "endpoint": "https://mcp.example.com/mcp",
  "auth": {
    "type": "bearer",
    "token_ref": "api_key"
  }
}
```

For OAuth 2.1-protected servers, configure the client credentials flow.
Token endpoint metadata is discovered automatically via protected
resource and authorization server metadata (RFC 9728 / RFC 8414) when
`token_url` is omitted:

```json
{
  "type": "mcp",
  "mode": "streamable_http",
  "endpoint": "https://mcp.example.com/mcp",
  "auth": {
    "type": "oauth2",
    "oauth": {
      "client_id": "petalflow",
      "client_secret_ref": "client_secret",
      "scopes": ["tools:call"],
      "resource": "https://mcp.example.com"
    }
  }
}
```

Rules enforced at registration time:

- `token_ref` / `client_secret_ref` must name config fields marked
  `sensitive`, so credentials are encrypted at rest and never rendered
  in API output.
- Endpoint URLs must not embed credentials (`user:pass@host` or
  credential-like query parameters such as `access_token`).
- HTTP endpoints are validated against the outbound egress (SSRF)
  policy; set `allow_private_network: true` only for trusted
  loopback/private deployments.

Error behavior: unauthorized (`401`, after one automatic token refresh)
and insufficient-scope (`403`) failures are terminal and reported as
`UNAUTHORIZED` / `FORBIDDEN` tool errors. Expired sessions are recovered
transparently by replaying the initialize handshake; interrupted GET
streams resume with `Last-Event-ID` or surface an unambiguous terminal
`TRANSPORT_FAILURE` after the reconnect budget is exhausted.

## Migrating from `sse` to `streamable_http`

1. Change `"mode": "sse"` to `"mode": "streamable_http"` and keep the
   same `endpoint`. Streamable HTTP is a superset of the old
   request/response behavior: servers that only answered
   `application/json` continue to work.
2. If you passed credentials out-of-band, declare them as sensitive
   config fields and reference them via `auth.token_ref` or an overlay
   `header` mapping instead.
3. Re-run `petalflow tools register` to validate and probe reachability.
   The registration probe performs a real MCP initialize against the
   endpoint, so a working probe means sessions, authorization, and
   tool discovery all work.

## Validate via Example

See the runnable end-to-end example:

- [`examples/07_mcp_overlay`](../examples/07_mcp_overlay)
