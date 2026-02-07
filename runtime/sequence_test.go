package runtime

import (
	"sync"
	"testing"
)

func TestSeqGen_Next_StartsAt1(t *testing.T) {
	sg := newSeqGen()
	got := sg.Next()
	if got != 1 {
		t.Fatalf("first call to Next() = %d, want 1", got)
	}
}

func TestSeqGen_Next_Monotonic(t *testing.T) {
	sg := newSeqGen()
	for i := uint64(1); i <= 100; i++ {
		got := sg.Next()
		if got != i {
			t.Fatalf("Next() call #%d = %d, want %d", i, got, i)
		}
	}
}

func TestSeqGen_Next_ConcurrentSafe(t *testing.T) {
	const goroutines = 100
	const callsPerGoroutine = 100
	const totalCalls = goroutines * callsPerGoroutine

	sg := newSeqGen()

	var mu sync.Mutex
	seen := make(map[uint64]bool, totalCalls)

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for c := 0; c < callsPerGoroutine; c++ {
				v := sg.Next()
				mu.Lock()
				seen[v] = true
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(seen) != totalCalls {
		t.Fatalf("unique values = %d, want %d", len(seen), totalCalls)
	}

	// Verify every value from 1..totalCalls is present.
	for i := uint64(1); i <= totalCalls; i++ {
		if !seen[i] {
			t.Fatalf("missing sequence number %d", i)
		}
	}
}
