package bus

import (
	"sync"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/runtime"
)

func TestMemBus_PublishSubscribe(t *testing.T) {
	b := NewMemBus(MemBusConfig{})
	defer b.Close()

	sub := b.Subscribe("run-1")
	defer sub.Close()

	event := runtime.NewEvent(runtime.EventRunStarted, "run-1")
	b.Publish(event)

	select {
	case received := <-sub.Events():
		if received.Kind != runtime.EventRunStarted {
			t.Errorf("got kind %v, want %v", received.Kind, runtime.EventRunStarted)
		}
		if received.RunID != "run-1" {
			t.Errorf("got RunID %q, want %q", received.RunID, "run-1")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestMemBus_FanOut(t *testing.T) {
	b := NewMemBus(MemBusConfig{})
	defer b.Close()

	sub1 := b.Subscribe("run-1")
	defer sub1.Close()
	sub2 := b.Subscribe("run-1")
	defer sub2.Close()
	sub3 := b.Subscribe("run-1")
	defer sub3.Close()

	event := runtime.NewEvent(runtime.EventNodeStarted, "run-1")
	b.Publish(event)

	for i, sub := range []Subscription{sub1, sub2, sub3} {
		select {
		case e := <-sub.Events():
			if e.Kind != runtime.EventNodeStarted {
				t.Errorf("sub%d: got kind %v, want %v", i, e.Kind, runtime.EventNodeStarted)
			}
		case <-time.After(time.Second):
			t.Fatalf("sub%d: timed out", i)
		}
	}
}

func TestMemBus_RunIsolation(t *testing.T) {
	b := NewMemBus(MemBusConfig{})
	defer b.Close()

	sub1 := b.Subscribe("run-1")
	defer sub1.Close()
	sub2 := b.Subscribe("run-2")
	defer sub2.Close()

	b.Publish(runtime.NewEvent(runtime.EventRunStarted, "run-1"))

	select {
	case <-sub1.Events():
		// expected
	case <-time.After(time.Second):
		t.Fatal("sub1 should receive run-1 events")
	}

	select {
	case <-sub2.Events():
		t.Fatal("sub2 should NOT receive run-1 events")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestMemBus_SubscribeAll(t *testing.T) {
	b := NewMemBus(MemBusConfig{})
	defer b.Close()

	global := b.SubscribeAll()
	defer global.Close()

	b.Publish(runtime.NewEvent(runtime.EventRunStarted, "run-1"))
	b.Publish(runtime.NewEvent(runtime.EventRunStarted, "run-2"))
	b.Publish(runtime.NewEvent(runtime.EventRunStarted, "run-3"))

	for i := 0; i < 3; i++ {
		select {
		case <-global.Events():
		case <-time.After(time.Second):
			t.Fatalf("global subscriber missed event %d", i)
		}
	}
}

func TestMemBus_SubscribeAllWithRunSpecific(t *testing.T) {
	b := NewMemBus(MemBusConfig{})
	defer b.Close()

	runSub := b.Subscribe("run-1")
	defer runSub.Close()
	globalSub := b.SubscribeAll()
	defer globalSub.Close()

	b.Publish(runtime.NewEvent(runtime.EventRunStarted, "run-1"))

	// Both the run-specific and global subscriber should receive the event.
	select {
	case <-runSub.Events():
	case <-time.After(time.Second):
		t.Fatal("run subscriber should receive event")
	}

	select {
	case <-globalSub.Events():
	case <-time.After(time.Second):
		t.Fatal("global subscriber should receive event")
	}
}

func TestMemBus_ClosedSubscription(t *testing.T) {
	b := NewMemBus(MemBusConfig{})
	defer b.Close()

	sub := b.Subscribe("run-1")
	sub.Close()

	// Publishing after subscription close should not panic.
	b.Publish(runtime.NewEvent(runtime.EventRunStarted, "run-1"))
}

func TestMemBus_DoubleCloseSubscription(t *testing.T) {
	b := NewMemBus(MemBusConfig{})
	defer b.Close()

	sub := b.Subscribe("run-1")

	// Closing twice should not panic.
	if err := sub.Close(); err != nil {
		t.Fatalf("first Close returned error: %v", err)
	}
	if err := sub.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

func TestMemBus_ClosedBusPublish(t *testing.T) {
	b := NewMemBus(MemBusConfig{})

	sub := b.Subscribe("run-1")
	b.Close()

	// Publishing to a closed bus should not panic.
	b.Publish(runtime.NewEvent(runtime.EventRunStarted, "run-1"))

	// The subscription channel should be closed (drained and then zero-value).
	select {
	case _, ok := <-sub.Events():
		if ok {
			t.Fatal("expected channel to be closed after bus Close")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for closed channel")
	}
}

func TestMemBus_DefaultBufferSize(t *testing.T) {
	b := NewMemBus(MemBusConfig{})
	defer b.Close()

	if b.bufSize != 256 {
		t.Errorf("default buffer size = %d, want 256", b.bufSize)
	}
}

func TestMemBus_CustomBufferSize(t *testing.T) {
	b := NewMemBus(MemBusConfig{SubscriberBufferSize: 64})
	defer b.Close()

	if b.bufSize != 64 {
		t.Errorf("buffer size = %d, want 64", b.bufSize)
	}
}

func TestMemBus_BufferOverflow(t *testing.T) {
	b := NewMemBus(MemBusConfig{SubscriberBufferSize: 2})
	defer b.Close()

	sub := b.Subscribe("run-1")
	defer sub.Close()

	// Publish 5 events into a buffer of size 2; extras should be dropped.
	for i := 0; i < 5; i++ {
		b.Publish(runtime.NewEvent(runtime.EventNodeStarted, "run-1"))
	}

	count := 0
	for {
		select {
		case <-sub.Events():
			count++
		case <-time.After(50 * time.Millisecond):
			goto done
		}
	}
done:
	if count != 2 {
		t.Errorf("received %d events, want 2 (buffer size)", count)
	}
}

func TestMemBus_ConcurrentPublish(t *testing.T) {
	b := NewMemBus(MemBusConfig{SubscriberBufferSize: 1000})
	defer b.Close()

	sub := b.Subscribe("run-1")
	defer sub.Close()

	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Publish(runtime.NewEvent(runtime.EventNodeStarted, "run-1"))
		}()
	}
	wg.Wait()

	// Drain and count.
	count := 0
	for {
		select {
		case <-sub.Events():
			count++
		case <-time.After(100 * time.Millisecond):
			goto done
		}
	}
done:
	if count != n {
		t.Errorf("received %d events, want %d", count, n)
	}
}

func TestMemBus_ConcurrentSubscribePublish(t *testing.T) {
	b := NewMemBus(MemBusConfig{SubscriberBufferSize: 100})
	defer b.Close()

	var wg sync.WaitGroup

	// Concurrently subscribe and publish.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := b.Subscribe("run-1")
			defer sub.Close()
			b.Publish(runtime.NewEvent(runtime.EventNodeStarted, "run-1"))
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := b.SubscribeAll()
			defer sub.Close()
			b.Publish(runtime.NewEvent(runtime.EventRunStarted, "run-1"))
		}()
	}

	wg.Wait()
}
