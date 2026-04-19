package agent

import (
	"fmt"
	"strings"
	"sync"
)

// TaskContext holds shared state for multi-agent collaboration within a single task execution.
// It enables sub-agents to pass results to downstream tasks via dependency chains.
type TaskContext struct {
	mu         sync.RWMutex
	ID         string                    // unique task context ID
	Goal       string                    // original user goal
	Results    map[string]SubAgentResult // subtask ID → result
	SharedData map[string]string         // arbitrary KV store for cross-agent data
}

// NewTaskContext creates a new TaskContext for a given goal.
func NewTaskContext(id, goal string) *TaskContext {
	return &TaskContext{
		ID:         id,
		Goal:       goal,
		Results:    make(map[string]SubAgentResult),
		SharedData: make(map[string]string),
	}
}

// SetResult stores the result of a subtask execution.
func (tc *TaskContext) SetResult(taskID string, result SubAgentResult) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.Results[taskID] = result
}

// GetResult retrieves the result of a subtask by ID.
func (tc *TaskContext) GetResult(taskID string) (SubAgentResult, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	r, ok := tc.Results[taskID]
	return r, ok
}

// SetShared stores a key-value pair in the shared data store.
func (tc *TaskContext) SetShared(key, value string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.SharedData[key] = value
}

// GetShared retrieves a value from the shared data store.
func (tc *TaskContext) GetShared(key string) (string, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	v, ok := tc.SharedData[key]
	return v, ok
}

// BuildContextForTask collects outputs from all dependency tasks and formats them
// as a context string to be injected into the current task's input.
func (tc *TaskContext) BuildContextForTask(taskID string, plan *TaskPlan) string {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	// Find the subtask
	var subtask *SubTask
	for _, st := range plan.SubTasks {
		if st.ID == taskID {
			subtask = st
			break
		}
	}
	if subtask == nil || len(subtask.DependsOn) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Context from previous tasks:\n\n")

	for _, depID := range subtask.DependsOn {
		result, ok := tc.Results[depID]
		if !ok {
			continue
		}
		_, _ = fmt.Fprintf(&sb, "--- Task %s (%s) ---\n", depID, result.AgentName)
		if result.Error != "" {
			_, _ = fmt.Fprintf(&sb, "Error: %s\n", result.Error)
		} else {
			sb.WriteString(result.Output)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

