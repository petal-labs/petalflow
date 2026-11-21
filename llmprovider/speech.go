package llmprovider

import (
	"fmt"

	iriscore "github.com/petal-labs/iris/v2/core"

	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/nodes"
)

// NewSpeechRecognizer returns a nodes.SpeechRecognizer for the named provider.
// It delegates to the iris provider registry, then discovers the Iris
// speech_recognition capability on the provider: a provider without that
// capability fails here, at construction, instead of at run time.
func NewSpeechRecognizer(name string, cfg hydrate.ProviderConfig) (nodes.SpeechRecognizer, error) {
	provider, err := createProvider(name, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating provider %q: %w", name, err)
	}
	recognizer, ok := iriscore.AsSpeechRecognizer(provider)
	if !ok {
		return nil, fmt.Errorf("provider %q does not support speech recognition (iris %s capability missing)", name, iriscore.FeatureSpeechRecognition)
	}
	return recognizer, nil
}
