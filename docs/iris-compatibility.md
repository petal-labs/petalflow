# PetalFlow Iris 2.0 compatibility contract

PetalFlow exposes a provider-neutral LLM request model and adapts it to Iris
2.0 `core.ChatRequest` through `irisadapter`. The adapter supports the
capabilities below and rejects requested capabilities before calling a
provider when the provider or its model catalog does not advertise them.

| Iris 2.0 capability | PetalFlow contract | Adapter behavior |
| --- | --- | --- |
| Chat | `LLMRequest` messages, system prompt, instructions, temperature, and max tokens | Mapped to `core.ChatRequest`; `FeatureChat` is checked before execution |
| Structured output | `ResponseFormat`, `StructuredOutput`, and legacy `JSONSchema` fields | Maps schema name, description, strict mode, and schema bytes; `JSONValue` supports object, array, scalar, and null roots |
| JSON mode | `ResponseFormat: LLMResponseFormatJSON` | Maps to Iris `ResponseFormatJSON` |
| Tool calling | `Tools`, message tool calls, and tool results | Maps definitions and preserves call IDs, raw arguments after decoding, result content, and error state |
| Reasoning | `ReasoningEffort` | Maps to Iris reasoning effort and preserves response ID and summary |
| Responses continuation | `PreviousResponseID` and `Truncation` | Maps to Iris response chaining fields |
| Built-in tools | `BuiltInTools` and `ToolResources` | Maps built-in tool types and file-search vector stores |
| Search grounding | `SearchOptions` | Maps domain filters, recency, and search mode |
| Multimodal input | `LLMContentPart` and `LLMArtifactReference` | Maps text, image URL/file ID, file URL/file ID/data, and artifact file references; per-part provider support is checked |
| Response metadata | `LLMResponse.ResponseID`, `Status`, `Citations`, `Reasoning`, `ToolCalls`, and `Meta` | Preserved for synchronous responses and on the terminal streaming chunk |
| Streaming | `StreamingLLMClient` and `StreamChunk` | Text deltas are emitted in order; terminal metadata is equivalent to the synchronous response |
| Speech recognition | Typed `speech_transcribe` node and `nodes.SpeechRecognizer` | The node's typed recognizer port accepts Iris providers that implement `iriscore.SpeechRecognizer` directly; `llmprovider.NewSpeechRecognizer` performs capability discovery at construction, resolves credentials through the standard ProviderMap flow (config file, `PETALFLOW_PROVIDER_*`, `--provider-key`), wraps the provider in an Iris client (rate limiting, retry for in-memory audio, default timeout, telemetry), and reports missing credentials or unsupported capabilities without echoing the API key. The node builds `iriscore.TranscriptionRequest`s, validates responses, and commits only final (`IsFinal`) transcripts |
| Speech synthesis | Typed `speech_synthesize` node and `nodes.SpeechSynthesizer` | Same construction contract as recognition (`llmprovider.NewSpeechSynthesizer`). The node builds `iriscore.SpeechRequest`s, supports both synchronous synthesis and Iris HTTP streaming (`StreamSynthesize` with per-chunk delta events), and commits bounded audio artifacts only. Real-time voice sessions are claimed through the voice boundary below |

Voice sessions are separate from the LLM adapter and one-shot DAG nodes:
`llmprovider.NewVoiceSessionStarter` discovers Iris `voice_session` on the
resolved provider, wraps it in an Iris client, and returns `voice.Session`
handles (session lifecycle, `iriscore.VoiceEvent` envelopes, turn tracking,
interruption propagation) — see `docs/voice-sessions.md`.

`llmprovider` registers both speech factories as hydration defaults
(`hydrate.RegisterDefaultSpeechFactories`) in binaries that link it, so
speech nodes hydrate from the standard provider credential resolution without
explicit per-run wiring; explicit `WithSpeechRecognizerFactory` /
`WithSpeechSynthesizerFactory` options still take precedence. Binaries that
do not link `llmprovider` must supply explicit factories.

The adapter deliberately does not claim support for Iris batch operations,
image generation, real-time voice sessions, embeddings, reranking, or
provider-specific response items that have no representation in the
PetalFlow LLM contract. These operations must use their Iris-native APIs or
a future PetalFlow capability interface. Speech recognition and synthesis
are claimed through the typed `speech_transcribe` / `speech_synthesize` node
ports rather than through `irisadapter`'s LLM contract.
Strict tool schemas are also rejected because Iris 2.0's provider-neutral
`core.Tool` interface has no strict flag; strictness is supported for response
JSON Schemas.

## Structured output

`JSONSchema` remains supported for compatibility. New code should prefer
`StructuredOutput`, which carries the schema name, description, and an
optional strict preference. The adapter defaults an omitted schema name to
`petalflow_output`, and preserves decoded JSON in `JSONValue`. Object results
are also copied to the legacy `JSON` map.

## Capability failures

Capability failures return an `*irisadapter.CapabilityError` and wrap
`irisadapter.ErrUnsupportedCapability`. The error includes the Iris feature,
provider, and model so callers can select a compatible model or remove the
unsupported request option. Validation happens before `Chat` or `StreamChat`
is invoked.
