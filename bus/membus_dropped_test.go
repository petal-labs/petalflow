package bus

import (
	"testing"

	"github.com/petal-labs/petalflow/runtime"
)

func TestMemBus_Send_CountsDrops(t *testing.T) {
	b := NewMemBus(MemBusConfig{SubscriberBufferSize: 2})
	sub := b.Subscribe("r")

	// Publish more than the buffer holds without draining; the excess is dropped.
	for i := 0; i < 5; i++ {
		b.Publish(runtime.Event{RunID: "r"})
	}

	if sub.Dropped() == 0 {
		t.Error("expected Dropped > 0 on a full subscriber buffer")
	}
}
