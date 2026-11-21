package llmprovider

import (
	"fmt"
	"strings"

	iriscore "github.com/petal-labs/iris/v2/core"

	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/nodes"
)

func init() {
	// Install the Iris-backed speech factories as hydration defaults so
	// workflows that declare speech nodes resolve credentials through the
	// standard ProviderMap flow (config file, PETALFLOW_PROVIDER_* env vars,
	// --provider-key flags) without explicit per-run wiring. Hydrate keeps no
	// Iris import: this registration happens only in binaries that link
	// llmprovider. Callers needing full control can still override with
	// hydrate.WithSpeechRecognizerFactory / WithSpeechSynthesizerFactory.
	hydrate.RegisterDefaultSpeechFactories(NewSpeechRecognizer, NewSpeechSynthesizer)
}

// NewSpeechRecognizer returns a nodes.SpeechRecognizer for the named provider.
// It delegates to the iris provider registry, wraps the provider in an Iris
// client (rate limiting, retry for in-memory audio, timeout, telemetry), and
// discovers the Iris speech_recognition capability: a provider without that
// capability, or without resolved credentials, fails here at construction
// instead of at run time. Errors never include the API key.
func NewSpeechRecognizer(name string, cfg hydrate.ProviderConfig) (nodes.SpeechRecognizer, error) {
	provider, err := createProvider(name, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating provider %q: %w", name, err)
	}
	if _, ok := iriscore.AsSpeechRecognizer(provider); !ok {
		return nil, fmt.Errorf("provider %q does not support speech recognition (iris %s capability missing)", name, iriscore.FeatureSpeechRecognition)
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errMissingSpeechCredential(name)
	}
	return iriscore.NewClient(provider), nil
}

// NewSpeechSynthesizer returns a nodes.SpeechSynthesizer for the named
// provider. It delegates to the iris provider registry, wraps the provider in
// an Iris client (rate limiting, retry for setup, timeout, telemetry), and
// discovers the Iris speech_synthesis capability: a provider without that
// capability, or without resolved credentials, fails here at construction
// instead of at run time. Errors never include the API key.
func NewSpeechSynthesizer(name string, cfg hydrate.ProviderConfig) (nodes.SpeechSynthesizer, error) {
	provider, err := createProvider(name, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating provider %q: %w", name, err)
	}
	if _, ok := iriscore.AsSpeechSynthesizer(provider); !ok {
		return nil, fmt.Errorf("provider %q does not support speech synthesis (iris %s capability missing)", name, iriscore.FeatureSpeechSynthesis)
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errMissingSpeechCredential(name)
	}
	return iriscore.NewClient(provider), nil
}

// errMissingSpeechCredential reports unresolved speech credentials with the
// concrete resolution paths. It never echoes the (empty) key value.
func errMissingSpeechCredential(name string) error {
	return fmt.Errorf(
		"provider %q requires an API key for speech: set PETALFLOW_PROVIDER_%s_API_KEY, providers.%s.api_key in the config file, or --provider-key %s=<key>",
		name, strings.ToUpper(name), name, name,
	)
}
