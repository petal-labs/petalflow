package artifacttest

import (
	"context"
	"io"
	"testing"

	"github.com/petal-labs/petalflow/artifact"
)

func TestConformanceSuiteOnReferenceStore(t *testing.T) {
	store, err := artifact.NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	RunConformance(t, store)
}

func TestCheckUnavailable(t *testing.T) {
	CheckUnavailable(t, unavailableStore{})
}

func TestContainsMetadataMissing(t *testing.T) {
	if containsMetadata(nil, artifact.Metadata{}) {
		t.Fatal("containsMetadata(nil) = true, want false")
	}
}

type unavailableStore struct{}

func (unavailableStore) Put(context.Context, artifact.PutRequest, io.Reader) (artifact.Metadata, error) {
	return artifact.Metadata{}, artifact.ErrUnavailable
}

func (unavailableStore) Get(context.Context, artifact.Scope, artifact.Reference) (io.ReadCloser, error) {
	return nil, artifact.ErrUnavailable
}

func (unavailableStore) Stat(context.Context, artifact.Scope, artifact.Reference) (artifact.Metadata, error) {
	return artifact.Metadata{}, artifact.ErrUnavailable
}

func (unavailableStore) Delete(context.Context, artifact.Scope, artifact.Reference) error {
	return artifact.ErrUnavailable
}

func (unavailableStore) ListByRun(context.Context, artifact.Scope) ([]artifact.Metadata, error) {
	return nil, artifact.ErrUnavailable
}
