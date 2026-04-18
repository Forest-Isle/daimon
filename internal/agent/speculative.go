package agent

import (
	"context"
	"sync"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// speculativeResult holds the outcome of a speculatively launched tool execution.
type speculativeResult struct {
	toolUseID string
	toolName  string
	result    *tool.Result
	err       error
	done      chan struct{}
	cancel    context.CancelFunc
	cancelled bool
}

// SpeculativeExecutor launches read-only tools during streaming (before the
// model finishes its response) and caches results for later collection.
type SpeculativeExecutor struct {
	registry    *tool.Registry
	maxInFlight int
	results     map[string]*speculativeResult
	inFlight    int
	mu          sync.Mutex
}

func NewSpeculativeExecutor(registry *tool.Registry, maxInFlight int) *SpeculativeExecutor {
	if maxInFlight <= 0 {
		maxInFlight = 3
	}
	return &SpeculativeExecutor{
		registry:    registry,
		maxInFlight: maxInFlight,
		results:     make(map[string]*speculativeResult),
	}
}

// TryLaunch attempts to speculatively execute a read-only tool in a goroutine.
// Returns true if the tool was launched, false if rejected (non-existent, not
// read-only, at capacity, or duplicate ID).
func (se *SpeculativeExecutor) TryLaunch(ctx context.Context, toolUseID, toolName, input string) bool {
	t, err := se.registry.Get(toolName)
	if err != nil {
		return false
	}
	if !tool.IsToolReadOnly(t) {
		return false
	}

	se.mu.Lock()
	if se.inFlight >= se.maxInFlight {
		se.mu.Unlock()
		return false
	}
	if _, exists := se.results[toolUseID]; exists {
		se.mu.Unlock()
		return false
	}

	execCtx, cancel := context.WithCancel(ctx)
	sr := &speculativeResult{
		toolUseID: toolUseID,
		toolName:  toolName,
		done:      make(chan struct{}),
		cancel:    cancel,
	}
	se.results[toolUseID] = sr
	se.inFlight++
	se.mu.Unlock()

	go func() {
		defer func() {
			se.mu.Lock()
			se.inFlight--
			se.mu.Unlock()
			close(sr.done)
		}()

		result, execErr := t.Execute(execCtx, []byte(input))

		se.mu.Lock()
		if sr.cancelled {
			se.mu.Unlock()
			return
		}
		sr.result = &result
		sr.err = execErr
		se.mu.Unlock()
	}()

	return true
}

// Collect returns the result of a speculatively executed tool.
// If the tool finished: returns (result, error).
// If still running or unknown: returns (nil, nil) — caller should execute normally.
func (se *SpeculativeExecutor) Collect(toolUseID string) (*tool.Result, error) {
	se.mu.Lock()
	sr, exists := se.results[toolUseID]
	se.mu.Unlock()

	if !exists {
		return nil, nil
	}

	select {
	case <-sr.done:
	default:
		return nil, nil
	}

	se.mu.Lock()
	defer se.mu.Unlock()
	if sr.cancelled {
		return nil, nil
	}
	return sr.result, sr.err
}

// CancelAll cancels all in-flight goroutines and marks them as cancelled.
func (se *SpeculativeExecutor) CancelAll() {
	se.mu.Lock()
	defer se.mu.Unlock()
	for _, sr := range se.results {
		sr.cancelled = true
		sr.cancel()
	}
}

// Reset cancels all in-flight work, then clears the results map.
func (se *SpeculativeExecutor) Reset() {
	se.CancelAll()
	se.mu.Lock()
	defer se.mu.Unlock()
	se.results = make(map[string]*speculativeResult)
	se.inFlight = 0
}
