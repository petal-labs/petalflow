package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/registry"
	"github.com/petal-labs/petalflow/runtime"
)

// The tests in this file run the example's demos deterministically against
// the in-process simulated provider (registered in main.go's init). CI runs
// them via `go test ./...` in the examples module; no network or API keys
// are needed. The live ElevenLabs round trip is exercised in
// TestLiveRoundTripDemo only when ELEVENLABS_API_KEY is set.

// osReadGraphDefinition loads the shipped CLI graph definition.
func osReadGraphDefinition() (*graph.GraphDefinition, error) {
	data, err := os.ReadFile("voice_workflow.graph.json")
	if err != nil {
		return nil, err
	}
	var def graph.GraphDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, err
	}
	return &def, nil
}

func TestVoiceWorkflowDemo(t *testing.T) {
	result, err := runVoiceWorkflowDemo()
	if err != nil {
		t.Fatalf("runVoiceWorkflowDemo: %v", err)
	}
	if result.Transcript != simulatedTranscript {
		t.Errorf("transcript = %q, want %q", result.Transcript, simulatedTranscript)
	}
	if result.Reply != simulatedAgentReply {
		t.Errorf("reply = %q, want %q", result.Reply, simulatedAgentReply)
	}
	if result.AudioBytes != len("simulated-audio-output") {
		t.Errorf("audio bytes = %d, want %d", result.AudioBytes, len("simulated-audio-output"))
	}
}

func TestVoiceSessionDemo(t *testing.T) {
	result, err := runVoiceSessionDemo()
	if err != nil {
		t.Fatalf("runVoiceSessionDemo: %v", err)
	}
	if result.SessionID != "voice-demo-session" {
		t.Errorf("session ID = %q", result.SessionID)
	}
	if result.TurnID != "turn-1" {
		t.Errorf("turn ID = %q", result.TurnID)
	}
	if result.AgentText != simulatedAgentReply {
		t.Errorf("agent text = %q", result.AgentText)
	}
	if !result.ToolExecuted {
		t.Error("tool bridge did not execute the registered tool")
	}
	if len(result.ToolResults) == 0 || result.ToolResults[0].IsError {
		t.Fatalf("tool results = %+v", result.ToolResults)
	}
	content, ok := result.ToolResults[0].Content.(map[string]any)
	if !ok || content["status"] != "healthy" {
		t.Fatalf("tool result content = %+v", result.ToolResults[0].Content)
	}
}

// TestVoiceWorkflowGraphDefinitionValid validates the shipped graph JSON so
// CLI users (`petalflow validate`) get the same graph as the Go demo.
func TestVoiceWorkflowGraphDefinitionValid(t *testing.T) {
	definition, err := osReadGraphDefinition()
	if err != nil {
		t.Fatalf("reading graph definition: %v", err)
	}
	if diags := definition.ValidateWithRegistry(registry.Global()); graph.HasErrors(diags) {
		t.Fatalf("invalid definition: %v", graph.Errors(diags))
	}
}

// TestVoiceWorkflowFailureStopsBeforeSpeech verifies the documented failure
// behavior: a provider without speech capabilities fails the DAG at
// hydration, before any run or side effect.
func TestVoiceWorkflowFailureStopsBeforeSpeech(t *testing.T) {
	providers := hydrate.ProviderMap{"chat-only-demo": {APIKey: "k"}}
	defs := voiceDAGDefs()
	defs[0].Config["provider"] = "chat-only-demo"
	defs[2].Config["provider"] = "chat-only-demo"

	_, err := hydrateNodeDefs("voice-failure", demoFactory(providers), defs)
	if err == nil {
		t.Fatal("expected hydration failure for a provider without speech capabilities")
	}
	if !strings.Contains(err.Error(), "does not support speech") {
		t.Errorf("error = %v", err)
	}
}

// TestVoiceWorkflowCancellationCommitNothing verifies that cancelling the
// run context mid-flight returns an error.
func TestVoiceWorkflowCancellationCommitNothing(t *testing.T) {
	g, err := hydrateNodeDefs("voice-cancel", demoFactory(demoProviders()), voiceDAGDefs())
	if err != nil {
		t.Fatalf("hydrating graph: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	result, err := runtime.NewRuntime().Run(ctx, g, core.NewEnvelope().WithVar("audio", simulatedAudio), runtime.DefaultRunOptions())
	if err == nil {
		t.Fatal("expected the cancelled run to fail")
	}
	if result != nil {
		if transcript := result.GetVarString("transcript"); transcript != "" {
			t.Errorf("cancelled run committed transcript %q", transcript)
		}
	}
}

// TestLiveRoundTripDemo runs the ElevenLabs round trip when the API key is
// available (skipped otherwise, e.g. in CI without secrets for this repo).
func TestLiveRoundTripDemo(t *testing.T) {
	if os.Getenv("ELEVENLABS_API_KEY") == "" {
		t.Skip("ELEVENLABS_API_KEY not set, skipping the live round trip")
	}
	if err := runLiveRoundTripDemo(); err != nil {
		t.Fatalf("runLiveRoundTripDemo: %v", err)
	}
}
