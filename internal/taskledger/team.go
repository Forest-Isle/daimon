package taskledger

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type Notification struct {
	FromTaskID string
	Type       string // "completed", "failed", "info"
	Message    string
}

type TeamResult struct {
	RootTaskID     string
	TasksCompleted int
	TasksFailed    int
	TasksCancelled int
	Summary        string
	Duration       time.Duration
}

// TaskExecutor runs a claimed task and returns a result summary or error.
type TaskExecutor func(ctx context.Context, task Task) (string, error)

type TeamCoordinator struct {
	ledger       TaskLedger
	maxWorkers   int
	executor     TaskExecutor
	notifyCh     chan Notification
	completionCh chan struct{} // fired when any task completes/fails/cancels — wakes blocked workers
	mu           sync.Mutex
}

func NewTeamCoordinator(ledger TaskLedger, maxWorkers int) *TeamCoordinator {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	return &TeamCoordinator{
		ledger:       ledger,
		maxWorkers:   maxWorkers,
		notifyCh:     make(chan Notification, 64),
		completionCh: make(chan struct{}, 64),
	}
}

func (tc *TeamCoordinator) SetExecutor(exec TaskExecutor) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.executor = exec
}

// Notify sends a non-blocking notification. Drops silently if the channel is full.
func (tc *TeamCoordinator) Notify(n Notification) {
	select {
	case tc.notifyCh <- n:
	default:
	}
}

func (tc *TeamCoordinator) AddTask(ctx context.Context, task Task) error {
	task.Kind = TaskKindTeamTask
	if task.State == "" {
		task.State = TaskStatePending
	}
	return tc.ledger.Register(ctx, task)
}

// RunWithExecutor launches worker goroutines that claim and execute tasks
// until all tasks are processed. Returns an aggregate TeamResult.
func (tc *TeamCoordinator) RunWithExecutor(ctx context.Context) (*TeamResult, error) {
	tc.mu.Lock()
	exec := tc.executor
	tc.mu.Unlock()
	if exec == nil {
		return nil, fmt.Errorf("no executor set")
	}

	start := time.Now()

	var wg sync.WaitGroup
	results := make([]teamWorkerStats, tc.maxWorkers)

	for i := range tc.maxWorkers {
		wg.Add(1)
		go func(workerIdx int) {
			defer wg.Done()
			workerID := fmt.Sprintf("worker-%d", workerIdx)
			tc.runWorker(ctx, workerID, exec, &results[workerIdx])
		}(i)
	}

	wg.Wait()

	var total TeamResult
	total.Duration = time.Since(start)
	for _, r := range results {
		total.TasksCompleted += r.completed
		total.TasksFailed += r.failed
		total.TasksCancelled += r.cancelled
	}
	total.Summary = fmt.Sprintf("%d completed, %d failed, %d cancelled",
		total.TasksCompleted, total.TasksFailed, total.TasksCancelled)
	return &total, nil
}

type teamWorkerStats struct {
	completed int
	failed    int
	cancelled int
}

func (tc *TeamCoordinator) runWorker(ctx context.Context, workerID string, exec TaskExecutor, stats *teamWorkerStats) {
	for {
		if ctx.Err() != nil {
			return
		}

		task, err := tc.ledger.ClaimNext(ctx, TaskKindTeamTask, workerID)
		if err != nil || task == nil {
			return
		}

		blocked, depFailed := tc.blockedByDeps(ctx, task)
		if depFailed {
			tc.cancelTask(ctx, task, "dependency failed or cancelled")
			stats.cancelled++
			tc.Notify(Notification{FromTaskID: task.ID, Type: "failed", Message: "dependency failed"})
			continue
		}
		if blocked {
			tc.putBack(ctx, task)
			// Event-driven: wait for any task completion signal instead of polling.
			// Falls back to a 5s heartbeat to guard against lost signals.
			select {
			case <-ctx.Done():
				return
			case <-tc.completionCh:
			case <-time.After(5 * time.Second):
			}
			continue
		}

		result, execErr := exec(ctx, *task)
		if execErr != nil {
			tc.failTask(ctx, task, execErr.Error())
			stats.failed++
			tc.Notify(Notification{FromTaskID: task.ID, Type: "failed", Message: execErr.Error()})
		} else {
			if strings.TrimSpace(result) == "" {
				slog.Warn("team: sub-agent returned empty result, task may be incomplete",
					"task_id", task.ID, "title", task.Title, "assignee", task.Assignee)
				result = fmt.Sprintf("[Sub-agent '%s' returned no result for task '%s'. The coordinator should consider this task as failed.]",
					task.Assignee, task.Title)
			}
			tc.completeTask(ctx, task, result)
			stats.completed++
			tc.Notify(Notification{FromTaskID: task.ID, Type: "completed", Message: result})
		}
	}
}

// blockedByDeps returns (blocked, depFailed). blocked is true when at least one
// dependency hasn't completed yet. depFailed is true when a dependency is in a
// terminal failure/cancelled state.
func (tc *TeamCoordinator) blockedByDeps(ctx context.Context, task *Task) (blocked, depFailed bool) {
	for _, depID := range task.DependsOn {
		dep, err := tc.ledger.Get(ctx, depID)
		if err != nil {
			return true, false
		}
		switch dep.State {
		case TaskStateFailed, TaskStateCancelled:
			return false, true
		case TaskStateCompleted:
			continue
		default:
			blocked = true
		}
	}
	return blocked, false
}

func (tc *TeamCoordinator) putBack(ctx context.Context, task *Task) {
	task.State = TaskStatePending
	task.Assignee = ""
	task.StartedAt = nil
	_ = tc.ledger.Update(ctx, *task)
}

func (tc *TeamCoordinator) completeTask(ctx context.Context, task *Task, result string) {
	now := time.Now().UTC()
	task.State = TaskStateCompleted
	task.CompletedAt = &now
	task.Result = result
	_ = tc.ledger.Update(ctx, *task)
	tc.signalCompletion()
}

func (tc *TeamCoordinator) failTask(ctx context.Context, task *Task, reason string) {
	now := time.Now().UTC()
	task.State = TaskStateFailed
	task.CompletedAt = &now
	task.Result = reason
	_ = tc.ledger.Update(ctx, *task)
	tc.signalCompletion()
}

func (tc *TeamCoordinator) cancelTask(ctx context.Context, task *Task, reason string) {
	_ = tc.ledger.Cancel(ctx, task.ID, reason)
	tc.signalCompletion()
}

// signalCompletion wakes blocked workers waiting for dependency resolution.
func (tc *TeamCoordinator) signalCompletion() {
	select {
	case tc.completionCh <- struct{}{}:
	default:
	}
}
