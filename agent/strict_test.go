package agent

import (
	"strings"
	"testing"
)

func TestLoadFromBytes_RejectsUnknownField(t *testing.T) {
	data := []byte(`{"version":"1.0","kind":"agent_workflow","id":"w","name":"n","agents":{},"tasks":{},"execution":{"strategy":"sequential"},"bogus":true}`)

	_, err := LoadFromBytes(data)
	if err == nil {
		t.Fatal("expected an error for an unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error = %v, want it to name the unknown field", err)
	}
}

func TestLoadFromBytes_AcceptsKnownFields(t *testing.T) {
	data := []byte(`{"version":"1.0","kind":"agent_workflow","id":"w","name":"n","agents":{},"tasks":{},"execution":{"strategy":"sequential"}}`)

	if _, err := LoadFromBytes(data); err != nil {
		t.Fatalf("unexpected error for a valid workflow: %v", err)
	}
}
