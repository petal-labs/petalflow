package server

import (
	"testing"

	"github.com/petal-labs/petalflow/artifact"
)

func TestNewServerRetainsArtifactStore(t *testing.T) {
	store, err := artifact.NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	server := NewServer(ServerConfig{ArtifactStore: store})
	if server.artifactStore != store {
		t.Fatal("NewServer did not retain ArtifactStore")
	}
}
