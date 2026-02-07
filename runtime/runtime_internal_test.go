package runtime

import "testing"

func TestGenerateRunID(t *testing.T) {
	id1 := generateRunID()
	id2 := generateRunID()

	if id1 == "" {
		t.Error("generateRunID() returned empty string")
	}
	if id1 == id2 {
		t.Error("generateRunID() should return unique IDs")
	}
}
