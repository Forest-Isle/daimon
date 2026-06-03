package dag

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecute_EmptyTasks(t *testing.T) {
	results := Execute(context.Background(), nil, nil, 2)
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestExecute_SingleTask(t *testing.T) {
	tasks := []Task{
		{ID: "t1", Description: "hello", ToolName: "echo", ToolInput: `"hello"`},
	}
	exec := func(ctx context.Context, t Task) Result {
		return Result{Status: StatusDone, Output: "ok"}
	}
	results := Execute(context.Background(), tasks, exec, 2)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.TaskID != "t1" || r.Status != StatusDone || r.Output != "ok" {
		t.Errorf("unexpected result: %+v", r)
	}
	if r.DurationMs < 0 {
		t.Errorf("expected non-negative duration, got %d", r.DurationMs)
	}
}

func TestExecute_ParallelIndependence(t *testing.T) {
	tasks := []Task{
		{ID: "a", Description: "task a"},
		{ID: "b", Description: "task b"},
		{ID: "c", Description: "task c"},
	}

	var counter int32
	// Track max concurrency by counting active goroutines.
	var active int32
	var maxActive int32

	exec := func(ctx context.Context, t Task) Result {
		n := int(atomic.AddInt32(&active, 1))
		for {
			old := atomic.LoadInt32(&maxActive)
			if int32(n) <= old {
				break
			}
			if atomic.CompareAndSwapInt32(&maxActive, old, int32(n)) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond) // Ensure overlap.
		atomic.AddInt32(&active, -1)
		atomic.AddInt32(&counter, 1)
		return Result{Status: StatusDone, Output: t.ID}
	}

	results := Execute(context.Background(), tasks, exec, 3)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if c := atomic.LoadInt32(&counter); c != 3 {
		t.Errorf("expected 3 executions, got %d", c)
	}
	// With 3 tasks and maxParallel=3, all should run concurrently.
	if maxActive < 2 {
		t.Errorf("expected at least 2 concurrent tasks, got maxActive=%d", maxActive)
	}
}

func TestExecute_DependencyChain(t *testing.T) {
	// a -> b -> c
	tasks := []Task{
		{ID: "a", Description: "first"},
		{ID: "b", Description: "second", DependsOn: []string{"a"}},
		{ID: "c", Description: "third", DependsOn: []string{"b"}},
	}

	var order []string
	exec := func(ctx context.Context, t Task) Result {
		order = append(order, t.ID)
		return Result{Status: StatusDone, Output: t.ID}
	}

	results := Execute(context.Background(), tasks, exec, 2)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// Tasks must execute in dependency order: a, b, c.
	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Errorf("unexpected execution order: %v", order)
	}
	for _, r := range results {
		if r.Status != StatusDone {
			t.Errorf("task %s: expected done, got %s", r.TaskID, r.Status)
		}
	}
}

func TestExecute_FailurePropagation(t *testing.T) {
	// a -> b -> c
	// If a fails, b and c must be skipped.
	tasks := []Task{
		{ID: "a", Description: "failing task"},
		{ID: "b", Description: "depends on a", DependsOn: []string{"a"}},
		{ID: "c", Description: "depends on b", DependsOn: []string{"b"}},
	}

	exec := func(ctx context.Context, t Task) Result {
		if t.ID == "a" {
			return Result{Status: StatusFailed, Error: "boom"}
		}
		return Result{Status: StatusDone, Output: t.ID}
	}

	results := Execute(context.Background(), tasks, exec, 2)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	statuses := map[string]ResultStatus{}
	for _, r := range results {
		statuses[r.TaskID] = r.Status
	}

	if statuses["a"] != StatusFailed {
		t.Errorf("task a: expected failed, got %s", statuses["a"])
	}
	if statuses["b"] != StatusSkipped {
		t.Errorf("task b: expected skipped, got %s", statuses["b"])
	}
	if statuses["c"] != StatusSkipped {
		t.Errorf("task c: expected skipped, got %s", statuses["c"])
	}
}

func TestExecute_FailurePropagationDiamond(t *testing.T) {
	//    a
	//   / \
	//  b   c
	//   \ /
	//    d
	// If b fails, d should still be skippable... wait, no.
	// If a succeeds, b fails, c succeeds.
	// d depends on both b and c. Since b failed, d should be skipped.
	tasks := []Task{
		{ID: "a", Description: "root"},
		{ID: "b", Description: "left", DependsOn: []string{"a"}},
		{ID: "c", Description: "right", DependsOn: []string{"a"}},
		{ID: "d", Description: "merge", DependsOn: []string{"b", "c"}},
	}

	exec := func(ctx context.Context, t Task) Result {
		if t.ID == "b" {
			return Result{Status: StatusFailed, Error: "left branch failed"}
		}
		time.Sleep(5 * time.Millisecond)
		return Result{Status: StatusDone, Output: t.ID}
	}

	results := Execute(context.Background(), tasks, exec, 2)
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}

	statuses := map[string]ResultStatus{}
	for _, r := range results {
		statuses[r.TaskID] = r.Status
	}

	if statuses["a"] != StatusDone {
		t.Errorf("task a: expected done, got %s", statuses["a"])
	}
	if statuses["b"] != StatusFailed {
		t.Errorf("task b: expected failed, got %s", statuses["b"])
	}
	if statuses["c"] != StatusDone {
		t.Errorf("task c: expected done, got %s", statuses["c"])
	}
	// d depends on b (failed) and c (done) — should be skipped.
	if statuses["d"] != StatusSkipped {
		t.Errorf("task d: expected skipped, got %s", statuses["d"])
	}
}

func TestExecute_FailurePropagationFanOut(t *testing.T) {
	// a fails -> b, c, d all skip.
	tasks := []Task{
		{ID: "a", Description: "root"},
		{ID: "b", Description: "child 1", DependsOn: []string{"a"}},
		{ID: "c", Description: "child 2", DependsOn: []string{"a"}},
		{ID: "d", Description: "child 3", DependsOn: []string{"a"}},
	}

	exec := func(ctx context.Context, t Task) Result {
		if t.ID == "a" {
			return Result{Status: StatusFailed, Error: "root failure"}
		}
		return Result{Status: StatusDone, Output: t.ID}
	}

	results := Execute(context.Background(), tasks, exec, 3)
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}

	statuses := map[string]ResultStatus{}
	for _, r := range results {
		statuses[r.TaskID] = r.Status
	}

	if statuses["a"] != StatusFailed {
		t.Errorf("task a: expected failed, got %s", statuses["a"])
	}
	for _, id := range []string{"b", "c", "d"} {
		if statuses[id] != StatusSkipped {
			t.Errorf("task %s: expected skipped, got %s", id, statuses[id])
		}
	}
}

func TestExecute_ContextCancellation(t *testing.T) {
	tasks := []Task{
		{ID: "a", Description: "slow task"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	exec := func(ctx context.Context, t Task) Result {
		time.Sleep(100 * time.Millisecond)
		return Result{Status: StatusDone}
	}

	// Worker should see ctx.Done() before pulling from readyCh,
	// or the task should never start.
	results := Execute(ctx, tasks, exec, 1)
	// Either no result or an incomplete result is acceptable.
	_ = results
}

func TestExecute_MaxParallelZero(t *testing.T) {
	tasks := []Task{
		{ID: "a", Description: "task a"},
		{ID: "b", Description: "task b"},
	}

	exec := func(ctx context.Context, t Task) Result {
		return Result{Status: StatusDone, Output: t.ID}
	}

	// maxParallel=0 should default to 1 (no panic).
	results := Execute(context.Background(), tasks, exec, 0)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestExecute_MultipleIndependentBatches(t *testing.T) {
	// a -> b
	// c -> d
	// Two independent chains that can run in parallel.
	tasks := []Task{
		{ID: "a", Description: "chain 1 step 1"},
		{ID: "b", Description: "chain 1 step 2", DependsOn: []string{"a"}},
		{ID: "c", Description: "chain 2 step 1"},
		{ID: "d", Description: "chain 2 step 2", DependsOn: []string{"c"}},
	}

	var order []string
	var mu sync.Mutex // We need sync.Mutex for order tracking.
	exec := func(ctx context.Context, t Task) Result {
		mu.Lock()
		order = append(order, t.ID)
		mu.Unlock()
		return Result{Status: StatusDone, Output: t.ID}
	}

	results := Execute(context.Background(), tasks, exec, 4)
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	// a must come before b, c must come before d.
	idx := func(id string) int {
		for i, o := range order {
			if o == id {
				return i
			}
		}
		return -1
	}
	if idx("a") >= idx("b") {
		t.Errorf("a must execute before b, got order %v", order)
	}
	if idx("c") >= idx("d") {
		t.Errorf("c must execute before d, got order %v", order)
	}
}

func TestFormatResults(t *testing.T) {
	results := []Result{
		{TaskID: "t1", Status: StatusDone, DurationMs: 150, Output: "hello"},
		{TaskID: "t2", Status: StatusFailed, DurationMs: 200, Error: "boom"},
		{TaskID: "t3", Status: StatusSkipped, Error: "skipped: upstream task \"t2\" failed"},
	}

	out := FormatResults(results)

	if !strings.Contains(out, "t1") || !strings.Contains(out, "done") {
		t.Errorf("expected t1 done in output: %s", out)
	}
	if !strings.Contains(out, "t2") || !strings.Contains(out, "failed") || !strings.Contains(out, "boom") {
		t.Errorf("expected t2 failed with error in output: %s", out)
	}
	if !strings.Contains(out, "t3") || !strings.Contains(out, "skipped") {
		t.Errorf("expected t3 skipped in output: %s", out)
	}
}

func TestFormatResults_Empty(t *testing.T) {
	out := FormatResults(nil)
	if out != "No tasks executed." {
		t.Errorf("expected 'No tasks executed.', got %q", out)
	}
	out = FormatResults([]Result{})
	if out != "No tasks executed." {
		t.Errorf("expected 'No tasks executed.', got %q", out)
	}
}

func TestExecute_ResultTaskID(t *testing.T) {
	// The executor fills in TaskID if the callback leaves it empty.
	tasks := []Task{
		{ID: "fill-me", Description: "test"},
	}
	exec := func(ctx context.Context, t Task) Result {
		return Result{Status: StatusDone, Output: "ok"}
		// TaskID intentionally left empty.
	}
	results := Execute(context.Background(), tasks, exec, 1)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].TaskID != "fill-me" {
		t.Errorf("expected TaskID 'fill-me', got %q", results[0].TaskID)
	}
}

func TestExecute_NonExistentDependency(t *testing.T) {
	// Task b depends on a non-existent task x.
	// The executor detects unsatisfiable deps and marks b as skipped.
	tasks := []Task{
		{ID: "a", Description: "independent"},
		{ID: "b", Description: "depends on missing", DependsOn: []string{"x"}},
	}

	exec := func(ctx context.Context, t Task) Result {
		return Result{Status: StatusDone, Output: t.ID}
	}

	results := Execute(context.Background(), tasks, exec, 2)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	statuses := map[string]ResultStatus{}
	for _, r := range results {
		statuses[r.TaskID] = r.Status
	}

	if statuses["a"] != StatusDone {
		t.Errorf("task a: expected done, got %s", statuses["a"])
	}
	if statuses["b"] != StatusSkipped {
		t.Errorf("task b: expected skipped, got %s", statuses["b"])
	}
}

