package runtime

import "sync/atomic"

// seqGen produces monotonically increasing sequence numbers for a single run.
type seqGen struct {
	counter atomic.Uint64
}

func newSeqGen() *seqGen {
	return &seqGen{}
}

// Next returns the next sequence number (1-indexed).
func (s *seqGen) Next() uint64 {
	return s.counter.Add(1)
}
