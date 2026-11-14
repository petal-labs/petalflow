package runtime

import (
	"testing"

	"github.com/petal-labs/petalflow/artifact"
)

func TestRunOptionsCarriesArtifactStore(t *testing.T) {
	store, err := artifact.NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	options := RunOptions{ArtifactStore: store}
	if options.ArtifactStore != store {
		t.Fatal("RunOptions did not retain ArtifactStore")
	}
}
