package loader

import (
	"strings"
	"testing"
)

func TestLoadGraphDefinition_RejectsUnknownTopLevelField(t *testing.T) {
	data := []byte(`{"id":"g","version":"1.0","nodes":[{"id":"a","type":"noop"}],"edges":[],"entry":"a","bogus_field":true}`)

	_, err := loadGraphDefinition(data, "test.json")
	if err == nil {
		t.Fatal("expected an error for an unknown top-level field, got nil")
	}
	if !strings.Contains(err.Error(), "bogus_field") {
		t.Errorf("error = %v, want it to name the unknown field", err)
	}
}

func TestLoadGraphDefinition_RejectsUnknownNodeField(t *testing.T) {
	data := []byte(`{"id":"g","version":"1.0","nodes":[{"id":"a","type":"noop","bogus":1}],"edges":[],"entry":"a"}`)

	_, err := loadGraphDefinition(data, "test.json")
	if err == nil {
		t.Fatal("expected an error for an unknown node field, got nil")
	}
}

func TestLoadGraphDefinition_AcceptsKnownFields(t *testing.T) {
	data := []byte(`{"id":"g","version":"1.0","kind":"graph","nodes":[{"id":"a","type":"noop","config":{}}],"edges":[],"entry":"a"}`)

	if _, err := loadGraphDefinition(data, "test.json"); err != nil {
		t.Fatalf("unexpected error for a valid graph: %v", err)
	}
}
