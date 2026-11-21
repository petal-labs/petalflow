package llmprovider

import (
	"context"
	"fmt"
	"strings"

	iriscore "github.com/petal-labs/iris/v2/core"

	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/voice"
)

// NewVoiceSessionStarter returns a voice.SessionStarter for the named
// provider. It delegates to the iris provider registry, wraps the provider in
// an Iris client (rate limiting before every session start), and discovers
// the Iris voice_session capability: a provider without that capability, or
// without resolved credentials, fails here at construction instead of at
// session start. Errors never include the API key.
//
// Voice sessions are deliberately separate from one-shot DAG nodes — this
// starter is not wired into graph hydration; callers hold the returned
// voice.Session handles directly (see docs/voice-sessions.md).
func NewVoiceSessionStarter(name string, cfg hydrate.ProviderConfig) (voice.SessionStarter, error) {
	provider, err := createProvider(name, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating provider %q: %w", name, err)
	}
	if _, ok := iriscore.AsVoiceSessionProvider(provider); !ok {
		return nil, fmt.Errorf("provider %q does not support voice sessions (iris %s capability missing)", name, iriscore.FeatureVoiceSession)
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errMissingSpeechCredential(name)
	}
	client := iriscore.NewClient(provider)
	return voice.StarterFunc(func(ctx context.Context, sessionCfg iriscore.VoiceSessionConfig) (*voice.Session, error) {
		session, err := client.StartVoiceSession(ctx, sessionCfg)
		if err != nil {
			return nil, fmt.Errorf("starting voice session on provider %q: %w", name, err)
		}
		return voice.NewSession(ctx, session), nil
	}), nil
}
