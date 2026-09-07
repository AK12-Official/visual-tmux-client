package eventbus

import (
	"testing"
	"time"
)

func TestBus_OutputDeliveredToSubscriber(t *testing.T) {
	b := NewBus()
	_, ch := b.SubscribeOutput()

	b.PublishOutput(PaneOutputEvent{PaneID: "%1", Data: []byte("hello")})

	select {
	case evt := <-ch:
		if evt.PaneID != "%1" || string(evt.Data) != "hello" {
			t.Errorf("unexpected event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("expected to receive published output event")
	}
}

func TestBus_LifecycleDeliveredToSubscriber(t *testing.T) {
	b := NewBus()
	_, ch := b.SubscribeLifecycle()

	b.PublishLifecycle(LifecycleEvent{Type: SessionDiscovered, Payload: "alpha"})

	select {
	case evt := <-ch:
		if evt.Type != SessionDiscovered || evt.Payload != "alpha" {
			t.Errorf("unexpected event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("expected to receive published lifecycle event")
	}
}

func TestBus_SessionRenamedAndPaneFocusChangedDeliveredToSubscriber(t *testing.T) {
	b := NewBus()
	_, ch := b.SubscribeLifecycle()

	b.PublishLifecycle(LifecycleEvent{Type: SessionRenamed, Payload: "alpha"})
	b.PublishLifecycle(LifecycleEvent{Type: PaneFocusChanged, Payload: "%3"})

	for _, want := range []LifecycleEvent{
		{Type: SessionRenamed, Payload: "alpha"},
		{Type: PaneFocusChanged, Payload: "%3"},
	} {
		select {
		case evt := <-ch:
			if evt.Type != want.Type || evt.Payload != want.Payload {
				t.Errorf("expected %+v, got %+v", want, evt)
			}
		case <-time.After(time.Second):
			t.Fatalf("expected to receive published %v event", want.Type)
		}
	}
}

// TestBus_SlowLifecycleSubscriberDoesNotBlockOutput is the load-bearing
// test for design.md's "Event bus separates high-frequency output from
// lifecycle events" decision: a lifecycle subscriber that never drains its
// channel must not stall publication of pane.output events to *any*
// subscriber, including a healthy output subscriber.
func TestBus_SlowLifecycleSubscriberDoesNotBlockOutput(t *testing.T) {
	b := NewBus()

	// Slow/absent lifecycle subscriber: subscribed but never reads, and we
	// publish enough lifecycle events to fill its buffer.
	_, lifecycleCh := b.SubscribeLifecycle()
	for i := range defaultLifecycleBuffer + 10 {
		b.PublishLifecycle(LifecycleEvent{Type: PaneDied, Payload: i})
	}
	_ = lifecycleCh // intentionally never drained in this test

	// Healthy output subscriber.
	_, outputCh := b.SubscribeOutput()

	done := make(chan struct{})
	go func() {
		b.PublishOutput(PaneOutputEvent{PaneID: "%1", Data: []byte("still flowing")})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("PublishOutput blocked despite a full, undrained lifecycle subscriber")
	}

	select {
	case evt := <-outputCh:
		if string(evt.Data) != "still flowing" {
			t.Errorf("unexpected output event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("expected output subscriber to still receive events despite slow lifecycle subscriber")
	}
}

func TestBus_UnsubscribeStopsDelivery(t *testing.T) {
	b := NewBus()
	id, ch := b.SubscribeOutput()
	b.UnsubscribeOutput(id)

	b.PublishOutput(PaneOutputEvent{PaneID: "%1", Data: []byte("x")})

	// The channel is deliberately left open (not closed) after unsubscribe —
	// see UnsubscribeOutput's doc comment — but nothing should arrive on it
	// once it is out of the subscriber list.
	select {
	case v, ok := <-ch:
		t.Errorf("expected no delivery after unsubscribe, got value=%v ok=%v", v, ok)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBus_FullSubscriberBufferDropsRatherThanBlocks(t *testing.T) {
	b := NewBus()
	_, ch := b.SubscribeOutput()

	// Fill the subscriber's buffer without draining it.
	for i := range defaultOutputBuffer + 10 {
		done := make(chan struct{})
		go func() {
			b.PublishOutput(PaneOutputEvent{PaneID: "%1", Data: []byte("x")})
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("PublishOutput blocked on iteration %d despite non-blocking delivery contract", i)
		}
	}

	if len(ch) != defaultOutputBuffer {
		t.Errorf("expected channel to be full at %d, got %d", defaultOutputBuffer, len(ch))
	}
}
