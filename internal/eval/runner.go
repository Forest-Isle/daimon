package eval

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// RunnableAgent is the minimal interface the eval runner needs. The real agent
// is adapted to this in the cmd entry point; tests supply a deterministic mock.
// Run executes the task prompt in workdir and returns the agent's final output.
type RunnableAgent interface {
	Run(ctx context.Context, workdir, prompt string) (string, error)
}

// Result is the outcome of running one Task.
type Result struct {
	TaskID   string
	Passed   bool      // true only if every scorer passed
	Score    float64   // fraction of scorers that passed, 0.0–1.0
	Verdicts []Verdict // per-scorer detail
	Output   string    // agent's final output, retained for inspection
}

// RunTask runs a single task end-to-end in an isolated temp workdir: optional
// setup, then the agent, then every scorer. It never mutates the caller's cwd.
func RunTask(ctx context.Context, task *Task, agent RunnableAgent) (Result, error) {
	scorers, err := task.BuildScorers()
	if err != nil {
		return Result{}, err
	}

	workdir, err := os.MkdirTemp("", "eval-"+task.ID+"-")
	if err != nil {
		return Result{}, fmt.Errorf("create workdir: %w", err)
	}
	defer os.RemoveAll(workdir)

	if task.Setup != "" {
		cmd := exec.CommandContext(ctx, "sh", "-c", task.Setup)
		cmd.Dir = workdir
		if out, setupErr := cmd.CombinedOutput(); setupErr != nil {
			return Result{}, fmt.Errorf("setup failed: %w: %s", setupErr, out)
		}
	}

	output, err := agent.Run(ctx, workdir, task.Prompt)
	if err != nil {
		return Result{}, fmt.Errorf("agent run: %w", err)
	}

	res := Result{TaskID: task.ID, Output: output}
	passed := 0
	for _, s := range scorers {
		v := s.Score(workdir, output)
		res.Verdicts = append(res.Verdicts, v)
		if v.Passed {
			passed++
		}
	}
	if len(scorers) > 0 {
		res.Score = float64(passed) / float64(len(scorers))
	}
	res.Passed = passed == len(scorers)
	return res, nil
}
