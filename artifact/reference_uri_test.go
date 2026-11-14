package artifact_test

import (
	"context"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/artifact"
)

func TestReferenceURIRoundTrip(t *testing.T) {
	ref := artifact.Reference{Digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 42, ContentType: "text/plain; charset=utf-8"}
	got, err := artifact.ParseReferenceURI(artifact.ReferenceURI(ref))
	if err != nil {
		t.Fatalf("ParseReferenceURI: %v", err)
	}
	if got != ref {
		t.Fatalf("reference = %+v, want %+v", got, ref)
	}
}

func TestResolveURIEnforcesReadLimit(t *testing.T) {
	store, _ := artifact.NewInMemoryStore()
	ref := artifact.Reference{Digest: strings.Repeat("a", 64), Size: 2, ContentType: "text/plain"}
	_, _, err := artifact.ResolveURI(context.Background(), artifact.Resolver{Store: store, MaxBytes: 1}, artifact.ReferenceURI(ref))
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("ResolveURI error = %v, want size limit", err)
	}
}

func TestParseReferenceURIRejectsUntrustedValues(t *testing.T) {
	for _, value := range []string{
		"https://example.com/file",
		"artifact://sha256/not-a-digest?size=1&content_type=text%2Fplain",
		"artifact://sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa?size=-1&content_type=text%2Fplain",
	} {
		if _, err := artifact.ParseReferenceURI(value); err == nil {
			t.Errorf("ParseReferenceURI(%q) error = nil", value)
		}
	}
}
