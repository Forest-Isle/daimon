package dashboard

import (
	"sync"
	"time"
)

type EventType string

const (
	EventPhaseStart        EventType = "phase.start"
	EventPhaseEnd          EventType = "phase.end"
	EventToolStart         EventType = "tool.start"
	EventToolEnd           EventType = "tool.end"
	EventPlanGenerated     EventType = "plan.generated"
	EventReplanStart       EventType = "replan.start"
	EventTaskUpdate        EventType = "task.update"
	EventObservationResult EventType = "observation.result"
	EventSessionStart      EventType = "session.start"
	EventSessionEnd        EventType = "session.end"
	EventAgentIdle         EventType = "agent.idle"
	EventMetricsUpdate     EventType = "metrics.update"
	EventSubAgentSpawn     EventType = "subagent.spawn"
	EventSubAgentComplete  EventType = "subagent.complete"
	EventContextCompress   EventType = "context.compress"
)

type Event struct {
	Type      EventType      `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	SessionID string         `json:"session_id,omitempty"`
	Data      map[string]any `json:"data"`
}

type Bus struct {
	subscribers map[chan Event]struct{}
	mu          sync.RWMutex
	bufSize     int
}

func NewBus(bufSize int) *Bus {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &Bus{
		subscribers: make(map[chan Event]struct{}),
		bufSize:     bufSize,
	}
}

func (b *Bus) Subscribe() chan Event {
	ch := make(chan Event, b.bufSize)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *Bus) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	delete(b.subscribers, ch)
	b.mu.Unlock()
}

func (b *Bus) Publish(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers {
		select {
		case ch <- event:
		default:
			// slow subscriber — drop event to avoid blocking
		}
	}
}
