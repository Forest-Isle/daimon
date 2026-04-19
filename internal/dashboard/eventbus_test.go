package dashboard

import (
	"testing"
	"time"
)

func TestBusPublishToSubscriber(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	bus.Publish(Event{
		Type:      EventPhaseStart,
		Timestamp: time.Now(),
		SessionID: "s1",
		Data:      map[string]any{"phase": "PLAN"},
	})

	select {
	case ev := <-ch:
		if ev.Type != EventPhaseStart {
			t.Fatalf("got type %s, want phase.start", ev.Type)
		}
		if ev.Data["phase"] != "PLAN" {
			t.Fatalf("got phase %v, want PLAN", ev.Data["phase"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestBusMultipleSubscribers(t *testing.T) {
	bus := NewBus(16)
	ch1 := bus.Subscribe()
	ch2 := bus.Subscribe()
	defer bus.Unsubscribe(ch1)
	defer bus.Unsubscribe(ch2)

	bus.Publish(Event{Type: EventToolStart, Timestamp: time.Now()})

	for _, ch := range []chan Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Type != EventToolStart {
				t.Fatalf("got %s, want tool.start", ev.Type)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
}

func TestBusUnsubscribe(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	bus.Unsubscribe(ch)

	bus.Publish(Event{Type: EventAgentIdle, Timestamp: time.Now()})

	select {
	case <-ch:
		t.Fatal("unsubscribed channel should not receive events")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestBusSlowSubscriberDoesNotBlock(t *testing.T) {
	bus := NewBus(1) // buffer of 1
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	// Fill buffer
	bus.Publish(Event{Type: EventPhaseStart, Timestamp: time.Now()})
	// This should not block even though buffer is full
	bus.Publish(Event{Type: EventPhaseEnd, Timestamp: time.Now()})

	ev := <-ch
	if ev.Type != EventPhaseStart {
		t.Fatalf("got %s, want phase.start", ev.Type)
	}
}
