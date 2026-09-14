package memorytest_test

import (
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/memory"
	"github.com/petal-labs/petalflow/memory/memorytest"
)

// The reference provider must pass its own conformance suite; this is also
// the example a backend adapter copies.
func TestInMemoryProviderConformance(t *testing.T) {
	memorytest.RunMemoryProvider(t, func(*testing.T) memory.MemoryProvider {
		return memory.NewInMemoryProvider()
	})
	memorytest.RunKnowledgeProvider(t, func(t *testing.T, scope memory.Scope, docs []core.Artifact) memory.KnowledgeProvider {
		p := memory.NewInMemoryProvider()
		for _, d := range docs {
			if err := p.AddDocument(scope, "", d); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
		return p
	})
}
