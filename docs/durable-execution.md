# Durable execution

PetalFlow can persist run state by supplying a `runtime.RunStore` to a server
or `runtime.RunOptions`. Durable execution currently uses a sequential queue so
the checkpoint is unambiguous.

Each checkpoint contains the envelope, visited nodes, hop counts, and the next
node queue. It is written before a node starts and after the node's successors
are selected. A worker can therefore resume from the last safe point:

```go
result, err := runtime.NewRuntime().Resume(ctx, graph, runID, runtime.RunOptions{
    RunStore: store,
})
```

If a worker stops after a side effect begins but before its checkpoint is
advanced, the node may run again. Side-effecting nodes must use a stable caller
idempotency key (`RunOptions.IdempotencyKey`) with the external system and make
repeated requests safe. PetalFlow does not claim a side effect completed unless
the node result was committed to the checkpoint.

The HTTP server exposes:

- `GET /api/runs/{run_id}` for status;
- `POST /api/runs/{run_id}/cancel` for terminal cancellation;
- `POST /api/runs/{run_id}/resume` to continue from the checkpoint;
- `GET /api/runs/{run_id}/pending-actions` to retrieve a waiting human action;
- `POST /api/runs/{run_id}/pending-actions/{action_id}` to complete it exactly once.

Human actions are stored separately from event payloads. Snapshot events contain
only checkpoint identity and navigation metadata; the checkpoint store remains
the authorized source for recovery state.
