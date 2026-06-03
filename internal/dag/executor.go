// Package dag provides a zero-dependency DAG (Directed Acyclic Graph) executor.
// It handles topological scheduling, parallel execution via a worker pool,
// and failure propagation — tasks whose dependencies fail are automatically skipped.
//
// The package extracts the worker-pool + channel + semaphore pattern from the
// agent's ACT phase, but removes all agent/tool/hook/permission dependencies.
// The execute function is injected via a callback (ExecuteFunc), making the
// executor fully independent of IronClaw internals.
package dag

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Task represents a single unit of work in a DAG.
// DependsOn lists the IDs of tasks that must complete before this one can start.
type Task struct {
	ID          string
	Description string
	ToolName    string
	ToolInput   string
	DependsOn   []string
}

// ResultStatus describes the outcome of a task execution.
type ResultStatus string

const (
	StatusDone    ResultStatus = "done"
	StatusFailed  ResultStatus = "failed"
	StatusSkipped ResultStatus = "skipped"
)

// Result holds the outcome of executing a single task.
type Result struct {
	TaskID     string
	Output     string
	Error      string
	DurationMs int64
	Status     ResultStatus
}

// ExecuteFunc is the callback that runs a single task.
// The executor calls this for each task that becomes ready (all deps satisfied).
// The returned Result should have Status set to StatusDone or StatusFailed.
type ExecuteFunc func(ctx context.Context, t Task) Result

// taskState tracks the runtime state of a single task during execution.
type taskState struct {
	status   ResultStatus
	result   *Result // populated after execution or when skipped
	enqueued bool    // true if the task has been fed into readyCh
}

// Execute runs tasks with topological scheduling and parallel execution.
//
//Tasks with no dependencies are executed first. After each task completes,
// any pending tasks whose dependencies are all satisfied become ready.
// Failed tasks cause all downstream (transitively dependent) tasks to be
// skipped with StatusSkipped.
//
// Tasks that depend on IDs not present in the task set are marked as skipped
// (they can never become ready).
//
// maxParallel controls the maximum number of concurrently executing tasks.
// If maxParallel <= 0, it defaults to 1.
//
// The returned slice contains one Result per task that reached a terminal
// state (done, failed, or skipped), in completion order.
func Execute(ctx context.Context, tasks []Task, exec ExecuteFunc, maxParallel int) []Result {
	if maxParallel <= 0 {
		maxParallel = 1
	}
	total := len(tasks)
	if total == 0 {
		return nil
	}

	var (
		mu        sync.Mutex
		states    = make(map[string]*taskState, total)
		results   []Result
		doneCount int32
		closeOnce sync.Once
	)
	for i := range tasks {
		states[tasks[i].ID] = &taskState{}
	}

	taskIndex := buildTaskIndex(tasks)
	readyCh := make(chan string, total)
	sem := make(chan struct{}, maxParallel)

	seedReady(tasks, states, &mu, readyCh)

	// Resolve tasks that can never become ready due to missing dependencies.
	if int(atomic.LoadInt32(&doneCount)) == 0 {
		mu.Lock()
		markStuckLocked(tasks, states, taskIndex, &results, &doneCount)
		mu.Unlock()
	}
	if int(atomic.LoadInt32(&doneCount)) == total {
		close(readyCh)
		return results
	}

	workerCount := maxParallel
	if workerCount > total {
		workerCount = total
	}
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go runWorker(ctx, &wg, readyCh, sem, exec, taskIndex, tasks,
			states, &mu, &results, &doneCount, &closeOnce, total)
	}
	wg.Wait()
	return results
}

// buildTaskIndex creates a map from task ID to task pointer for O(1) lookups.
func buildTaskIndex(tasks []Task) map[string]*Task {
	idx := make(map[string]*Task, len(tasks))
	for i := range tasks {
		idx[tasks[i].ID] = &tasks[i]
	}
	return idx
}

// seedReady pushes tasks with no dependencies into the ready channel.
// Must be called before any workers start.
func seedReady(tasks []Task, states map[string]*taskState, mu *sync.Mutex, readyCh chan<- string) {
	mu.Lock()
	defer mu.Unlock()
	for i := range tasks {
		st := states[tasks[i].ID]
		if st.status != "" {
			continue
		}
		if len(tasks[i].DependsOn) == 0 {
			st.enqueued = true
			readyCh <- tasks[i].ID
		}
	}
}

// runWorker pulls ready task IDs from readyCh, executes them, and finalizes
// results. It exits when the context is cancelled or the channel is closed.
func runWorker(
	ctx context.Context,
	wg *sync.WaitGroup,
	readyCh chan string,
	sem chan struct{},
	exec ExecuteFunc,
	taskIndex map[string]*Task,
	tasks []Task,
	states map[string]*taskState,
	mu *sync.Mutex,
	results *[]Result,
	doneCount *int32,
	closeOnce *sync.Once,
	total int,
) {
	defer wg.Done()
	for {
		taskID, ok := nextTask(ctx, readyCh)
		if !ok {
			return
		}

		mu.Lock()
		st := states[taskID]
		if st.status == StatusSkipped {
			exit := recordSkippedLocked(st, taskID, results, doneCount, readyCh, closeOnce, total)
			mu.Unlock()
			if exit {
				return
			}
			continue
		}
		mu.Unlock()

		exit := processTask(ctx, taskID, sem, exec, taskIndex, tasks,
			states, mu, results, doneCount, readyCh, closeOnce, total)
		if exit {
			return
		}
	}
}

// nextTask blocks until a task ID is available from readyCh or the context is
// done. Returns (taskID, false) when the context is cancelled or the channel
// is closed.
func nextTask(ctx context.Context, readyCh chan string) (string, bool) {
	select {
	case <-ctx.Done():
		return "", false
	case taskID, ok := <-readyCh:
		return taskID, ok
	}
}

// recordSkippedLocked records a Result for an already-skipped task without
// executing it. Returns true if the caller should exit. Caller must hold mu.
func recordSkippedLocked(
	st *taskState,
	taskID string,
	results *[]Result,
	doneCount *int32,
	readyCh chan string,
	closeOnce *sync.Once,
	total int,
) (exit bool) {
	st.result = &Result{
		TaskID: taskID,
		Status: StatusSkipped,
		Error:  "skipped due to upstream failure",
	}
	*results = append(*results, *st.result)
	n := int(atomic.AddInt32(doneCount, 1))
	return tryClose(n, total, readyCh, closeOnce)
}

// processTask executes a single task (with semaphore), records the result,
// propagates failures to downstream tasks, detects stuck tasks, and feeds
// newly-ready tasks to the channel. Returns true if the caller should exit.
func processTask(
	ctx context.Context,
	taskID string,
	sem chan struct{},
	exec ExecuteFunc,
	taskIndex map[string]*Task,
	tasks []Task,
	states map[string]*taskState,
	mu *sync.Mutex,
	results *[]Result,
	doneCount *int32,
	readyCh chan string,
	closeOnce *sync.Once,
	total int,
) (exit bool) {
	// Acquire semaphore (block until a slot is free).
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return true
	}

	task := taskIndex[taskID]
	start := time.Now()
	result := exec(ctx, *task)
	result.DurationMs = time.Since(start).Milliseconds()
	if result.TaskID == "" {
		result.TaskID = taskID
	}
	<-sem

	mu.Lock()
	defer mu.Unlock()

	st := states[taskID]
	st.status = result.Status
	st.result = &result
	*results = append(*results, result)
	_ = int(atomic.AddInt32(doneCount, 1))

	if result.Status == StatusFailed {
		markDownstreamSkipped(taskID, tasks, taskIndex, states, results, doneCount)
	}

	feedReadyLocked(tasks, states, readyCh, taskIndex)

	if int(atomic.LoadInt32(doneCount)) < total {
		markStuckLocked(tasks, states, taskIndex, results, doneCount)
	}

	return tryClose(int(atomic.LoadInt32(doneCount)), total, readyCh, closeOnce)
}

// tryClose closes readyCh via closeOnce if doneCount has reached total.
// Returns true if the channel was actually closed, signaling the caller to exit.
func tryClose(done, total int, readyCh chan string, closeOnce *sync.Once) bool {
	if done < total {
		return false
	}
	closed := false
	closeOnce.Do(func() {
		close(readyCh)
		closed = true
	})
	return closed
}

// markDownstreamSkipped transitively marks all tasks that depend on failedID
// as Skipped and appends their results. doneCount is incremented for each.
// Caller must hold mu.
func markDownstreamSkipped(
	failedID string,
	tasks []Task,
	taskIndex map[string]*Task,
	states map[string]*taskState,
	results *[]Result,
	doneCount *int32,
) {
	skipped := map[string]bool{failedID: true}
	changed := true
	for changed {
		changed = false
		for i := range tasks {
			st := states[tasks[i].ID]
			if st.status != "" {
				continue
			}
			for _, depID := range tasks[i].DependsOn {
				if skipped[depID] {
					st.status = StatusSkipped
					st.result = &Result{
						TaskID: tasks[i].ID,
						Status: StatusSkipped,
						Error:  fmt.Sprintf("skipped: upstream task %q failed", failedID),
					}
					*results = append(*results, *st.result)
					atomic.AddInt32(doneCount, 1)
					skipped[tasks[i].ID] = true
					changed = true
					break
				}
			}
		}
	}
}

// feedReadyLocked checks all pending tasks and pushes those whose dependencies
// are now all satisfied into the ready channel. Uses enqueued flag to prevent
// double-pushing by concurrent workers. Caller must hold mu.
func feedReadyLocked(
	tasks []Task,
	states map[string]*taskState,
	readyCh chan string,
	taskIndex map[string]*Task,
) {
	for i := range tasks {
		st := states[tasks[i].ID]
		if st.status != "" || st.enqueued {
			continue
		}
		if len(tasks[i].DependsOn) == 0 {
			continue
		}
		if !allDepsSatisfied(tasks[i].DependsOn, states) {
			continue
		}
		st.enqueued = true
		select {
		case readyCh <- tasks[i].ID:
		default:
		}
	}
}

// markStuckLocked finds pending tasks that depend on IDs not present in the
// task set and marks them as skipped, since they can never become ready.
// Caller must hold mu.
func markStuckLocked(
	tasks []Task,
	states map[string]*taskState,
	taskIndex map[string]*Task,
	results *[]Result,
	doneCount *int32,
) {
	for i := range tasks {
		st := states[tasks[i].ID]
		if st.status != "" {
			continue
		}
		if hasUnsatisfiableDep(tasks[i].DependsOn, taskIndex) {
			st.status = StatusSkipped
			st.result = &Result{
				TaskID: tasks[i].ID,
				Status: StatusSkipped,
				Error:  "skipped: dependency not found in task set",
			}
			*results = append(*results, *st.result)
			atomic.AddInt32(doneCount, 1)
		}
	}
}

// hasUnsatisfiableDep returns true if any depID is not present in taskIndex,
// meaning the task can never become ready.
func hasUnsatisfiableDep(depIDs []string, taskIndex map[string]*Task) bool {
	for _, depID := range depIDs {
		if _, ok := taskIndex[depID]; !ok {
			return true
		}
	}
	return false
}

// allDepsSatisfied returns true when every dependency of a task has completed
// (either done or skipped).
func allDepsSatisfied(depIDs []string, states map[string]*taskState) bool {
	for _, depID := range depIDs {
		st, ok := states[depID]
		if !ok {
			return false
		}
		if st.status != StatusDone && st.status != StatusSkipped {
			return false
		}
	}
	return true
}

// FormatResults returns a human-readable summary of execution results.
func FormatResults(results []Result) string {
	if len(results) == 0 {
		return "No tasks executed."
	}

	var b strings.Builder
	b.WriteString("Results:\n")
	for i, r := range results {
		fmt.Fprintf(&b, "  %d. %s: %s", i+1, r.TaskID, statusLabel(r.Status))
		if r.Status == StatusDone || r.Status == StatusFailed {
			fmt.Fprintf(&b, " (%dms)", r.DurationMs)
		}
		if r.Status == StatusFailed && r.Error != "" {
			fmt.Fprintf(&b, " - %s", r.Error)
		}
		if r.Status == StatusSkipped && r.Error != "" {
			fmt.Fprintf(&b, " - %s", r.Error)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func statusLabel(s ResultStatus) string {
	switch s {
	case StatusDone:
		return "done"
	case StatusFailed:
		return "failed"
	case StatusSkipped:
		return "skipped"
	default:
		return string(s)
	}
}
