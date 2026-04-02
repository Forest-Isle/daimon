package agent

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
)

// AgentTask represents a single task to be dispatched to a sub-agent.
type AgentTask struct {
	ID        string   // unique task identifier
	AgentName string   // name of the agent to invoke
	Task      string   // task description / prompt
	Context   string   // optional context from upstream tasks
	DependsOn []string // IDs of tasks that must complete first
}

// AgentResult captures the outcome of a sub-agent execution.
type AgentResult struct {
	AgentName  string
	TaskID     string
	Output     string
	Error      error
	Duration   time.Duration
	TokenUsage TokenUsage
}

// TokenUsage tracks token consumption for a single agent invocation.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
}

// agentExecutor is the function signature for executing a single agent task.
type agentExecutor func(ctx context.Context, task AgentTask) (*AgentResult, error)

// AgentOrchestrator schedules and executes multiple agents in parallel or
// according to a dependency DAG.
type AgentOrchestrator struct {
	manager     *AgentManager
	maxParallel int
}

// NewAgentOrchestrator creates a new orchestrator.
func NewAgentOrchestrator(manager *AgentManager, maxParallel int) *AgentOrchestrator {
	if maxParallel <= 0 {
		maxParallel = 4
	}
	return &AgentOrchestrator{
		manager:     manager,
		maxParallel: maxParallel,
	}
}

// ExecuteParallel runs all tasks concurrently (up to maxParallel).
// Individual failures do not abort other tasks.
func (o *AgentOrchestrator) ExecuteParallel(
	ctx context.Context,
	tasks []AgentTask,
	executor agentExecutor,
) ([]*AgentResult, error) {
	results := make([]*AgentResult, len(tasks))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(o.maxParallel)

	for i, task := range tasks {
		i, task := i, task
		g.Go(func() error {
			result, err := executor(gctx, task)
			if err != nil {
				results[i] = &AgentResult{
					AgentName: task.AgentName,
					TaskID:    task.ID,
					Error:     err,
				}
				return nil
			}
			result.TaskID = task.ID
			results[i] = result
			return nil
		})
	}

	_ = g.Wait()
	return results, nil
}

// ExecuteDAG runs tasks respecting dependency ordering.
// Tasks are sorted topologically, then each layer is executed in parallel.
func (o *AgentOrchestrator) ExecuteDAG(
	ctx context.Context,
	tasks []AgentTask,
	executor agentExecutor,
) ([]*AgentResult, error) {
	layers, err := TopologicalSort(tasks)
	if err != nil {
		return nil, fmt.Errorf("orchestrator DAG sort: %w", err)
	}

	var allResults []*AgentResult
	for _, layer := range layers {
		layerResults, err := o.ExecuteParallel(ctx, layer, executor)
		if err != nil {
			return allResults, err
		}
		allResults = append(allResults, layerResults...)
	}
	return allResults, nil
}

// TopologicalSort arranges tasks into layers based on their DependsOn fields.
// Tasks in the same layer have no dependencies on each other and can run in parallel.
// Returns an error if a cycle is detected.
func TopologicalSort(tasks []AgentTask) ([][]AgentTask, error) {
	taskMap := make(map[string]*AgentTask, len(tasks))
	inDegree := make(map[string]int, len(tasks))
	dependents := make(map[string][]string)

	for i := range tasks {
		t := &tasks[i]
		taskMap[t.ID] = t
		inDegree[t.ID] = 0
	}

	for i := range tasks {
		t := &tasks[i]
		for _, dep := range t.DependsOn {
			if _, ok := taskMap[dep]; !ok {
				return nil, fmt.Errorf("task %q depends on unknown task %q", t.ID, dep)
			}
			inDegree[t.ID]++
			dependents[dep] = append(dependents[dep], t.ID)
		}
	}

	var layers [][]AgentTask
	processed := 0

	var currentLayer []AgentTask
	for id, deg := range inDegree {
		if deg == 0 {
			currentLayer = append(currentLayer, *taskMap[id])
		}
	}

	for len(currentLayer) > 0 {
		layers = append(layers, currentLayer)
		processed += len(currentLayer)

		var nextLayer []AgentTask
		for _, t := range currentLayer {
			for _, depID := range dependents[t.ID] {
				inDegree[depID]--
				if inDegree[depID] == 0 {
					nextLayer = append(nextLayer, *taskMap[depID])
				}
			}
		}
		currentLayer = nextLayer
	}

	if processed != len(tasks) {
		return nil, fmt.Errorf("dependency cycle detected: %d of %d tasks processed", processed, len(tasks))
	}

	return layers, nil
}
