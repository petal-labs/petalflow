package runtime

import (
	"encoding/json"
	"testing"

	"github.com/petal-labs/petalflow/contract"
)

func TestNewEventIncludesCurrentSchemaVersion(t *testing.T) {
	t.Parallel()

	event := NewEvent(EventRunStarted, "run-1")
	if event.SchemaVersion != contract.EventSchemaVersion {
		t.Fatalf("SchemaVersion = %q, want %q", event.SchemaVersion, contract.EventSchemaVersion)
	}
}

func TestEventJSONIncludesSchemaVersion(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(NewEvent(EventRunStarted, "run-1"))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var artifact map[string]any
	if err := json.Unmarshal(encoded, &artifact); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if artifact["schema_version"] != contract.EventSchemaVersion {
		t.Fatalf("schema_version = %#v, want %q", artifact["schema_version"], contract.EventSchemaVersion)
	}
}

func TestEventValidateSchemaVersion(t *testing.T) {
	t.Parallel()

	event := NewEvent(EventRunStarted, "run-1")
	if err := event.ValidateSchemaVersion(); err != nil {
		t.Fatalf("ValidateSchemaVersion() error = %v", err)
	}

	event.SchemaVersion = "2.0.0"
	if err := event.ValidateSchemaVersion(); err == nil {
		t.Fatal("ValidateSchemaVersion() error = nil, want unsupported-major error")
	}
}
