package memorytest

import (
	"testing"

	"github.com/petal-labs/petalflow/memory"
)

// TestInMemoryProviderConformance dogfoods the conformance suite against the
// reference in-memory provider, and demonstrates how external implementations
// (such as a Cortex adapter) verify the PetalFlow memory contract without
// importing PetalFlow internals.
func TestInMemoryProviderConformance(t *testing.T) {
	RunConformance(t, memory.NewInMemoryProvider())
}
