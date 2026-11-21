// Example: Voice Workflow
//
// This example demonstrates PetalFlow's voice capabilities end to end:
//
//  1. Voice DAG workflow (audio -> transcription -> agent -> speech):
//     a speech_transcribe node turns typed audio input into a committed
//     transcript, an llm_prompt agent node answers it, and a
//     speech_synthesize node renders the reply as a bounded audio artifact.
//     All three nodes hydrate through the default live-node factory —
//     llmprovider registers Iris-backed speech factories at init, so the
//     only wiring is the provider credential.
//
//  2. Simulated live voice session: a full-duplex voice.Session handle with
//     typed event envelopes, turn IDs, and the tool bridge that connects
//     session tool_call events to PetalFlow's tool registry.
//
// Both demos run fully offline against a deterministic simulated Iris
// provider. Set ELEVENLABS_API_KEY to also run a live round trip against
// ElevenLabs (synthesize -> transcribe), which needs network access.
//
// Prerequisites:
// - None for the simulated demos.
// - ELEVENLABS_API_KEY for the optional live round trip.
//
// Run: go run main.go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	iriscore "github.com/petal-labs/iris/v2/core"
	irisproviders "github.com/petal-labs/iris/v2/providers"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/llmprovider"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/voice"
)

// Deterministic fixtures shared by the demos and the CI tests.
const (
	voiceProviderName   = "voice-demo"
	simulatedAgentReply = "PetalFlow heard you: voice workflows run end to end."
	simulatedTranscript = "How do voice workflows work in PetalFlow?"
)

var simulatedAudio = []byte("simulated-user-audio-payload")

// simulatedVoiceProvider is a deterministic in-process Iris provider that
// advertises chat, speech recognition, speech synthesis, and full-duplex
// voice sessions. It replaces the network entirely so the demos run offline.
type simulatedVoiceProvider struct{}

func (simulatedVoiceProvider) ID() string { return voiceProviderName }

func (simulatedVoiceProvider) Models() []iriscore.ModelInfo {
	return []iriscore.ModelInfo{{
		ID: "voice-demo-model",
		Capabilities: []iriscore.Feature{
			iriscore.FeatureChat,
			iriscore.FeatureSpeechRecognition,
			iriscore.FeatureSpeechSynthesis,
			iriscore.FeatureVoiceSession,
		},
	}}
}

func (simulatedVoiceProvider) Supports(f iriscore.Feature) bool {
	return f == iriscore.FeatureChat ||
		f == iriscore.FeatureSpeechRecognition ||
		f == iriscore.FeatureSpeechSynthesis ||
		f == iriscore.FeatureVoiceSession
}

func (simulatedVoiceProvider) Chat(_ context.Context, req *iriscore.ChatRequest) (*iriscore.ChatResponse, error) {
	return &iriscore.ChatResponse{
		ID:     "chat-demo-1",
		Model:  req.Model,
		Output: simulatedAgentReply,
	}, nil
}

func (simulatedVoiceProvider) StreamChat(_ context.Context, req *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	// Deterministic single-delta stream with a terminal final response.
	ch := make(chan iriscore.ChatChunk, 1)
	ch <- iriscore.ChatChunk{Delta: simulatedAgentReply}
	close(ch)
	errCh := make(chan error)
	close(errCh)
	finalCh := make(chan *iriscore.ChatResponse, 1)
	finalCh <- &iriscore.ChatResponse{
		ID:     "chat-demo-stream-1",
		Model:  req.Model,
		Output: simulatedAgentReply,
	}
	close(finalCh)
	return &iriscore.ChatStream{Ch: ch, Err: errCh, Final: finalCh}, nil
}

func (simulatedVoiceProvider) Transcribe(context.Context, *iriscore.TranscriptionRequest) (*iriscore.TranscriptionResponse, error) {
	return &iriscore.TranscriptionResponse{
		ID:       "transcript-demo-1",
		Model:    "voice-demo-model",
		Text:     simulatedTranscript,
		Language: "en",
		Duration: 2 * time.Second,
		IsFinal:  true,
	}, nil
}

func (simulatedVoiceProvider) Synthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechResponse, error) {
	return &iriscore.SpeechResponse{
		ID:      "audio-demo-1",
		Model:   "voice-demo-model",
		VoiceID: "demo-voice",
		Audio: iriscore.AudioData{
			Data:   []byte("simulated-audio-output"),
			Format: iriscore.AudioFormatMP3_44100_128,
		},
		Usage: iriscore.SpeechUsage{
			InputCharacters: len(simulatedAgentReply),
			AudioDuration:   2 * time.Second,
			OutputBytes:     int64(len("simulated-audio-output")),
		},
	}, nil
}

func (simulatedVoiceProvider) StreamSynthesize(context.Context, *iriscore.SpeechRequest) (*iriscore.SpeechStream, error) {
	ch := make(chan iriscore.AudioChunk, 1)
	ch <- iriscore.AudioChunk{Data: []byte("simulated-audio-output"), Format: iriscore.AudioFormatMP3_44100_128}
	close(ch)
	errCh := make(chan error)
	close(errCh)
	finalCh := make(chan *iriscore.SpeechResponse)
	close(finalCh)
	return &iriscore.SpeechStream{Ch: ch, Err: errCh, Final: finalCh}, nil
}

// simulatedVoiceSession is a deterministic full-duplex session: SendText
// immediately emits the agent's committed reply and turn lifecycle events
// (a real provider does this asynchronously over its pipeline), and
// SubmitToolResult records what the tool bridge delivered.
type simulatedVoiceSession struct {
	id          string
	emitter     *iriscore.VoiceEventEmitter
	turns       int
	resultsMu   sync.Mutex
	toolResults []iriscore.ToolResult
}

func (s *simulatedVoiceSession) ID() string { return s.id }

func (s *simulatedVoiceSession) State() iriscore.VoiceSessionState {
	return iriscore.VoiceSessionStateActive
}

func (s *simulatedVoiceSession) SendAudio(context.Context, iriscore.AudioChunk) error {
	return nil
}

func (s *simulatedVoiceSession) SendText(_ context.Context, _ string) error {
	s.turns++
	turnID := fmt.Sprintf("turn-%d", s.turns)
	_ = s.emitter.Emit(context.Background(), iriscore.VoiceEvent{
		Type:   iriscore.VoiceEventTurnStarted,
		TurnID: turnID,
	})
	_ = s.emitter.Emit(context.Background(), iriscore.VoiceEvent{
		Type:    iriscore.VoiceEventAgentTextCommitted,
		TurnID:  turnID,
		Text:    simulatedAgentReply,
		IsFinal: true,
	})
	_ = s.emitter.Emit(context.Background(), iriscore.VoiceEvent{
		Type:   iriscore.VoiceEventTurnCompleted,
		TurnID: turnID,
		Text:   simulatedAgentReply,
	})
	return nil
}

func (s *simulatedVoiceSession) SubmitToolResult(_ context.Context, result iriscore.ToolResult) error {
	s.resultsMu.Lock()
	defer s.resultsMu.Unlock()
	s.toolResults = append(s.toolResults, result)
	return nil
}

func (s *simulatedVoiceSession) Interrupt(context.Context) error { return nil }

func (s *simulatedVoiceSession) Events() <-chan iriscore.VoiceEvent {
	return s.emitter.Events()
}

func (s *simulatedVoiceSession) Err() <-chan error {
	return s.emitter.Err()
}

func (s *simulatedVoiceSession) Close() error {
	s.emitter.Close()
	return nil
}

// lastSimulatedSession points at the most recently started simulated
// session, so the demo can inject a pipeline tool_call event (in production
// the provider's pipeline does this).
var (
	lastSimMu         sync.Mutex
	lastSimulatedSess *simulatedVoiceSession
)

func (simulatedVoiceProvider) StartVoiceSession(_ context.Context, cfg iriscore.VoiceSessionConfig) (iriscore.VoiceSession, error) {
	id := cfg.SessionID
	if id == "" {
		id = "voice-demo-session"
	}
	sess := &simulatedVoiceSession{
		id:      id,
		emitter: iriscore.NewVoiceEventEmitter(id, 0),
	}
	lastSimMu.Lock()
	lastSimulatedSess = sess
	lastSimMu.Unlock()
	return sess, nil
}

func lastSimulatedSession() *simulatedVoiceSession {
	lastSimMu.Lock()
	defer lastSimMu.Unlock()
	return lastSimulatedSess
}

func init() {
	irisproviders.Register(voiceProviderName, func(string) iriscore.Provider { return simulatedVoiceProvider{} })
	irisproviders.Register("chat-only-demo", func(string) iriscore.Provider { return chatOnlyDemoProvider{} })
}

// chatOnlyDemoProvider advertises chat only, so examples can demonstrate the
// capability failure path for speech nodes.
type chatOnlyDemoProvider struct{}

func (chatOnlyDemoProvider) ID() string                   { return "chat-only-demo" }
func (chatOnlyDemoProvider) Models() []iriscore.ModelInfo { return nil }
func (chatOnlyDemoProvider) Supports(f iriscore.Feature) bool {
	return f == iriscore.FeatureChat
}
func (chatOnlyDemoProvider) Chat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatResponse, error) {
	return nil, nil
}
func (chatOnlyDemoProvider) StreamChat(context.Context, *iriscore.ChatRequest) (*iriscore.ChatStream, error) {
	return nil, nil
}

// demoProviders mirrors the CLI/server credential resolution for the
// simulated provider: speech nodes require an API key even when the provider
// is in-process, so the demos exercise the real resolution paths.
func demoProviders() hydrate.ProviderMap {
	return hydrate.ProviderMap{
		voiceProviderName: {
			APIKey: "demo-key-not-a-secret",
			Capabilities: map[string][]string{
				"voice-demo-model": {"chat", "streaming"},
			},
		},
	}
}

// demoFactory builds the default live-node factory: llmprovider-backed LLM
// clients plus the llmprovider-registered default speech factories, so
// speech nodes hydrate without any explicit wiring.
func demoFactory(providers hydrate.ProviderMap) hydrate.NodeFactory {
	return hydrate.NewLiveNodeFactory(providers, func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
		return llmprovider.NewClient(name, cfg)
	})
}

// hydrateNodeDefs hydrates a chain of node definitions and wires them in
// order into a graph.
func hydrateNodeDefs(name string, factory hydrate.NodeFactory, defs []graph.NodeDef) (graph.Graph, error) {
	builder := graph.NewGraphBuilder(name)
	var previous string
	for _, def := range defs {
		node, err := factory(def)
		if err != nil {
			return nil, fmt.Errorf("hydrating %s: %w", def.ID, err)
		}
		builder.AddNode(node)
		if previous != "" {
			builder.Connect(previous, def.ID)
		}
		previous = def.ID
	}
	return builder.Build()
}

// voiceDAGDefs is the audio -> agent -> speech pipeline definition.
func voiceDAGDefs() []graph.NodeDef {
	return []graph.NodeDef{
		{
			ID:   "transcribe",
			Type: "speech_transcribe",
			Config: map[string]any{
				"provider":     voiceProviderName,
				"input_var":    "audio",
				"audio_format": "wav",
				"language":     "en",
				"output_var":   "transcript",
			},
		},
		{
			ID:   "agent",
			Type: "llm_prompt",
			Config: map[string]any{
				"provider":        voiceProviderName,
				"model":           "voice-demo-model",
				"system_prompt":   "You are a concise voice assistant.",
				"prompt_template": "The user asked: {{.transcript}}",
				"output_key":      "reply",
			},
		},
		{
			ID:   "speak",
			Type: "speech_synthesize",
			Config: map[string]any{
				"provider":   voiceProviderName,
				"voice_id":   "demo-voice",
				"input_var":  "reply",
				"output_var": "audio_id",
			},
		},
	}
}

// VoiceWorkflowResult carries the committed outputs of the DAG demo.
type VoiceWorkflowResult struct {
	Transcript string
	Reply      string
	AudioBytes int
}

// runVoiceWorkflowDemo runs the one-shot DAG:
// audio -> speech_transcribe -> llm_prompt (agent) -> speech_synthesize.
func runVoiceWorkflowDemo() (VoiceWorkflowResult, error) {
	providers := demoProviders()
	g, err := hydrateNodeDefs("voice-workflow-demo", demoFactory(providers), voiceDAGDefs())
	if err != nil {
		return VoiceWorkflowResult{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := core.NewEnvelope().WithVar("audio", simulatedAudio)
	result, err := runtime.NewRuntime().Run(ctx, g, env, runtime.DefaultRunOptions())
	if err != nil {
		return VoiceWorkflowResult{}, fmt.Errorf("run: %w", err)
	}

	out := VoiceWorkflowResult{
		Transcript: result.GetVarString("transcript"),
		Reply:      result.GetVarString("reply"),
	}
	if artifacts := result.GetArtifactsByType("audio"); len(artifacts) == 1 {
		out.AudioBytes = len(artifacts[0].Bytes)
	}

	fmt.Printf("  transcript:     %q\n", out.Transcript)
	fmt.Printf("  agent reply:    %q\n", out.Reply)
	fmt.Printf("  audio artifact: %d bytes\n", out.AudioBytes)

	return out, nil
}

// VoiceSessionResult carries the observed outputs of the live-voice demo.
type VoiceSessionResult struct {
	SessionID    string
	TurnID       string
	AgentText    string
	ToolExecuted bool
	ToolResults  []iriscore.ToolResult
}

// runVoiceSessionDemo runs the simulated live voice workflow: a full-duplex
// session with turn lifecycle, typed event envelopes, and the tool bridge.
func runVoiceSessionDemo() (VoiceSessionResult, error) {
	providers := demoProviders()
	starter, err := llmprovider.NewVoiceSessionStarter(voiceProviderName, providers[voiceProviderName])
	if err != nil {
		return VoiceSessionResult{}, fmt.Errorf("creating voice session starter: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	session, err := starter.StartVoiceSession(ctx, iriscore.VoiceSessionConfig{
		SessionID: "voice-demo-session",
		Language:  "en",
	})
	if err != nil {
		return VoiceSessionResult{}, fmt.Errorf("StartVoiceSession: %w", err)
	}
	defer func() { _ = session.Close() }()

	// The tool bridge connects session tool_call events to PetalFlow's
	// tool registry with safe semantics (allowlist, bounds, policy gate).
	toolRegistry := core.NewToolRegistry()
	toolRegistry.Register(core.NewFuncTool("lookup_status", "look up a system status",
		func(_ context.Context, args map[string]any) (map[string]any, error) {
			return map[string]any{"status": "healthy", "queried": args["system"]}, nil
		}))
	bridge, err := voice.NewToolBridge(session, voice.ToolBridgeConfig{Registry: toolRegistry})
	if err != nil {
		return VoiceSessionResult{}, fmt.Errorf("NewToolBridge: %w", err)
	}
	bridgeDone := make(chan error, 1)
	go func() { bridgeDone <- bridge.Serve(ctx) }()

	out := VoiceSessionResult{SessionID: session.ID()}

	// Turn lifecycle: start a turn, send the user utterance, and read the
	// simulated pipeline's typed event envelopes.
	turnID, turnCtx := session.StartTurn()
	if err := session.SendText(ctx, "What is PetalFlow?"); err != nil {
		return VoiceSessionResult{}, fmt.Errorf("SendText: %w", err)
	}

	events := session.Events()
	deadline := time.After(10 * time.Second)
	for turnOpen := true; turnOpen; {
		select {
		case evt := <-events:
			switch evt.Type {
			case iriscore.VoiceEventAgentTextCommitted:
				out.AgentText = evt.Text
				fmt.Printf("  agent: %q (turn %s)\n", evt.Text, evt.TurnID)
			case iriscore.VoiceEventTurnCompleted:
				if evt.TurnID == turnID && evt.Text == simulatedAgentReply {
					turnOpen = false
				}
			}
		case <-deadline:
			return VoiceSessionResult{}, errors.New("timed out waiting for session events")
		}
	}
	// Fold the provider's turn completion back into the handle's turn
	// tracker, then release the turn.
	session.ObserveEvent(iriscore.VoiceEvent{Type: iriscore.VoiceEventTurnCompleted, TurnID: turnID})
	session.CompleteTurn(turnID)
	select {
	case <-turnCtx.Done():
	default:
		return VoiceSessionResult{}, errors.New("turn context was not released on completion")
	}
	out.TurnID = turnID

	// Tool bridge: the simulated pipeline requests a tool; the bridge
	// executes the registered tool and the result flows back into the
	// session with the model's call ID.
	sim := lastSimulatedSession()
	if sim == nil {
		return VoiceSessionResult{}, errors.New("simulated session not found")
	}
	if err := sim.emitter.Emit(ctx, iriscore.VoiceEvent{
		Type: iriscore.VoiceEventToolCall,
		ToolCall: &iriscore.ToolCall{
			ID:        "call-demo-1",
			Name:      "lookup_status",
			Arguments: []byte(`{"system":"voice"}`),
		},
	}); err != nil {
		return VoiceSessionResult{}, fmt.Errorf("emitting tool_call: %w", err)
	}
	toolDeadline := time.After(5 * time.Second)
	for {
		sim.resultsMu.Lock()
		found := len(sim.toolResults) > 0
		out.ToolResults = append([]iriscore.ToolResult(nil), sim.toolResults...)
		sim.resultsMu.Unlock()
		if found {
			break
		}
		select {
		case <-toolDeadline:
			return VoiceSessionResult{}, errors.New("timed out waiting for the tool result")
		case <-time.After(5 * time.Millisecond):
		}
	}
	out.ToolExecuted = true
	fmt.Printf("  tool result: %+v\n", out.ToolResults[0].Content)

	cancel()
	if err := <-bridgeDone; err != nil {
		return VoiceSessionResult{}, fmt.Errorf("tool bridge: %w", err)
	}

	return out, nil
}

// runLiveRoundTripDemo synthesizes speech with ElevenLabs and transcribes it
// back, gated on ELEVENLABS_API_KEY. Requires network access.
func runLiveRoundTripDemo() error {
	apiKey := os.Getenv("ELEVENLABS_API_KEY")
	if apiKey == "" {
		fmt.Println("  ELEVENLABS_API_KEY not set, skipping the live round trip")
		return nil
	}

	providers := hydrate.ProviderMap{"elevenlabs": {APIKey: apiKey}}
	factory := demoFactory(providers)

	speakDef := graph.NodeDef{
		ID:   "speak",
		Type: "speech_synthesize",
		Config: map[string]any{
			"provider":     "elevenlabs",
			"voice_id":     "JBFqnCBsd6RMkjVDRZzb",
			"input_var":    "text",
			"audio_format": "mp3",
			"sample_rate":  22050,
			"bit_rate":     32,
			"output_var":   "audio_id",
		},
	}
	transcribeDef := graph.NodeDef{
		ID:   "transcribe",
		Type: "speech_transcribe",
		Config: map[string]any{
			"provider":     "elevenlabs",
			"model":        "scribe_v1",
			"input_var":    "audio_artifact",
			"audio_format": "mp3",
			"output_var":   "transcript",
		},
	}

	rt := runtime.NewRuntime()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Synthesize first; the audio artifact carries inline bytes, so the
	// transcribe node can consume the produced audio directly.
	speakGraph, err := hydrateNodeDefs("voice-live-speak", factory, []graph.NodeDef{speakDef})
	if err != nil {
		return err
	}
	spoke, err := rt.Run(ctx, speakGraph, core.NewEnvelope().WithVar("text", "Hello from PetalFlow voice workflows."), runtime.DefaultRunOptions())
	if err != nil {
		return fmt.Errorf("live synthesize: %w", err)
	}
	artifacts := spoke.GetArtifactsByType("audio")
	if len(artifacts) != 1 {
		return fmt.Errorf("live synthesize produced %d audio artifacts, want 1", len(artifacts))
	}

	env := core.NewEnvelope().WithVar("audio_artifact", artifacts[0])
	transGraph, err := hydrateNodeDefs("voice-live-transcribe", factory, []graph.NodeDef{transcribeDef})
	if err != nil {
		return err
	}
	heard, err := rt.Run(ctx, transGraph, env, runtime.DefaultRunOptions())
	if err != nil {
		return fmt.Errorf("live transcribe: %w", err)
	}
	transcript := heard.GetVarString("transcript")
	fmt.Printf("  round-trip transcript: %q\n", transcript)
	if strings.TrimSpace(transcript) == "" {
		return errors.New("live round trip returned an empty transcript")
	}
	return nil
}

func main() {
	fmt.Println("=== Voice DAG workflow (audio -> transcript -> agent -> speech) ===")
	if _, err := runVoiceWorkflowDemo(); err != nil {
		fmt.Fprintf(os.Stderr, "voice workflow demo failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n=== Simulated live voice session (turns, events, tool bridge) ===")
	if _, err := runVoiceSessionDemo(); err != nil {
		fmt.Fprintf(os.Stderr, "voice session demo failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n=== Optional live round trip (ElevenLabs) ===")
	if err := runLiveRoundTripDemo(); err != nil {
		fmt.Fprintf(os.Stderr, "live round trip failed: %v\n", err)
		os.Exit(1)
	}
}
