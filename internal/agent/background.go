package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// AgentState represents the lifecycle state of a background agent.
type AgentState string

const (
	StateRunning   AgentState = "running"
	StateCompleted AgentState = "completed"
	StateFailed    AgentState = "failed"
	StateCancelled AgentState = "cancelled"
)

// AgentStatus is a status update from a background agent.
type AgentStatus struct {
	AgentID   string
	AgentName string
	State     AgentState
	Progress  string
	UpdatedAt time.Time
}

// BackgroundAgent tracks a single background agent execution.
type BackgroundAgent struct {
	id     string
	spec   *AgentSpec
	state  AgentState
	result *AgentResult
	cancel context.CancelFunc
	doneCh chan struct{} // closed when agent finishes
	mu     sync.Mutex
}

// BackgroundManager manages all background (async) agent executions.
// Agents are launched via Spawn() and their results can be queried
// non-blockingly via GetResult() or waited on via Wait().
type BackgroundManager struct {
	mu       sync.RWMutex
	agents   map[string]*BackgroundAgent
	notifyCh chan AgentStatus // aggregated status notifications
}

// NewBackgroundManager creates a new BackgroundManager.
func NewBackgroundManager() *BackgroundManager {
	return &BackgroundManager{
		agents:   make(map[string]*BackgroundAgent),
		notifyCh: make(chan AgentStatus, 64),
	}
}

// NotifyCh returns the aggregated notification channel.
// Consumers can range over this to receive status updates from all background agents.
func (bm *BackgroundManager) NotifyCh() <-chan AgentStatus {
	return bm.notifyCh
}

// agentRunner is the function that actually executes the agent work.
// Abstracted for testability.
type agentRunner func(ctx context.Context) (*AgentResult, error)

// Spawn launches a background agent and returns its ID immediately.
// The runner function is executed in a new goroutine.
func (bm *BackgroundManager) Spawn(parentCtx context.Context, spec *AgentSpec, runner agentRunner) string {
	agentID := fmt.Sprintf("bg_%s_%d", spec.Name, time.Now().UnixNano())

	ctx, cancel := context.WithCancel(parentCtx)

	ba := &BackgroundAgent{
		id:     agentID,
		spec:   spec,
		state:  StateRunning,
		cancel: cancel,
		doneCh: make(chan struct{}),
	}

	bm.mu.Lock()
	bm.agents[agentID] = ba
	bm.mu.Unlock()

	// Send running notification
	bm.sendStatus(AgentStatus{
		AgentID:   agentID,
		AgentName: spec.Name,
		State:     StateRunning,
		UpdatedAt: time.Now(),
	})

	go func() {
		defer close(ba.doneCh)

		result, err := runner(ctx)

		ba.mu.Lock()
		if ctx.Err() == context.Canceled {
			ba.state = StateCancelled
			ba.result = &AgentResult{AgentName: spec.Name, Error: fmt.Errorf("cancelled")}
		} else if err != nil {
			ba.state = StateFailed
			ba.result = &AgentResult{AgentName: spec.Name, Error: err}
		} else {
			ba.state = StateCompleted
			ba.result = result
		}
		finalState := ba.state
		ba.mu.Unlock()

		bm.sendStatus(AgentStatus{
			AgentID:   agentID,
			AgentName: spec.Name,
			State:     finalState,
			UpdatedAt: time.Now(),
		})

		slog.Info("background agent finished",
			"agent_id", agentID,
			"agent", spec.Name,
			"state", finalState,
		)
	}()

	slog.Info("background agent spawned",
		"agent_id", agentID,
		"agent", spec.Name,
	)

	return agentID
}

// GetResult returns the result of a background agent, or nil if still running.
// The second return value indicates whether the agent has finished.
func (bm *BackgroundManager) GetResult(agentID string) (*AgentResult, bool) {
	bm.mu.RLock()
	ba, ok := bm.agents[agentID]
	bm.mu.RUnlock()

	if !ok {
		return nil, false
	}

	ba.mu.Lock()
	defer ba.mu.Unlock()

	if ba.state == StateRunning {
		return nil, false
	}
	return ba.result, true
}

// Wait blocks until the specified background agent finishes or the context is cancelled.
func (bm *BackgroundManager) Wait(ctx context.Context, agentID string) (*AgentResult, error) {
	bm.mu.RLock()
	ba, ok := bm.agents[agentID]
	bm.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown background agent: %s", agentID)
	}

	select {
	case <-ba.doneCh:
		ba.mu.Lock()
		defer ba.mu.Unlock()
		return ba.result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Cancel cancels a running background agent.
func (bm *BackgroundManager) Cancel(agentID string) error {
	bm.mu.RLock()
	ba, ok := bm.agents[agentID]
	bm.mu.RUnlock()

	if !ok {
		return fmt.Errorf("unknown background agent: %s", agentID)
	}

	ba.cancel()
	return nil
}

// List returns the current state of all background agents.
func (bm *BackgroundManager) List() []AgentStatus {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	var statuses []AgentStatus
	for _, ba := range bm.agents {
		ba.mu.Lock()
		statuses = append(statuses, AgentStatus{
			AgentID:   ba.id,
			AgentName: ba.spec.Name,
			State:     ba.state,
			UpdatedAt: time.Now(),
		})
		ba.mu.Unlock()
	}
	return statuses
}

// Cleanup removes completed/failed/cancelled agents from the manager.
func (bm *BackgroundManager) Cleanup() int {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	removed := 0
	for id, ba := range bm.agents {
		ba.mu.Lock()
		state := ba.state
		ba.mu.Unlock()

		if state != StateRunning {
			delete(bm.agents, id)
			removed++
		}
	}
	return removed
}

func (bm *BackgroundManager) sendStatus(status AgentStatus) {
	select {
	case bm.notifyCh <- status:
	default:
		// Channel full, drop oldest
		slog.Warn("background manager notification channel full, dropping status",
			"agent_id", status.AgentID,
			"state", status.State,
		)
	}
}
