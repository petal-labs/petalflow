package hydrate

import (
	"strings"
	"testing"
)

func TestParseConfigBytes_RejectsUnknownField(t *testing.T) {
	data := []byte(`{"providers":{},"bogus":true}`)

	_, err := parseConfigBytes(data)
	if err == nil {
		t.Fatal("expected an error for an unknown config field, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error = %v, want it to name the unknown field", err)
	}
}

func TestParseConfigBytes_AcceptsKnownFields(t *testing.T) {
	data := []byte(`{"providers":{"openai":{"api_key":"sk-x"}},"defaults":{"provider":"openai"}}`)

	if _, err := parseConfigBytes(data); err != nil {
		t.Fatalf("unexpected error for a valid config: %v", err)
	}
}
