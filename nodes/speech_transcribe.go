package nodes

import (
	"context"
	"fmt"
	"strings"
	"time"

	iriscore "github.com/petal-labs/iris/v2/core"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

// SpeechRecognizer converts audio input into a structured transcript response.
// Implementations adapt provider backends behind the typed node; the Iris v2
// providers that implement iriscore.SpeechRecognizer satisfy this interface
// directly, so PetalFlow never re-implements provider speech logic.
type SpeechRecognizer interface {
	Transcribe(ctx context.Context, req *iriscore.TranscriptionRequest) (*iriscore.TranscriptionResponse, error)
}

// SpeechTranscribeNodeConfig configures a SpeechTranscribeNode.
type SpeechTranscribeNodeConfig struct {
	// Model is the transcription model identifier (e.g., "scribe_v1").
	// Empty uses the provider default.
	Model string

	// InputVar is the envelope variable holding the typed audio input.
	// When empty, the envelope's Input field is used.
	InputVar string

	// AudioFormat is an encoding hint applied when the typed audio input
	// carries no format of its own (e.g., "mp3", "wav", "pcm").
	AudioFormat string

	// Filename is an optional filename hint for multipart uploads.
	Filename string

	// Language is an optional ISO-639 / BCP-47 language code hint.
	// Empty enables provider auto-detection when supported.
	Language string

	// Prompt supplies optional conditioning context or terminology hints.
	Prompt string

	// Keyterms supplies domain-specific words or proper nouns to bias recognition.
	Keyterms []string

	// Diarize enables speaker diarization when supported by the provider.
	Diarize bool

	// NumSpeakers optionally hints at the expected number of speakers.
	NumSpeakers *int

	// Timestamps requests timestamp granularity: "", "word", "segment", or "character".
	Timestamps string

	// TagAudioEvents enables tagging of non-speech audio events when supported.
	TagAudioEvents bool

	// Temperature optionally controls decoding randomness (>= 0).
	Temperature *float64

	// MaxBytes optionally bounds the accepted audio input size in bytes.
	// Zero disables the limit.
	MaxBytes int64

	// OutputVar is the envelope variable to store the transcript text.
	OutputVar string

	// OmitArtifacts skips committing the transcript artifact to the envelope.
	OmitArtifacts bool

	// Timeout is the maximum time to wait for the transcription response.
	Timeout time.Duration
}

// SpeechTranscribeStats summarizes a transcription for observability.
type SpeechTranscribeStats struct {
	Model               string
	Language            string
	LanguageProbability *float64
	Duration            time.Duration
	Words               int
	Segments            int
	AudioEvents         int
	InputBytes          int
	IsFinal             bool
}

// SpeechTranscribeNode transcribes typed audio input into a committed
// transcript: the transcript text is stored as an envelope variable and the
// full response is committed as a "transcript" artifact.
//
// Failure and cancellation behavior: the node returns the recognizer error
// (or the context error when the run is cancelled) and commits nothing —
// the runtime decides whether to fail the run or continue via
// RunOptions.ContinueOnError. Retries and rate limiting are delegated to
// the recognizer (Iris clients retry in-memory audio internally), so the
// node dispatches each call at most once.
type SpeechTranscribeNode struct {
	core.BaseNode
	config     SpeechTranscribeNodeConfig
	recognizer SpeechRecognizer
}

// NewSpeechTranscribeNode creates a new speech transcription node with the
// given recognizer and configuration.
func NewSpeechTranscribeNode(id string, recognizer SpeechRecognizer, config SpeechTranscribeNodeConfig) *SpeechTranscribeNode {
	if config.OutputVar == "" {
		config.OutputVar = id + "_transcript"
	}
	if config.Timeout == 0 {
		config.Timeout = 120 * time.Second
	}
	return &SpeechTranscribeNode{
		BaseNode:   core.NewBaseNode(id, core.NodeKindSpeechTranscribe),
		config:     config,
		recognizer: recognizer,
	}
}

// Run executes the transcription and stores the committed transcript in the envelope.
func (n *SpeechTranscribeNode) Run(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
	emit := runtime.EmitterFromContext(ctx)

	if n.recognizer == nil {
		return nil, fmt.Errorf("speech_transcribe node %s: no recognizer configured", n.ID())
	}

	if n.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, n.config.Timeout)
		defer cancel()
	}

	audio, err := n.resolveAudio(env)
	if err != nil {
		return nil, err
	}

	req, err := n.buildRequest(env, audio)
	if err != nil {
		return nil, err
	}

	resp, err := n.recognizer.Transcribe(ctx, req)
	if err != nil {
		// Report cancellation as cancellation, not as a provider failure.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("speech_transcribe node %s: transcription failed: %w", n.ID(), err)
	}
	if resp == nil {
		return nil, fmt.Errorf("speech_transcribe node %s: recognizer returned no response", n.ID())
	}
	if err := resp.Validate(); err != nil {
		return nil, fmt.Errorf("speech_transcribe node %s: invalid transcription response: %w", n.ID(), err)
	}
	if !resp.IsFinal {
		return nil, fmt.Errorf("speech_transcribe node %s: transcript is not committed (IsFinal=false); refusing to store a partial transcript", n.ID())
	}

	return n.commit(ctx, env, emit, audio, resp), nil
}

// resolveAudio extracts the typed audio input from the envelope. Accepted
// forms, in preference order: iriscore.AudioData (typed input), *iriscore.AudioData,
// a core.Artifact carrying inline audio bytes, raw []byte, or a base64 string.
func (n *SpeechTranscribeNode) resolveAudio(env *core.Envelope) (*iriscore.AudioData, error) {
	var value any
	if n.config.InputVar != "" {
		v, ok := env.GetVar(n.config.InputVar)
		if !ok {
			return nil, fmt.Errorf("speech_transcribe node %s: audio input variable %q not found", n.ID(), n.config.InputVar)
		}
		value = v
	} else {
		value = env.Input
	}

	switch typed := value.(type) {
	case *iriscore.AudioData:
		if typed == nil {
			return nil, fmt.Errorf("speech_transcribe node %s: audio input is nil", n.ID())
		}
		return typed, nil
	case iriscore.AudioData:
		audio := typed
		return &audio, nil
	case core.Artifact:
		return n.audioFromArtifact(typed)
	case []byte:
		if len(typed) == 0 {
			return nil, fmt.Errorf("speech_transcribe node %s: audio input is empty", n.ID())
		}
		return &iriscore.AudioData{Data: typed, Filename: n.config.Filename}, nil
	case string:
		if typed == "" {
			return nil, fmt.Errorf("speech_transcribe node %s: audio input is empty", n.ID())
		}
		return &iriscore.AudioData{Base64: typed, Filename: n.config.Filename}, nil
	case nil:
		if n.config.InputVar != "" {
			return nil, fmt.Errorf("speech_transcribe node %s: audio input variable %q is nil", n.ID(), n.config.InputVar)
		}
		return nil, fmt.Errorf("speech_transcribe node %s: no audio input (set input_var or envelope input)", n.ID())
	default:
		return nil, fmt.Errorf("speech_transcribe node %s: unsupported audio input type %T (use AudioData, artifact, bytes, or base64 string)", n.ID(), value)
	}
}

func (n *SpeechTranscribeNode) audioFromArtifact(a core.Artifact) (*iriscore.AudioData, error) {
	if len(a.Bytes) == 0 {
		return nil, fmt.Errorf("speech_transcribe node %s: audio artifact %q carries no inline bytes (large payloads are offloaded to storage before the node runs)", n.ID(), a.ID)
	}
	audio := &iriscore.AudioData{
		Data:     a.Bytes,
		Filename: n.config.Filename,
	}
	if n.config.AudioFormat == "" {
		audio.Format = iriscore.AudioFormat{Encoding: audioEncodingFromMIME(a.MimeType)}
	}
	return audio, nil
}

func (n *SpeechTranscribeNode) buildRequest(env *core.Envelope, audio *iriscore.AudioData) (*iriscore.TranscriptionRequest, error) {
	if n.config.AudioFormat != "" && audio.Format.Encoding == "" {
		audio.Format = iriscore.AudioFormat{Encoding: iriscore.AudioEncoding(n.config.AudioFormat)}
	}
	if audio.Format.Encoding == "" {
		return nil, fmt.Errorf("speech_transcribe node %s: audio encoding missing: set config.audio_format or supply typed AudioData with a format", n.ID())
	}
	if n.config.Filename != "" {
		audio.Filename = n.config.Filename
	}

	req := &iriscore.TranscriptionRequest{
		Model:          iriscore.ModelID(n.config.Model),
		Audio:          audio,
		Filename:       n.config.Filename,
		Language:       n.config.Language,
		Prompt:         n.config.Prompt,
		Keyterms:       n.config.Keyterms,
		Diarize:        n.config.Diarize,
		NumSpeakers:    n.config.NumSpeakers,
		Timestamps:     iriscore.TranscriptionTimestampGranularity(n.config.Timestamps),
		TagAudioEvents: n.config.TagAudioEvents,
		Temperature:    n.config.Temperature,
		MaxBytes:       n.config.MaxBytes,
		Metadata: map[string]string{
			"node_id": n.ID(),
			"run_id":  env.Trace.RunID,
		},
	}
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("speech_transcribe node %s: invalid transcription request: %w", n.ID(), err)
	}
	return req, nil
}

// commit stores the transcript, stats, and artifact on a cloned envelope and
// emits the node.output.final event. Transcript text is content-classified in
// events: shape descriptor by default, bounded redacted text when capture is
// opted in.
func (n *SpeechTranscribeNode) commit(ctx context.Context, env *core.Envelope, emit runtime.EventEmitter, audio *iriscore.AudioData, resp *iriscore.TranscriptionResponse) *core.Envelope {
	inputBytes := len(audio.Data)
	if inputBytes == 0 {
		if decoded, err := audio.GetBytes(); err == nil {
			inputBytes = len(decoded)
		}
	}

	stats := SpeechTranscribeStats{
		Model:               string(resp.Model),
		Language:            resp.Language,
		LanguageProbability: resp.LanguageProbability,
		Duration:            resp.Duration,
		Words:               len(resp.Words),
		Segments:            len(resp.Segments),
		AudioEvents:         len(resp.AudioEvents),
		InputBytes:          inputBytes,
		IsFinal:             resp.IsFinal,
	}

	resultEnv := env.Clone()
	resultEnv.SetVar(n.config.OutputVar, resp.Text)
	resultEnv.SetVar(n.config.OutputVar+"_stats", stats)
	if !n.config.OmitArtifacts {
		resultEnv.AppendArtifact(n.transcriptArtifact(resp))
	}

	capture, _ := runtime.ContentCaptureFromContext(ctx)
	emit(runtime.NewEvent(runtime.EventNodeOutputFinal, env.Trace.RunID).
		WithNode(n.ID(), n.Kind()).
		WithContentPayload("text", resp.Text, capture).
		WithPayload("model", stats.Model).
		WithPayload("language", stats.Language).
		WithPayload("is_final", stats.IsFinal).
		WithPayload("words", stats.Words).
		WithPayload("segments", stats.Segments).
		WithPayload("input_bytes", stats.InputBytes))

	return resultEnv
}

// transcriptArtifact commits the transcript as a durable envelope artifact.
// The artifact carries the transcript text plus response metadata so
// downstream nodes and the runtime's artifact offload can persist it.
func (n *SpeechTranscribeNode) transcriptArtifact(resp *iriscore.TranscriptionResponse) core.Artifact {
	id := resp.ID
	if id == "" {
		id = fmt.Sprintf("%s_%d", n.ID(), time.Now().UnixNano())
	}
	return core.Artifact{
		ID:       id,
		Type:     "transcript",
		MimeType: "text/plain",
		Text:     resp.Text,
		Meta: map[string]any{
			"model":                string(resp.Model),
			"language":             resp.Language,
			"language_probability": resp.LanguageProbability,
			"duration_ns":          int64(resp.Duration),
			"is_final":             resp.IsFinal,
			"words":                len(resp.Words),
			"segments":             len(resp.Segments),
			"audio_events":         len(resp.AudioEvents),
			"input_bytes":          resp.Usage.InputBytes,
			"node_id":              n.ID(),
		},
	}
}

// audioEncodingFromMIME maps common audio MIME types to Iris encodings.
// Unrecognized MIME types return the zero encoding (provider default).
func audioEncodingFromMIME(mimeType string) iriscore.AudioEncoding {
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0])) {
	case "audio/mpeg", "audio/mp3":
		return iriscore.AudioEncodingMP3
	case "audio/wav", "audio/wave", "audio/x-wav":
		return iriscore.AudioEncodingWAV
	case "audio/pcm":
		return iriscore.AudioEncodingPCM
	case "audio/ogg", "application/ogg":
		return iriscore.AudioEncodingOGG
	case "audio/opus":
		return iriscore.AudioEncodingOpus
	case "audio/flac":
		return iriscore.AudioEncodingFLAC
	case "audio/aac":
		return iriscore.AudioEncodingAAC
	case "audio/webm":
		return iriscore.AudioEncodingWebM
	case "audio/basic", "audio/x-mulaw", "audio/mulaw":
		return iriscore.AudioEncodingULaw
	case "audio/alaw", "audio/PCMA":
		return iriscore.AudioEncodingALaw
	default:
		return ""
	}
}

// speechConfigFloat64 extracts a float64 from graph config (JSON numbers are
// float64).
func speechConfigFloat64(m map[string]any, key string) (float64, bool) {
	v, ok := m[key].(float64)
	return v, ok
}

// speechConfigInt extracts an int from graph config, handling JSON float64 →
// int coercion and programmatic int literals.
func speechConfigInt(m map[string]any, key string) (int, bool) {
	switch v := m[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

// ParseSpeechTranscribeConfig builds and validates a SpeechTranscribeNodeConfig
// from graph config. Invalid values fail hydration instead of failing at run time.
func ParseSpeechTranscribeConfig(m map[string]any) (SpeechTranscribeNodeConfig, error) {
	cfg := SpeechTranscribeNodeConfig{
		Model:       webhookConfigString(m, "model"),
		InputVar:    webhookConfigString(m, "input_var"),
		AudioFormat: webhookConfigString(m, "audio_format"),
		Filename:    webhookConfigString(m, "filename"),
		Language:    webhookConfigString(m, "language"),
		Prompt:      webhookConfigString(m, "prompt"),
		Diarize:     webhookConfigBool(m, "diarize"),
		Timestamps:  webhookConfigString(m, "timestamps"),
		OutputVar:   webhookConfigString(m, "output_var"),
		MaxBytes:    webhookConfigInt64(m, "max_bytes"),
	}
	if keyterms, ok := webhookConfigStringSlice(m, "keyterms"); ok {
		cfg.Keyterms = keyterms
	}
	if v, ok := speechConfigFloat64(m, "temperature"); ok {
		cfg.Temperature = &v
	}
	if v, ok := speechConfigInt(m, "num_speakers"); ok {
		cfg.NumSpeakers = &v
	}
	cfg.TagAudioEvents = webhookConfigBool(m, "tag_audio_events")
	cfg.OmitArtifacts = webhookConfigBool(m, "omit_artifacts")
	cfg.Timeout = webhookConfigDuration(m, "timeout")

	if cfg.AudioFormat != "" && !iriscore.AudioEncoding(cfg.AudioFormat).IsValid() {
		return cfg, fmt.Errorf("unsupported audio_format %q", cfg.AudioFormat)
	}
	if cfg.Timestamps != "" && !iriscore.TranscriptionTimestampGranularity(cfg.Timestamps).IsValid() {
		return cfg, fmt.Errorf("unsupported timestamps granularity %q", cfg.Timestamps)
	}
	if cfg.NumSpeakers != nil && *cfg.NumSpeakers <= 0 {
		return cfg, fmt.Errorf("num_speakers must be > 0, got %d", *cfg.NumSpeakers)
	}
	if cfg.Temperature != nil && *cfg.Temperature < 0 {
		return cfg, fmt.Errorf("temperature must be >= 0, got %v", *cfg.Temperature)
	}
	if cfg.MaxBytes < 0 {
		return cfg, fmt.Errorf("max_bytes must be non-negative, got %d", cfg.MaxBytes)
	}
	for i, term := range cfg.Keyterms {
		if strings.TrimSpace(term) == "" {
			return cfg, fmt.Errorf("keyterms[%d] must not be empty", i)
		}
	}
	return cfg, nil
}

// Config returns the node's configuration.
func (n *SpeechTranscribeNode) Config() SpeechTranscribeNodeConfig {
	return n.config
}

// Ensure interface compliance at compile time.
var (
	_ core.Node        = (*SpeechTranscribeNode)(nil)
	_ SpeechRecognizer = iriscore.SpeechRecognizer(nil)
)
