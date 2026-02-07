// Package bus provides an event distribution system for PetalFlow workflow
// execution. It allows components to publish and subscribe to runtime events,
// enabling decoupled communication between the execution engine and observers
// such as loggers, UIs, and monitoring systems.
package bus

import "github.com/petal-labs/petalflow/runtime"

// EventBus distributes events to subscribers.
type EventBus interface {
	// Publish sends an event to all matching subscribers.
	Publish(event runtime.Event)

	// Subscribe registers a subscriber for a specific run.
	// Returns a Subscription that must be closed when done.
	Subscribe(runID string) Subscription

	// SubscribeAll registers a subscriber that receives events from all runs.
	// Returns a Subscription that must be closed when done.
	SubscribeAll() Subscription

	// Close shuts down the bus and all subscriptions.
	Close() error
}

// Subscription receives events.
type Subscription interface {
	// Events returns a channel of events for this subscription.
	Events() <-chan runtime.Event

	// Close unsubscribes and releases resources.
	Close() error
}
