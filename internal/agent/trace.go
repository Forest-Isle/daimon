package agent

import (
	"fmt"
	"sync"
	"time"
)

// Trace represents a single execution trace for an agent or tool call.
type Trace struct {
	ID        string
	ParentID  string
	AgentName string
	Input     string
	Output    string
	Error     string
	StartedAt time.Time
	EndedAt   time.Time
	Children  []*Trace
}

// TraceCollector collects execution traces in a tree structure.
type TraceCollector struct {
	mu     sync.Mutex
	traces map[string]*Trace // trace ID → trace
	root   *Trace
}

// NewTraceCollector creates a new TraceCollector.
func NewTraceCollector(rootID, rootAgent string) *TraceCollector {
	root := &Trace{
		ID:        rootID,
		AgentName: rootAgent,
		StartedAt: time.Now(),
	}
	return &TraceCollector{
		traces: map[string]*Trace{rootID: root},
		root:   root,
	}
}

// StartTrace creates a new trace and returns its ID.
func (tc *TraceCollector) StartTrace(parentID, agentName, input string) string {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	traceID := generateTraceID()
	trace := &Trace{
		ID:        traceID,
		ParentID:  parentID,
		AgentName: agentName,
		Input:     input,
		StartedAt: time.Now(),
	}

	tc.traces[traceID] = trace

	// Add to parent's children
	if parent, ok := tc.traces[parentID]; ok {
		parent.Children = append(parent.Children, trace)
	}

	return traceID
}

// EndTrace marks a trace as completed.
func (tc *TraceCollector) EndTrace(traceID, output, errorMsg string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if trace, ok := tc.traces[traceID]; ok {
		trace.Output = output
		trace.Error = errorMsg
		trace.EndedAt = time.Now()
	}
}

// Root returns the root trace.
func (tc *TraceCollector) Root() *Trace {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.root
}

// AllTraces returns all traces as a flat list.
func (tc *TraceCollector) AllTraces() []*Trace {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	traces := make([]*Trace, 0, len(tc.traces))
	for _, t := range tc.traces {
		traces = append(traces, t)
	}
	return traces
}

func generateTraceID() string {
	return fmt.Sprintf("trace_%d", time.Now().UnixNano())
}
