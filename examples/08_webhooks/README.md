# Example: Webhook Nodes (`webhook_trigger`, `webhook_call`)

This directory includes an inbound webhook workflow (`webhook_trigger`) and an outbound webhook workflow (`webhook_call`).

## Files

| File | Purpose |
| --- | --- |
| `webhook_trigger.graph.json` | Receives HTTP requests and maps request data into workflow vars |
| `webhook_call.graph.json` | Sends an outbound webhook request with envelope data |
| `webhook_receiver.ts` | Simple TypeScript HTTP server to receive outbound webhook calls |

## Prerequisites

1. PetalFlow CLI/binary available.
2. `ngrok` installed (for public tunnel testing).
3. Node.js 18+ (for the TypeScript receiver in `webhook_call` demo).

Change to this directory first:

```bash
cd examples/08_webhooks
```

## 1) Start the PetalFlow API

This starts the workflow API on port `8080`.

```bash
petalflow serve --host 0.0.0.0 --port 8080
```

Keep this terminal running.

## 2) Inbound Demo: `webhook_trigger`

### What this workflow does

`webhook_trigger.graph.json` defines:
1. A `webhook_trigger` node with token header auth.
2. A transform that extracts `event_type`.
3. A transform that builds a summary string.

### Step 2.1: Configure the shared token

The trigger expects this header:
`x-petalflow-webhook-token: <token>`

Set the token value:

```bash
export PETALFLOW_WEBHOOK_TOKEN="replace-me"
```

### Step 2.2: Create the workflow

```bash
curl -sS -X POST http://localhost:8080/api/workflows/graph \
  -H 'Content-Type: application/json' \
  --data-binary @webhook_trigger.graph.json | jq
```

### Step 2.3: Open an ngrok tunnel to PetalFlow

This gives a public URL that forwards to your local daemon.

```bash
ngrok http 8080
```

Use the HTTPS forwarding URL from ngrok (example: `https://abc123.ngrok.io`).

### Step 2.4: Send a test webhook through ngrok

```bash
curl -sS -X POST 'https://<your-ngrok-domain>/api/workflows/webhook_trigger_demo/webhooks/incoming?tenant=acme' \
  -H 'Content-Type: application/json' \
  -H "x-petalflow-webhook-token: ${PETALFLOW_WEBHOOK_TOKEN}" \
  -d '{"event_type":"order.created","event_id":"evt_123"}' | jq
```

### Step 2.5: Confirm expected output

You should see output vars such as:
1. `webhook_request`
2. `webhook_body`
3. `webhook_meta`
4. `event_type`
5. `summary`

## 3) Outbound Demo: `webhook_call`

### What this workflow does

`webhook_call.graph.json` defines:
1. A transform that creates `event_key`.
2. A `webhook_call` node that sends JSON to a receiver URL.
3. A transform that summarizes webhook call status.

By default, the workflow sends to:
`http://localhost:8787/webhook`

### Step 3.1: Start the TypeScript receiver

In a new terminal, from `examples/08_webhooks`:

```bash
npx tsx webhook_receiver.ts
```

This server logs request headers/body and returns `200 OK` JSON.

### Step 3.2: Optional: expose the receiver with ngrok

Use this if you want `webhook_call` to hit a public URL instead of localhost.

```bash
ngrok http 8787
```

If you do this, update `webhook_call.graph.json`:
1. Open the file.
2. Find `nodes[1].config.url`.
3. Replace `http://localhost:8787/webhook` with your ngrok URL + `/webhook`.

### Step 3.3: Create the workflow

```bash
curl -sS -X POST http://localhost:8080/api/workflows/graph \
  -H 'Content-Type: application/json' \
  --data-binary @webhook_call.graph.json | jq
```

### Step 3.4: Run the workflow

```bash
curl -sS -X POST http://localhost:8080/api/workflows/webhook_call_demo/run \
  -H 'Content-Type: application/json' \
  -d '{
    "input": {
      "event_type": "invoice.paid",
      "event_id": "evt_456",
      "tenant": "acme"
    }
  }' | jq
```

### Step 3.5: Confirm expected output

In the run response, expect:
1. `event_key`
2. `webhook_result`
3. `status_line`

In the receiver terminal, you should see the incoming payload printed.

## Notes

1. `webhook_trigger` auth in this demo uses `header_token`.
2. `webhook_call` supports `error_policy`; this example uses `"fail"` so non-2xx responses fail the run.
