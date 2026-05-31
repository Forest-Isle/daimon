package agent

import "sync"

// EventBus is a simple in-process pub/sub bus for agent lifecycle events.
// Publish is non-blocking; subscribers are called in separate goroutines.
type EventBus interface {
	Publish(event Event)
	Subscribe(handler func(Event)) Subscription
}

// Subscription represents an active event subscription.
type Subscription interface {
	Unsubscribe()
}

// NewEventBus creates a new in-process event bus.
func NewEventBus() EventBus {
	return &inprocBus{
		subs: make(map[int]func(Event)),
	}
}

type inprocBus struct {
	mu   sync.RWMutex
	subs map[int]func(Event)
	next int
}

func (b *inprocBus) Publish(event Event) {
	b.mu.RLock()
	// Snapshot subscribers to avoid holding lock during handler execution
	handlers := make([]func(Event), 0, len(b.subs))
	for _, h := range b.subs {
		handlers = append(handlers, h)
	}
	b.mu.RUnlock()

	for _, h := range handlers {
		go h(event)
	}
}

func (b *inprocBus) Subscribe(handler func(Event)) Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	b.subs[id] = handler
	return &subscription{bus: b, id: id}
}

type subscription struct {
	bus *inprocBus
	id  int
}

func (s *subscription) Unsubscribe() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	delete(s.bus.subs, s.id)
}
