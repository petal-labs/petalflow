# Voice Workflow Example

End-to-end voice demo for PetalFlow: **audio → transcription → agent →
speech** plus a **simulated live voice session** with the tool bridge.

## What it demonstrates

- `speech_transcribe`: typed audio input (envelope bytes) → committed
  transcript text and `transcript` artifact
- `llm_prompt`: the agent step answering the transcript
- `speech_synthesize`: the reply → bounded `audio` artifact (bytes never in
  envelope vars; `output_var` holds the artifact ID)
- Default hydration: `llmprovider` registers Iris-backed speech factories at
  init, so speech nodes hydrate from the provider credential alone — no
  explicit `WithSpeechRecognizerFactory`/`WithSpeechSynthesizerFactory`
- `voice.Session` handles: full-duplex simulated live session with typed
  `iriscore.VoiceEvent` envelopes, turn IDs, turn lifecycle, and
  `voice.ToolBridge` connecting session tool calls to the PetalFlow tool
  registry (allowlist, bounds, policy gate)
- Failure and cancellation: a provider without speech capabilities fails at
  hydration; a cancelled run commits nothing

## Files

- `main.go` — the demos (simulated DAG workflow, simulated live voice
  session, optional live ElevenLabs round trip)
- `voice_workflow.graph.json` — the DAG as a Graph IR definition for the CLI
  (`petalflow validate`, `petalflow compile`, `petalflow run`); the Go demo
  builds the same pipeline programmatically

## Running

```bash
# Simulated demos (no network, no keys required)
go run ./09_voice_workflow

# Optional live ElevenLabs round trip (synthesize -> transcribe)
ELEVENLABS_API_KEY=ek-... go run ./09_voice_workflow
```

The simulated demos register an in-process Iris provider (`voice-demo`) that
implements chat, speech recognition, speech synthesis, and full-duplex voice
sessions deterministically. To point the same pipeline at real providers,
set `provider: elevenlabs` in the node configs and resolve credentials via
`PETALFLOW_PROVIDER_ELEVENLABS_API_KEY`, the config file, or
`--provider-key` (see the README's Provider Credentials section).

## CI tests

`main_test.go` runs the demos deterministically (no network): the DAG
round-trip, the simulated session with the tool bridge, graph-definition
validation, the speech-capability hydration failure, and a cancelled-run
case. The live round trip test runs only when `ELEVENLABS_API_KEY` is set.
