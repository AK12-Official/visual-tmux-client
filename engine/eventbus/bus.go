// Package eventbus implements the engine's event bus, keeping the
// high-frequency pane.output stream on its own channel per subscriber,
// separate from low-frequency lifecycle events, so a slow consumer of one
// cannot stall delivery of the other.
package eventbus

import "sync"

// LifecycleEventType names the kind of a LifecycleEvent.
type LifecycleEventType string

const (
	SessionDiscovered   LifecycleEventType = "session.discovered"
	SessionClosed       LifecycleEventType = "session.closed"
	SessionRenamed      LifecycleEventType = "session.renamed"
	WindowLayoutChanged LifecycleEventType = "window.layout-changed"
	WindowRenamed       LifecycleEventType = "window.renamed"
	PaneDied            LifecycleEventType = "pane.died"
	PaneFocusChanged    LifecycleEventType = "pane.focus-changed"
	HostConnected       LifecycleEventType = "host.connected"
	HostDisconnected    LifecycleEventType = "host.disconnected"
	HostDegraded        LifecycleEventType = "host.degraded"
)

// PaneOutputEvent carries raw bytes produced by a pane.
type PaneOutputEvent struct {
	PaneID string
	Data   []byte
}

// LifecycleEvent carries a structural change in the domain model. Payload's
// concrete type depends on Type (e.g. a SessionKey for SessionDiscovered).
type LifecycleEvent struct {
	Type    LifecycleEventType
	Payload any
}

const (
	defaultOutputBuffer    = 256
	defaultLifecycleBuffer = 64
)

type outputSub struct {
	id int
	ch chan PaneOutputEvent
}

type lifecycleSub struct {
	id int
	ch chan LifecycleEvent
}

// Bus is a thread-safe, non-blocking publish/subscribe event bus. Publish
// calls never block on a slow or absent subscriber: each subscriber has its
// own buffered channel, and a full channel causes that single subscriber to
// miss the event rather than stalling the publisher or any other
// subscriber. This mirrors the per-pane ring buffer's role in the engine:
// the bus is for live delivery, not guaranteed-delivery history.
type Bus struct {
	mu sync.Mutex

	nextID int

	outputSubs    []*outputSub
	lifecycleSubs []*lifecycleSub
}

// NewBus returns an empty Bus.
func NewBus() *Bus {
	return &Bus{}
}

// SubscribeOutput registers a new subscriber for pane.output events and
// returns its ID (for Unsubscribe) and its receive channel.
func (b *Bus) SubscribeOutput() (int, <-chan PaneOutputEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	sub := &outputSub{id: b.nextID, ch: make(chan PaneOutputEvent, defaultOutputBuffer)}
	b.outputSubs = append(b.outputSubs, sub)
	return sub.id, sub.ch
}

// UnsubscribeOutput removes a previously registered output subscriber. The
// subscriber's channel is not closed — Publish loads the subscriber slice
// under the same lock but sends to each channel afterward, unlocked, so
// closing here could race with an in-flight send. Leaving it open and simply
// forgetting it is safe: nothing sends to it once it is out of the slice,
// and it is garbage collected once the caller drops its receive end.
func (b *Bus) UnsubscribeOutput(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, sub := range b.outputSubs {
		if sub.id == id {
			b.outputSubs = append(b.outputSubs[:i], b.outputSubs[i+1:]...)
			return
		}
	}
}

// SubscribeLifecycle registers a new subscriber for lifecycle events and
// returns its ID (for Unsubscribe) and its receive channel.
func (b *Bus) SubscribeLifecycle() (int, <-chan LifecycleEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	sub := &lifecycleSub{id: b.nextID, ch: make(chan LifecycleEvent, defaultLifecycleBuffer)}
	b.lifecycleSubs = append(b.lifecycleSubs, sub)
	return sub.id, sub.ch
}

// UnsubscribeLifecycle removes a previously registered lifecycle subscriber.
// The subscriber's channel is not closed — PublishLifecycle loads the
// subscriber slice under the same lock but sends to each channel afterward,
// unlocked, so closing here could race with an in-flight send. Leaving it
// open and simply forgetting it is safe: nothing sends to it once it is out
// of the slice, and it is garbage collected once the caller drops its
// receive end.
func (b *Bus) UnsubscribeLifecycle(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, sub := range b.lifecycleSubs {
		if sub.id == id {
			b.lifecycleSubs = append(b.lifecycleSubs[:i], b.lifecycleSubs[i+1:]...)
			return
		}
	}
}

// PublishOutput delivers evt to every output subscriber. Delivery to each
// subscriber is non-blocking: a subscriber whose buffer is full simply
// misses the event.
func (b *Bus) PublishOutput(evt PaneOutputEvent) {
	b.mu.Lock()
	subs := make([]*outputSub, len(b.outputSubs))
	copy(subs, b.outputSubs)
	b.mu.Unlock()

	for _, sub := range subs {
		select {
		case sub.ch <- evt:
		default:
		}
	}
}

// PublishLifecycle delivers evt to every lifecycle subscriber. Delivery to
// each subscriber is non-blocking: a subscriber whose buffer is full simply
// misses the event.
func (b *Bus) PublishLifecycle(evt LifecycleEvent) {
	b.mu.Lock()
	subs := make([]*lifecycleSub, len(b.lifecycleSubs))
	copy(subs, b.lifecycleSubs)
	b.mu.Unlock()

	for _, sub := range subs {
		select {
		case sub.ch <- evt:
		default:
		}
	}
}
