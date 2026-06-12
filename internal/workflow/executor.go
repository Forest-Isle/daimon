package workflow

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type StepRunner interface {
	RunStep(ctx context.Context, step Step, input StepInput) (StepOutput, error)
}

type Executor struct {
	Runner      StepRunner
	Cache       ReplayCache
	Observer    Observer
	MaxParallel int
}

type Observer interface {
	ObserveWorkflowStep(ctx context.Context, event StepEvent)
}

type StepEvent struct {
	WorkflowName   string
	WorkflowHash   string
	StageID        string
	StepID         string
	StepType       StepType
	Phase          string
	Status         Status
	Cached         bool
	DurationMillis int64
	Error          string
}

type RunStatus string

const (
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
)

type BudgetUsage struct {
	MaxSteps      int `json:"max_steps,omitempty"`
	MaxTokens     int `json:"max_tokens,omitempty"`
	StepsPlanned  int `json:"steps_planned"`
	StepsExecuted int `json:"steps_executed"`
	CacheHits     int `json:"cache_hits"`
	TokensUsed    int `json:"tokens_used"`
}

type Run struct {
	WorkflowName string       `json:"workflow_name"`
	WorkflowHash string       `json:"workflow_hash"`
	Status       RunStatus    `json:"status"`
	Results      []StepResult `json:"results"`
	Budget       BudgetUsage  `json:"budget"`
	StartedAt    time.Time    `json:"started_at"`
	CompletedAt  time.Time    `json:"completed_at"`
}

type budgetTracker struct {
	mu    sync.Mutex
	usage *BudgetUsage
}

func (b *budgetTracker) reserveStep() error {
	if b == nil || b.usage == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.usage.MaxSteps > 0 && b.usage.StepsExecuted+1 > b.usage.MaxSteps {
		return fmt.Errorf("workflow step budget exceeded: %d/%d", b.usage.StepsExecuted+1, b.usage.MaxSteps)
	}
	b.usage.StepsExecuted++
	return nil
}

func (b *budgetTracker) addCacheHit() {
	if b == nil || b.usage == nil {
		return
	}
	b.mu.Lock()
	b.usage.CacheHits++
	b.mu.Unlock()
}

func (b *budgetTracker) addTokens(step Step, tokens int) error {
	if b == nil || b.usage == nil || tokens <= 0 {
		if step.Budget.MaxTokens > 0 && tokens > step.Budget.MaxTokens {
			return fmt.Errorf("workflow step token budget exceeded: %d/%d", tokens, step.Budget.MaxTokens)
		}
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.usage.TokensUsed += tokens
	if step.Budget.MaxTokens > 0 && tokens > step.Budget.MaxTokens {
		return fmt.Errorf("workflow step token budget exceeded: %d/%d", tokens, step.Budget.MaxTokens)
	}
	if b.usage.MaxTokens > 0 && b.usage.TokensUsed > b.usage.MaxTokens {
		return fmt.Errorf("workflow token budget exceeded: %d/%d", b.usage.TokensUsed, b.usage.MaxTokens)
	}
	return nil
}

func (e *Executor) Execute(ctx context.Context, spec *Spec) (*Run, error) {
	if spec == nil {
		return nil, fmt.Errorf("workflow spec is nil")
	}
	if err := spec.NormalizeAndValidate(); err != nil {
		return nil, err
	}
	if e == nil || e.Runner == nil {
		return nil, fmt.Errorf("workflow runner is required")
	}

	run := &Run{
		WorkflowName: spec.Name,
		WorkflowHash: spec.Digest(),
		Status:       RunSucceeded,
		StartedAt:    time.Now().UTC(),
		Budget: BudgetUsage{
			MaxSteps:     spec.Budget.MaxSteps,
			MaxTokens:    spec.Budget.MaxTokens,
			StepsPlanned: countSteps(*spec),
		},
	}
	tracker := &budgetTracker{usage: &run.Budget}

	prior := make([]StepResult, 0, run.Budget.StepsPlanned)
	for _, stage := range spec.Stages {
		stagePrior := cloneResults(prior)
		var stageResults []StepResult
		var err error
		if stage.Parallel {
			stageResults, err = e.executeParallelStage(ctx, *spec, stage, stagePrior, tracker)
		} else {
			stageResults, err = e.executeSequentialStage(ctx, *spec, stage, prior, tracker)
		}
		if err != nil {
			run.Status = RunFailed
			run.CompletedAt = time.Now().UTC()
			run.Results = append(run.Results, stageResults...)
			return run, err
		}
		run.Results = append(run.Results, stageResults...)
		prior = append(prior, stageResults...)
		if containsFailure(stageResults) && spec.FailureStrategy == FailureStop {
			run.Status = RunFailed
			break
		}
	}
	if containsFailure(run.Results) {
		run.Status = RunFailed
	}
	run.CompletedAt = time.Now().UTC()
	return run, nil
}

func (e *Executor) executeSequentialStage(ctx context.Context, spec Spec, stage Stage, prior []StepResult, budget *budgetTracker) ([]StepResult, error) {
	results := make([]StepResult, 0, len(stage.Steps))
	currentPrior := cloneResults(prior)
	for _, step := range stage.Steps {
		result, err := e.executeStep(ctx, spec, stage, step, currentPrior, budget)
		results = append(results, result)
		currentPrior = append(currentPrior, result)
		if err != nil {
			return results, err
		}
		if isFailure(result) && spec.FailureStrategy == FailureStop {
			return results, nil
		}
	}
	return results, nil
}

func (e *Executor) executeParallelStage(ctx context.Context, spec Spec, stage Stage, prior []StepResult, budget *budgetTracker) ([]StepResult, error) {
	results := make([]StepResult, len(stage.Steps))
	maxParallel := e.MaxParallel
	if maxParallel <= 0 || maxParallel > len(stage.Steps) {
		maxParallel = len(stage.Steps)
	}
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i, step := range stage.Steps {
		wg.Add(1)
		go func(idx int, s Step) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				if firstErr == nil {
					firstErr = ctx.Err()
				}
				mu.Unlock()
				return
			}
			result, err := e.executeStep(ctx, spec, stage, s, prior, budget)
			mu.Lock()
			results[idx] = result
			if err != nil && firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		}(i, step)
	}
	wg.Wait()
	return results, firstErr
}

func (e *Executor) executeStep(ctx context.Context, spec Spec, stage Stage, step Step, prior []StepResult, budget *budgetTracker) (StepResult, error) {
	start := time.Now().UTC()
	key := cacheKey(spec, stage, step, prior)
	e.observe(ctx, spec, stage, step, StepResult{Status: StatusSuccess}, "started")
	if stepCacheEnabled(step) && e.Cache != nil {
		record, ok, err := e.Cache.Get(ctx, key)
		if err != nil {
			return StepResult{}, fmt.Errorf("workflow cache get %s: %w", step.ID, err)
		}
		if ok && record.Result.Status == StatusSuccess {
			result := record.Result
			result.Cached = true
			result.CacheKey = key
			budget.addCacheHit()
			e.observe(ctx, spec, stage, step, result, "completed")
			return result, nil
		}
	}

	if err := budget.reserveStep(); err != nil {
		result := failedStep(stage, step, key, start, err.Error())
		e.observe(ctx, spec, stage, step, result, "completed")
		return result, nil
	}

	output, err := e.Runner.RunStep(ctx, step, StepInput{
		WorkflowName: spec.Name,
		WorkflowHash: spec.Digest(),
		StageID:      stage.ID,
		PriorResults: cloneResults(prior),
	})
	completed := time.Now().UTC()
	result := StepResult{
		StepID:         step.ID,
		StageID:        stage.ID,
		Type:           step.Type,
		Status:         output.Status,
		CacheKey:       key,
		Summary:        output.Summary,
		Output:         output.Output,
		Artifacts:      output.Artifacts,
		TokensUsed:     output.TokensUsed,
		DurationMillis: completed.Sub(start).Milliseconds(),
		StartedAt:      start,
		CompletedAt:    completed,
		Metadata:       output.Metadata,
	}
	if result.Status == "" {
		result.Status = StatusSuccess
	}
	if err != nil {
		result.Status = StatusError
		result.Error = err.Error()
	}
	if output.Status == StatusError && result.Error == "" {
		result.Error = output.Output
	}
	if tokenErr := budget.addTokens(step, result.TokensUsed); tokenErr != nil && result.Error == "" {
		result.Status = StatusError
		result.Error = tokenErr.Error()
	}
	if result.Status == StatusSuccess && stepCacheEnabled(step) && e.Cache != nil {
		if err := e.Cache.Put(ctx, CacheRecord{
			WorkflowName: spec.Name,
			WorkflowHash: spec.Digest(),
			StageID:      stage.ID,
			StepID:       step.ID,
			CacheKey:     key,
			Result:       result,
			CreatedAt:    completed,
		}); err != nil {
			return result, fmt.Errorf("workflow cache put %s: %w", step.ID, err)
		}
	}
	e.observe(ctx, spec, stage, step, result, "completed")
	return result, nil
}

func (e *Executor) observe(ctx context.Context, spec Spec, stage Stage, step Step, result StepResult, phase string) {
	if e == nil || e.Observer == nil {
		return
	}
	e.Observer.ObserveWorkflowStep(ctx, StepEvent{
		WorkflowName:   spec.Name,
		WorkflowHash:   spec.Digest(),
		StageID:        stage.ID,
		StepID:         step.ID,
		StepType:       step.Type,
		Phase:          phase,
		Status:         result.Status,
		Cached:         result.Cached,
		DurationMillis: result.DurationMillis,
		Error:          result.Error,
	})
}

func failedStep(stage Stage, step Step, key string, start time.Time, msg string) StepResult {
	now := time.Now().UTC()
	return StepResult{
		StepID:         step.ID,
		StageID:        stage.ID,
		Type:           step.Type,
		Status:         StatusError,
		CacheKey:       key,
		Error:          msg,
		StartedAt:      start,
		CompletedAt:    now,
		DurationMillis: now.Sub(start).Milliseconds(),
	}
}

func containsFailure(results []StepResult) bool {
	for _, result := range results {
		if isFailure(result) {
			return true
		}
	}
	return false
}

func isFailure(result StepResult) bool {
	return result.Status == StatusError || result.Error != ""
}

func cloneResults(in []StepResult) []StepResult {
	out := make([]StepResult, len(in))
	copy(out, in)
	return out
}

func countSteps(spec Spec) int {
	total := 0
	for _, stage := range spec.Stages {
		total += len(stage.Steps)
	}
	return total
}
