# Voice Sessions: Session-Oriented Voice Execution Boundary

This document specifies PetalFlow's execution boundary for full-duplex,
real-time voice conversations (ElevenLabs Voice FRD; issue #252). It is built
on the Iris voice session API (`iris/v2@v2.0.0`, gates iris #155 "real-time
voice session and event API" and #156 "Iris-managed voice pipeline", both
shipped in v2.0.0) and deliberately stays **separate from one-shot DAG
nodes**.

## Scope and separation from DAG execution

One-shot DAG nodes (`llm_prompt`, `speech_transcribe`, `speech_synthesize`,
...) are stateless envelope transformations: one `Run(ctx, envelope)` call,
one committed output. Voice conversations are the opposite: long-lived,
stateful, concurrent, interruptible, and event-driven. Forcing them into the
graph IR would break the envelope semantics (turns mutate continuously,
interruptions abandon partial output, audio must not round-trip through
envelopes).

The boundary therefore is:

- **Graph IR is unchanged.** No new node kind, no new port types, no new
  envelope contracts. Existing graph validation (`GR-*` diagnostics) and
  tool contracts are untouched.
- **Sessions live outside the DAG.** A `voice.Session` handle is created,
  owned, and closed by the embedding application (service, CLI, host). It is
  never a graph node, never hydrated by the graph factory, and never
  participates in replay: sessions are live interaction, not recorded
  execution.
- **Sessions may coexist with runs.** A host can run PetalFlow workflows
  concurrently with voice sessions and hand results between them (e.g. a
  workflow produces text that a session speaks via `SendText`).

## Session handles

`voice.Session` (package `github.com/petal-labs/petalflow/voice`) is the
typed handle for one live session:

- Created by a `voice.SessionStarter` — `llmprovider.NewVoiceSessionStarter(name, cfg)`
  resolves provider credentials through the standard hydration flow (config
  file, `PETALFLOW_PROVIDER_<NAME>_API_KEY`, `--provider-key`), discovers the
  Iris `voice_session` capability, wraps the provider in an Iris client
  (rate limiting), and fails **at construction** for unsupported providers or
  missing credentials. Construction errors never echo the API key.
- The handle owns:
  - the underlying `iriscore.VoiceSession` (transport),
  - a session-scoped context (cancelled by `Close` or parent cancellation),
  - an `iriscore.VoiceTurnTracker` (turn IDs and stale-audio filtering).
- Operations: `ID`, `State`, `SendAudio`, `SendText`, `SubmitToolResult`,
  `StartTurn`, `CompleteTurn`, `Interrupt`, `ObserveEvent`,
  `ShouldPlayAudio`, `Events`, `Err`, `Close`.
- `Close` is idempotent and safe for concurrent calls; it cancels the active
  turn, cancels the session context, and closes the underlying session
  exactly once.

## Event envelopes

Events are **Iris `iriscore.VoiceEvent` envelopes, reused verbatim** —
PetalFlow does not define its own voice event types (no duplicated speech
logic). Envelope properties:

- `Type` is one of the typed Iris kinds: lifecycle (`session_started`,
  `session_state`, `session_ended`), turn lifecycle
  (`turn_started`, `turn_completed`, `turn_interrupted`), transcripts
  (`user_transcript_partial`/`_committed`), assistant text
  (`agent_text_delta`/`_committed`), audio (`audio_output`,
  `audio_output_ended`), tools (`tool_call`, `tool_result`), and `error`.
- `SessionID` and `TurnID` correlate every transcript, text delta, audio
  chunk, tool exchange, and interruption to its session and turn.
- `Sequence` is a per-session monotonic index; `Timestamp` the SDK receive
  time.
- Delivery is **bounded**: the underlying channel has a fixed capacity
  (default 64, configurable via `VoiceSessionConfig.EventBufferSize`), so a
  slow consumer cannot cause unbounded memory growth. `Emit` reports
  `iriscore.ErrVoiceSessionClosed` or `ctx.Err()` instead of blocking
  forever or dropping.
- `Events()` and `Err()` close promptly on session termination, provider
  failure, or parent-context cancellation. `Err()` emits at most one terminal
  error.
- Privacy: Iris `VoiceEvent.String()`/`GoString()` omit raw audio, transcript
  text, assistant text, and tool payloads; PetalFlow forwards the envelopes
  unmodified, so default logging stays metadata-only.

## Turn IDs

Turns are the unit of interruption and correlation:

- `Session.StartTurn()` returns a monotonic turn ID (`turn-1`, `turn-2`, ...)
  plus a per-turn context. All downstream work for the turn (assistant
  generation, audio playback, tool execution) must be bound to that context.
- `StartTurn` on an already-active turn **supersedes** it: the previous
  turn's context is cancelled and its ID is recorded as cancelled.
- `CompleteTurn(turnID)` releases the active turn's context; re-completing or
  completing a non-active turn returns false.
- `ActiveTurnID` returns the active, non-interrupted turn (empty otherwise).
- Provider-driven lifecycle is folded back via `ObserveEvent`
  (`turn_started`, `turn_interrupted`, `turn_completed`, `session_ended`) so
  consumer-side tracking stays in sync with the provider's view.
- Cancelled-turn history is bounded (Iris keeps at most 128 recent IDs), so
  long sessions use O(1) memory.

## Interrupt and cancel propagation

Three cancellation paths, all converging on the turn tracker:

1. **Client cancel** — `Session.Interrupt()`: cancels the active turn's
   context, marks the turn cancelled, forwards the interrupt to the Iris
   session (which stops in-flight generation), and returns the
   `VoiceInterruption{CancelledTurnID, Reason: client_cancel}` payload.
   Repeated interrupts are no-ops.
2. **Barge-in** — the provider emits `turn_interrupted` with reason
   `barge_in`; `ObserveEvent` cancels and marks the active turn. Hosts that
   detect barge-in locally (e.g. echo cancellation) may instead call
   `Interrupt()` themselves.
3. **Session termination** — `Close`, parent-context cancellation, or
   provider failure: the active turn is cancelled with reason
   `session_closed`, the session context is cancelled, and `Events`/`Err`
   close promptly.

**Stale audio suppression:** playback consumers must gate every
`audio_output` event through `Session.ShouldPlayAudio(evt)`. Audio chunks
belonging to interrupted, superseded, or completed turns are never playable —
so an interrupted assistant response cannot leak stale audio after barge-in.

## Failure and cancellation behavior

- **Construction failures** (unsupported capability, unresolved credentials,
  unknown provider) are hydration-time errors with actionable resolution
  paths; no network call is required.
- **Start failures** (rate limit, provider handshake) surface from
  `StartVoiceSession` before any turn begins; no handle is returned.
- **Mid-turn failure** arrives as a `VoiceEventError` envelope and/or a
  terminal error on `Err()`; the session transitions per the provider's
  lifecycle. Hosts decide whether to restart a session or surface the error.
- **Parent-context cancellation** terminates the session asynchronously:
  `Events`/`Err` close, turn contexts cancel, and subsequent `SendAudio`/
  `SendText`/`SubmitToolResult` calls return
  `iriscore.ErrVoiceSessionClosed` without touching the provider.
- **No partial audio is committed anywhere**: audio lives only in the event
  stream; nothing is written to envelope vars or artifacts (contrast with the
  one-shot `speech_synthesize` node).

## Tools

Voice sessions receive tools through `iriscore.VoiceSessionConfig.Tools`
(Iris `core.Tool` contract — unchanged from Iris). Model-requested
invocations arrive as `tool_call` envelopes; hosts execute and deliver
outcomes with `Session.SubmitToolResult` (`tool_result` envelopes). PetalFlow
tool contracts (`core.PetalTool`, tool registry, MCP overlays) are untouched.

## Iris dependency revision

Root, `irisadapter`, and `examples` require `github.com/petal-labs/iris/v2
v2.0.0` (enforced by `compatibility/module_graph_test.go`). The voice
session/event API and the managed pipeline shipped at that revision
(iris #155, #156).
