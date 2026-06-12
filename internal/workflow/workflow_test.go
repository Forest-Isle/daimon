package workflow

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Forest-Isle/daimon/internal/store"
)

type recordingRunner struct {
	mu       sync.Mutex
	calls    map[string]int
	contexts map[string]int
	tokens   int
}

func newRecordingRunner() *recordingRunner {
	return &recordingRunner{
		calls:    make(map[string]int),
		contexts: make(map[string]int),
		tokens:   10,
	}
}

func (r *recordingRunner) RunStep(_ context.Context, step Step, input StepInput) (StepOutput, error) {
	r.mu.Lock()
	r.calls[step.ID]++
	r.contexts[step.ID] = len(input.PriorResults)
	r.mu.Unlock()
	return StepOutput{
		Status:     StatusSuccess,
		Summary:    fmt.Sprintf("%s saw %d prior", step.ID, len(input.PriorResults)),
		Output:     "output:" + step.ID,
		Artifacts:  []string{"artifact:" + step.ID},
		TokensUsed: r.tokens,
	}, nil
}

func (r *recordingRunner) totalCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := 0
	for _, count := range r.calls {
		total += count
	}
	return total
}

func TestParseSpecYAMLDefaultsAndValidation(t *testing.T) {
	spec, err := ParseSpec([]byte(`
version: v1
name: research-pipeline
stages:
  - steps:
      - type: agent
        agent: researcher
        task: gather facts
`))
	if err != nil {
		t.Fatalf("ParseSpec() error = %v", err)
	}
	if spec.Stages[0].ID != "stage_1" {
		t.Fatalf("stage id = %q", spec.Stages[0].ID)
	}
	if spec.Stages[0].Steps[0].ID != "stage_1_step_1" {
		t.Fatalf("step id = %q", spec.Stages[0].Steps[0].ID)
	}

	_, err = ParseSpec([]byte(`
name: duplicate
stages:
  - id: s
    steps:
      - id: a
        type: agent
        agent: one
        task: first
      - id: a
        type: agent
        agent: two
        task: second
`))
	if err == nil {
		t.Fatal("expected duplicate step id error")
	}
}

func TestExecutorPipelineParallelBarrierAndReplay(t *testing.T) {
	spec, err := ParseSpec([]byte(`
version: v1
name: ship-feature
stages:
  - id: plan
    steps:
      - id: design
        type: agent
        agent: architect
        task: design the change
  - id: build
    parallel: true
    steps:
      - id: backend
        type: agent
        agent: engineer
        task: implement backend
      - id: tests
        type: agent
        agent: tester
        task: write tests
  - id: verify
    steps:
      - id: review
        type: agent
        agent: reviewer
        task: review outputs
`))
	if err != nil {
		t.Fatalf("ParseSpec() error = %v", err)
	}

	cache := NewMemoryCache()
	runner := newRecordingRunner()
	executor := Executor{Runner: runner, Cache: cache, MaxParallel: 2}
	run, err := executor.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if run.Status != RunSucceeded {
		t.Fatalf("run status = %s", run.Status)
	}
	if runner.totalCalls() != 4 {
		t.Fatalf("runner calls = %d, want 4", runner.totalCalls())
	}
	if runner.contexts["design"] != 0 || runner.contexts["backend"] != 1 || runner.contexts["tests"] != 1 || runner.contexts["review"] != 3 {
		t.Fatalf("contexts = %#v", runner.contexts)
	}

	replayRunner := newRecordingRunner()
	replayExecutor := Executor{Runner: replayRunner, Cache: cache, MaxParallel: 2}
	replayRun, err := replayExecutor.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("replay Execute() error = %v", err)
	}
	if replayRunner.totalCalls() != 0 {
		t.Fatalf("replay runner calls = %d, want 0", replayRunner.totalCalls())
	}
	if replayRun.Budget.CacheHits != 4 || replayRun.Budget.StepsExecuted != 0 {
		t.Fatalf("replay budget = %#v", replayRun.Budget)
	}
	for _, result := range replayRun.Results {
		if !result.Cached {
			t.Fatalf("result %s was not marked cached", result.StepID)
		}
	}
}

func TestExecutorBudgetStopsUncachedExecution(t *testing.T) {
	spec, err := ParseSpec([]byte(`
name: budgeted
budget:
  max_steps: 1
stages:
  - id: run
    steps:
      - id: first
        type: agent
        agent: worker
        task: first
      - id: second
        type: agent
        agent: worker
        task: second
`))
	if err != nil {
		t.Fatalf("ParseSpec() error = %v", err)
	}
	runner := newRecordingRunner()
	executor := Executor{Runner: runner, Cache: NewMemoryCache()}
	run, err := executor.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if run.Status != RunFailed {
		t.Fatalf("run status = %s, want failed", run.Status)
	}
	if runner.totalCalls() != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.totalCalls())
	}
	if len(run.Results) != 2 || run.Results[1].Status != StatusError {
		t.Fatalf("results = %#v", run.Results)
	}
}

func TestSQLiteCacheReplaysAcrossExecutors(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "workflow.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	spec, err := ParseSpec([]byte(`
name: persistent-replay
stages:
  - id: one
    steps:
      - id: research
        type: agent
        agent: researcher
        task: produce output
`))
	if err != nil {
		t.Fatalf("ParseSpec() error = %v", err)
	}

	firstRunner := newRecordingRunner()
	cache := NewSQLiteCache(db.DB)
	firstExecutor := Executor{Runner: firstRunner, Cache: cache}
	if _, err := firstExecutor.Execute(context.Background(), spec); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	if firstRunner.totalCalls() != 1 {
		t.Fatalf("first calls = %d, want 1", firstRunner.totalCalls())
	}

	secondRunner := newRecordingRunner()
	secondExecutor := Executor{Runner: secondRunner, Cache: NewSQLiteCache(db.DB)}
	run, err := secondExecutor.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	if secondRunner.totalCalls() != 0 {
		t.Fatalf("second calls = %d, want replay", secondRunner.totalCalls())
	}
	if run.Budget.CacheHits != 1 || !run.Results[0].Cached {
		t.Fatalf("replay run = %#v", run)
	}
}
