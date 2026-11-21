package hydrate

import "sync"

// Default speech factories let workflows that declare speech nodes hydrate
// without explicit wiring: the caller (llmprovider) installs Iris-backed
// factories once at init, and NewLiveNodeFactory falls back to them whenever
// WithSpeechRecognizerFactory/WithSpeechSynthesizerFactory was not supplied.
// This keeps hydrate free of any direct Iris import — the registration hook is
// populated by whichever binary links llmprovider, and credential resolution
// stays in the standard ProviderMap flow (config file, env vars, provider
// flags).

var (
	defaultSpeechFactoriesMu        sync.RWMutex
	defaultSpeechRecognizerFactory  SpeechRecognizerFactory
	defaultSpeechSynthesizerFactory SpeechSynthesizerFactory
)

// RegisterDefaultSpeechFactories installs the default speech node factories.
// Passing nil for either factory keeps the previously registered value.
// Callers that need full control can still override per-factory with the
// WithSpeechRecognizerFactory / WithSpeechSynthesizerFactory options.
func RegisterDefaultSpeechFactories(recognizer SpeechRecognizerFactory, synthesizer SpeechSynthesizerFactory) {
	defaultSpeechFactoriesMu.Lock()
	defer defaultSpeechFactoriesMu.Unlock()
	if recognizer != nil {
		defaultSpeechRecognizerFactory = recognizer
	}
	if synthesizer != nil {
		defaultSpeechSynthesizerFactory = synthesizer
	}
}

// DefaultSpeechFactories returns the currently registered default speech
// factories (both nil when none are registered), for diagnostics and tests.
func DefaultSpeechFactories() (SpeechRecognizerFactory, SpeechSynthesizerFactory) {
	defaultSpeechFactoriesMu.RLock()
	defer defaultSpeechFactoriesMu.RUnlock()
	return defaultSpeechRecognizerFactory, defaultSpeechSynthesizerFactory
}

// defaultSpeechRecognizerFactory returns the registered default recognizer
// factory, if any.
func lookupDefaultSpeechRecognizerFactory() SpeechRecognizerFactory {
	defaultSpeechFactoriesMu.RLock()
	defer defaultSpeechFactoriesMu.RUnlock()
	return defaultSpeechRecognizerFactory
}

// defaultSpeechSynthesizerFactory returns the registered default synthesizer
// factory, if any.
func lookupDefaultSpeechSynthesizerFactory() SpeechSynthesizerFactory {
	defaultSpeechFactoriesMu.RLock()
	defer defaultSpeechFactoriesMu.RUnlock()
	return defaultSpeechSynthesizerFactory
}
