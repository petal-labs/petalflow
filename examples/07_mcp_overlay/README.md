# MCP Overlay Example

This example provides a runnable local MCP stdio server and an overlay.

## 1. Register the example MCP server

```bash
petalflow tools register s3_fetch \
  --type mcp \
  --transport-mode stdio \
  --command go \
  --arg run \
  --arg ./examples/07_mcp_overlay/mock_mcp_server.go \
  --overlay ./examples/07_mcp_overlay/s3_fetch.overlay.yaml
```

## 2. Inspect discovered + merged actions

```bash
petalflow tools inspect s3_fetch --actions
```

Expected grouped action names include `list` and `download`.

## 3. Invoke a test action

```bash
petalflow tools test s3_fetch list --input bucket=reports
```

## 4. Refresh discovery after server changes

```bash
petalflow tools refresh s3_fetch
```
