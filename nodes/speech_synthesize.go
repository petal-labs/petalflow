package nodes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	iriscore "github.com/petal-labs/iris/v2/core"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

// SpeechSynthesizer converts text into synthesized audio, either as a complete
// response or as an incremental audio stream. Implementations adapt provider
// backends behind the typed node; the Iris v2 providers that implement
// iriscore.SpeechSynthesizer satisfy this interface directly, so PetalFlow
// never re-implements provider speech logic.
type SpeechSynthesizer interface {
	Synthesize(ctx context.Context, req *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error)
	StreamSynthesize(ctx context.Context, req *iriscore.SpeechRequest) (*iriscore.SpeechStream, error)
}

// defaultSpeechMaxAudioBytes caps the audio a speech_synthesize node commits
// to the envelope when no limit is configured, so envelopes are never
// unbounded (large payloads are handled by the runtime's artifact offload
// once committed within this bound).
const defaultSpeechMaxAudioBytes int64 = 25 << 20 // 25 MiB

// SpeechSynthesizeNodeConfig configures a SpeechSynthesizeNode.
type SpeechSynthesizeNodeConfig struct {
	// Model is the synthesis model identifier (e.g., "eleven_flash_v2_5").
	// Empty uses the provider default.
	Model string

	// VoiceID identifies the voice to synthesize with. Required.
	VoiceID string

	// InputVar is the envelope variable holding the text to synthesize.
	// When empty, the envelope's Input field is used.
	InputVar string

	// AudioFormat is the output encoding hint (e.g., "mp3", "wav", "pcm").
	// Empty uses the provider default.
	AudioFormat string

	// SampleRate is the optional output sample rate in Hz (0 = provider default).
	SampleRate int

	// BitRate is the optional output bitrate in kbps (0 = provider default).
	BitRate int

	// Language is an optional ISO-639 / BCP-47 language code hint.
	Language string

	// Seed requests deterministic synthesis when supported by the provider.
	Seed *int64

	// Stream selects incremental synthesis: audio chunks are emitted as
	// node.output.delta events and assembled into the committed artifact.
	Stream bool

	// MaxBytes bounds the audio committed to the envelope in bytes.
	// Audio beyond the bound fails the node rather than truncating.
	// Zero applies the default bound (25 MiB).
	MaxBytes int64

	// OutputVar is the envelope variable to store the committed audio
	// artifact ID. Raw audio bytes are never stored in envelope vars.
	OutputVar string

	// OmitArtifacts skips committing the audio artifact to the envelope.
	OmitArtifacts bool

	// Timeout is the maximum time to wait for the synthesis to complete.
	Timeout time.Duration
}

// SpeechSynthesizeStats summarizes a synthesis for observability.
type SpeechSynthesizeStats struct {
	Model           string
	VoiceID         string
	AudioBytes      int
	AudioDuration   time.Duration
	InputCharacters int
	Streamed        bool
}

// SpeechSynthesizeNode synthesizes speech from envelope text and commits the
// audio as a bounded "audio" artifact. Streaming mode emits one
// node.output.delta event per audio chunk.
//
// Failure and cancellation behavior: the node returns the synthesizer error
// (or the context error when the run is cancelled) and commits nothing — the
// runtime decides whether to fail the run or continue via
// RunOptions.ContinueOnError. No partial audio is committed on cancellation,
// and audio beyond MaxBytes fails the node instead of truncating. Retries and
// rate limiting are delegated to the synthesizer's Iris client, so the node
// dispatches each call at most once.
type SpeechSynthesizeNode struct {
	core.BaseNode
	config      SpeechSynthesizeNodeConfig
	synthesizer SpeechSynthesizer
}

// NewSpeechSynthesizeNode creates a new speech synthesis node with the given
// synthesizer and configuration.
func NewSpeechSynthesizeNode(id string, synthesizer SpeechSynthesizer, config SpeechSynthesizeNodeConfig) *SpeechSynthesizeNode {
	if config.OutputVar == "" {
		config.OutputVar = id + "_audio"
	}
	if config.Timeout == 0 {
		config.Timeout = 120 * time.Second
	}
	if config.MaxBytes == 0 {
		config.MaxBytes = defaultSpeechMaxAudioBytes
	}
	return &SpeechSynthesizeNode{
		BaseNode:    core.NewBaseNode(id, core.NodeKindSpeechSynthesize),
		config:      config,
		synthesizer: synthesizer,
	}
}

// Run executes the synthesis and commits the bounded audio artifact.
func (n *SpeechSynthesizeNode) Run(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	emit := runtime.EmitterFromContext(ctx)

	if n.synthesizer == nil {
		return nil, fmt.Errorf("speech_synthesize node %s: no synthesizer configured", n.ID())
	}

	if n.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, n.config.Timeout)
		defer cancel()
	}

	text, err := n.resolveText(env)
	if err != nil {
		return nil, err
	}

	req, err := n.buildRequest(env, text)
	if err != nil {
		return nil, err
	}

	var resp *iriscore.SpeechResponse
	if n.config.Stream {
		resp, err = n.streamSynthesize(ctx, env, emit, req)
	} else {
		resp, err = n.synthesizer.Synthesize(ctx, req)
	}
	if err != nil {
		// Report cancellation as cancellation, not as a provider failure.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("speech_synthesize node %s: synthesis failed: %w", n.ID(), err)
	}
	if resp == nil {
		return nil, fmt.Errorf("speech_synthesize node %s: synthesizer returned no response", n.ID())
	}
	if err := n.validateAudio(resp); err != nil {
		return nil, err
	}

	return n.commit(ctx, env, emit, text, resp), nil
}

// resolveText extracts the text to synthesize from the envelope. Only string
// inputs are accepted so envelope text stays predictable.
func (n *SpeechSynthesizeNode) resolveText(env *core.Envelope) (string, error) {
	var value any
	if n.config.InputVar != "" {
		v, ok := env.GetVar(n.config.InputVar)
		if !ok {
			return "", fmt.Errorf("speech_synthesize node %s: input variable %q not found", n.ID(), n.config.InputVar)
		}
		value = v
	} else {
		value = env.Input
	}

	switch typed := value.(type) {
	case string:
		if typed == "" {
			return "", fmt.Errorf("speech_synthesize node %s: input text is empty", n.ID())
		}
		return typed, nil
	case []byte:
		if len(typed) == 0 {
			return "", fmt.Errorf("speech_synthesize node %s: input text is empty", n.ID())
		}
		return string(typed), nil
	case nil:
		if n.config.InputVar != "" {
			return "", fmt.Errorf("speech_synthesize node %s: input variable %q is nil", n.ID(), n.config.InputVar)
		}
		return "", fmt.Errorf("speech_synthesize node %s: no input text (set input_var or envelope input)", n.ID())
	default:
		return "", fmt.Errorf("speech_synthesize node %s: unsupported input text type %T (use a string)", n.ID(), value)
	}
}

func (n *SpeechSynthesizeNode) buildRequest(env *core.Envelope, text string) (*iriscore.SpeechRequest, error) {
	req := &iriscore.SpeechRequest{
		Model:    iriscore.ModelID(n.config.Model),
		VoiceID:  n.config.VoiceID,
		Text:     text,
		Language: n.config.Language,
		Seed:     n.config.Seed,
		Metadata: map[string]string{
			"node_id": n.ID(),
			"run_id":  env.Trace.RunID,
		},
	}
	if n.config.AudioFormat != "" || n.config.SampleRate > 0 || n.config.BitRate > 0 {
		req.Format = iriscore.AudioFormat{
			Encoding:   iriscore.AudioEncoding(n.config.AudioFormat),
			SampleRate: n.config.SampleRate,
			BitRate:    n.config.BitRate,
		}
	}
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("speech_synthesize node %s: invalid speech request: %w", n.ID(), err)
	}
	return req, nil
}

// streamSynthesize consumes an incremental audio stream: chunks are bounded,
// emitted as delta events, and assembled into a single SpeechResponse.
// Cancellation and stream errors abandon the buffer immediately.
func (n *SpeechSynthesizeNode) streamSynthesize(ctx context.Context, env *core.Envelope, emit runtime.EventEmitter, req *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error) {
	stream, err := n.synthesizer.StreamSynthesize(ctx, req)
	if err != nil {
		return nil, err
	}
	if stream == nil {
		return nil, errors.New("synthesizer returned nil stream")
	}

	var buf bytes.Buffer
	var firstFormat iriscore.AudioFormat
	var streamErr error
	var finalResp *iriscore.SpeechResponse

	chClosed := stream.Ch == nil
	errResolved := stream.Err == nil
	finalResolved := stream.Final == nil
	index := 0

	for !chClosed || !errResolved || !finalResolved {
		var chRecv <-chan iriscore.AudioChunk
		if !chClosed {
			chRecv = stream.Ch
		}
		var errRecv <-chan error
		if !errResolved {
			errRecv = stream.Err
		}
		var finalRecv <-chan *iriscore.SpeechResponse
		if !finalResolved {
			finalRecv = stream.Final
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case chunk, ok := <-chRecv:
			if !ok {
				chClosed = true
				continue
			}
			if firstFormat.IsZero() && !chunk.Format.IsZero() {
				firstFormat = chunk.Format
			}
			if len(chunk.Data) == 0 {
				continue
			}
			if int64(buf.Len()+len(chunk.Data)) > n.config.MaxBytes {
				return nil, fmt.Errorf("speech_synthesize node %s: streamed audio exceeds max_bytes %d; committing nothing", n.ID(), n.config.MaxBytes)
			}
			buf.Write(chunk.Data)
			emit(runtime.NewEvent(runtime.EventNodeOutputDelta, env.Trace.RunID).
				WithNode(n.ID(), n.Kind()).
				WithPayload("index", index).
				WithPayload("bytes", len(chunk.Data)))
			index++

		case err, ok := <-errRecv:
			errResolved = true
			if ok && err != nil {
				streamErr = err
			}

		case resp, ok := <-finalRecv:
			finalResolved = true
			if ok {
				finalResp = resp
			}
		}
	}

	if streamErr != nil {
		return nil, streamErr
	}

	if finalResp == nil {
		finalResp = &iriscore.SpeechResponse{}
	}
	if finalResp.Audio.IsEmpty() && buf.Len() > 0 {
		finalResp.Audio.Data = buf.Bytes()
	}
	if finalResp.Audio.Format.IsZero() && !firstFormat.IsZero() {
		finalResp.Audio.Format = firstFormat
	}
	if finalResp.Usage.OutputBytes == 0 && buf.Len() > 0 {
		finalResp.Usage.OutputBytes = int64(buf.Len())
	}
	return finalResp, nil
}

// validateAudio rejects empty or oversized audio. Oversized audio fails the
// node rather than truncating, so envelopes never carry unbounded payloads.
func (n *SpeechSynthesizeNode) validateAudio(resp *iriscore.SpeechResponse) error {
	if err := resp.Audio.Validate(); err != nil {
		return fmt.Errorf("speech_synthesize node %s: invalid audio response: %w", n.ID(), err)
	}
	data, err := resp.Audio.GetBytes()
	if err != nil {
		return fmt.Errorf("speech_synthesize node %s: %w", n.ID(), err)
	}
	if n.config.MaxBytes > 0 && int64(len(data)) > n.config.MaxBytes {
		return fmt.Errorf("speech_synthesize node %s: audio size %d exceeds max_bytes %d", n.ID(), len(data), n.config.MaxBytes)
	}
	return nil
}

// commit stores the audio artifact (bytes never in vars), stats, and the
// audio ID reference on a cloned envelope, then emits the node.output.final
// event with audio metadata only — raw audio and synthesis text are never
// placed in event payloads.
func (n *SpeechSynthesizeNode) commit(ctx context.Context, env *core.Envelope, emit runtime.EventEmitter, text string, resp *iriscore.SpeechResponse) *core.Envelope {
	data, _ := resp.Audio.GetBytes()
	mimeType := resp.Audio.Format.EffectiveMIMEType()
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	stats := SpeechSynthesizeStats{
		Model:           string(resp.Model),
		VoiceID:         resp.VoiceID,
		AudioBytes:      len(data),
		AudioDuration:   resp.Usage.AudioDuration,
		InputCharacters: resp.Usage.InputCharacters,
		Streamed:        n.config.Stream,
	}
	if resp.Model == "" {
		stats.Model = n.config.Model
	}

	resultEnv := env.Clone()
	artifact := core.Artifact{
		ID:       n.artifactID(resp),
		Type:     "audio",
		MimeType: mimeType,
		Bytes:    data,
		Meta: map[string]any{
			"model":            stats.Model,
			"voice_id":         resp.VoiceID,
			"format":           resp.Audio.Format.String(),
			"duration_ns":      int64(resp.Usage.AudioDuration),
			"output_bytes":     resp.Usage.OutputBytes,
			"input_characters": resp.Usage.InputCharacters,
			"text_length":      len(text),
			"streamed":         n.config.Stream,
			"node_id":          n.ID(),
		},
	}
	if !n.config.OmitArtifacts {
		resultEnv.AppendArtifact(artifact)
	}
	resultEnv.SetVar(n.config.OutputVar, artifact.ID)
	resultEnv.SetVar(n.config.OutputVar+"_stats", stats)

	emit(runtime.NewEvent(runtime.EventNodeOutputFinal, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithPayload("artifact_id", artifact.ID).
		WithPayload("audio_bytes", stats.AudioBytes).
		WithPayload("model", stats.Model).
		WithPayload("voice_id", stats.VoiceID).
		WithPayload("streamed", stats.Streamed).
		WithPayload("audio_duration_ms", resp.Usage.AudioDuration.Milliseconds()))

	return resultEnv
}

func (n *SpeechSynthesizeNode) artifactID(resp *iriscore.SpeechResponse) string {
	if resp.ID != "" {
		return resp.ID
	}
	return fmt.Sprintf("%s_%d", n.ID(), time.Now().UnixNano())
}

// ParseSpeechSynthesizeConfig builds and validates a SpeechSynthesizeNodeConfig
// from graph config. Invalid values fail hydration instead of failing at run time.
func ParseSpeechSynthesizeConfig(m map[string]any) (SpeechSynthesizeNodeConfig, error) {
	cfg := SpeechSynthesizeNodeConfig{
		Model:       webhookConfigString(m, "model"),
		VoiceID:     webhookConfigString(m, "voice_id"),
		InputVar:    webhookConfigString(m, "input_var"),
		AudioFormat: webhookConfigString(m, "audio_format"),
		SampleRate:  speechConfigIntOr(m, "sample_rate"),
		BitRate:     speechConfigIntOr(m, "bit_rate"),
		Language:    webhookConfigString(m, "language"),
		Stream:      webhookConfigBool(m, "stream"),
		OutputVar:   webhookConfigString(m, "output_var"),
		MaxBytes:    webhookConfigInt64(m, "max_bytes"),
	}
	if raw, ok := m["seed"]; ok && raw != nil {
		seed := webhookConfigInt64(m, "seed")
		cfg.Seed = &seed
	}
	cfg.OmitArtifacts = webhookConfigBool(m, "omit_artifacts")
	cfg.Timeout = webhookConfigDuration(m, "timeout")

	if cfg.AudioFormat != "" && !iriscore.AudioEncoding(cfg.AudioFormat).IsValid() {
		return cfg, fmt.Errorf("unsupported audio_format %q", cfg.AudioFormat)
	}
	if cfg.SampleRate < 0 || cfg.BitRate < 0 {
		return cfg, fmt.Errorf("sample_rate and bit_rate must be non-negative")
	}
	if cfg.VoiceID == "" {
		return cfg, fmt.Errorf("voice_id is required")
	}
	return cfg, nil
}

// speechConfigIntOr extracts an int from graph config, defaulting to 0.
func speechConfigIntOr(m map[string]any, key string) int {
	v, _ := speechConfigInt(m, key)
	return v
}

// Config returns the node's configuration.
func (n *SpeechSynthesizeNode) Config() SpeechSynthesizeNodeConfig {
	return n.config
}

// Ensure interface compliance at compile time; the typed port stays in sync
// with the Iris speech synthesis contract.
var (
	_ core.Node         = (*SpeechSynthesizeNode)(nil)
	_ SpeechSynthesizer = iriscore.SpeechSynthesizer(nil)
)
