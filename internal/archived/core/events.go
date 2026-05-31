package core

import "time"

// EventKind classifies events emitted on the bus.
type EventKind string

const (
	EventStart        EventKind = "start"
	EventLLMRequest   EventKind = "llm.request"
	EventLLMChunk     EventKind = "llm.chunk"
	EventLLMResponse  EventKind = "llm.response"
	EventToolRequest  EventKind = "tool.request"
	EventToolResult   EventKind = "tool.result"
	EventMessage      EventKind = "message"
	EventError        EventKind = "error"
	EventFinish       EventKind = "finish"
)

// Event is the unit of agentic telemetry. It is intentionally untyped on
// Payload — typed consumers should switch on Kind. This keeps the bus
// allocation-free for fast paths (chunks) while remaining expressive.
type Event struct {
	Kind    EventKind `json:"kind"`
	Time    time.Time `json:"time"`
	Turn    int       `json:"turn,omitempty"`
	Payload any       `json:"payload,omitempty"`
}

// EventSink consumes events. Implementations must be non-blocking.
type EventSink interface {
	Emit(Event)
}

// EventSinkFunc adapts a function to EventSink.
type EventSinkFunc func(Event)

func (f EventSinkFunc) Emit(e Event) { f(e) }

// MultiSink fans events out to a slice of sinks. Sinks are called serially
// in registration order — failure-isolation is the sink's responsibility.
type MultiSink []EventSink

func (m MultiSink) Emit(e Event) {
	for _, s := range m {
		if s == nil {
			continue
		}
		s.Emit(e)
	}
}

// nullSink is the default when no sink is configured.
type nullSink struct{}

func (nullSink) Emit(Event) {}

// NullSink is a safe zero-cost EventSink.
var NullSink EventSink = nullSink{}
