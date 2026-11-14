package artifact_test

import (
	"testing"

	"github.com/petal-labs/petalflow/artifact"
)

func TestReferenceIDRoundTrip(t *testing.T) {
	reference := artifact.Reference{
		Digest:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Size:        42,
		ContentType: "application/json; charset=utf-8",
	}
	id := artifact.ReferenceID(reference)
	got, err := artifact.ParseReferenceID(id)
	if err != nil {
		t.Fatalf("ParseReferenceID: %v", err)
	}
	if got != reference {
		t.Fatalf("reference = %+v, want %+v", got, reference)
	}
}

func TestParseReferenceIDRejectsInvalidValue(t *testing.T) {
	if _, err := artifact.ParseReferenceID("not-an-artifact-id"); err == nil {
		t.Fatal("ParseReferenceID error = nil")
	}
}
