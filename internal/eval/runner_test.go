package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// mockAgent implements RunnableAgent deterministically: it writes fixed content
// to a file in the workdir and returns a fixed output. This lets the runner be
// tested end-to-end with no LLM and no network.
type mockAgent struct {
	writeFile string
	writeBody string
	output    string
}

func (m mockAgent) Run(_ context.Context, workdir, _ string) (string, error) {
	if m.writeFile != "" {
		_ = os.WriteFile(filepath.Join(workdir, m.writeFile), []byte(m.writeBody), 0o644)
	}
	return m.output, nil
}

func TestRunTask_AllScorersPass(t *testing.T) {
	task := &Task{
		ID:     "make-greeting",
		Prompt: "create greeting.txt saying hello",
		Scorers: []ScorerSpec{
			{Type: "file_contains", Path: "greeting.txt", Substr: "hello"},
			{Type: "output_contains", Substr: "done"},
		},
	}
	agent := mockAgent{writeFile: "greeting.txt", writeBody: "hello there", output: "done"}

	res, err := RunTask(context.Background(), task, agent)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if !res.Passed {
		t.Errorf("expected pass, got %+v", res)
	}
	if res.Score != 1.0 {
		t.Errorf("score = %v, want 1.0", res.Score)
	}
	if len(res.Verdicts) != 2 {
		t.Errorf("verdicts = %d, want 2", len(res.Verdicts))
	}
}

func TestRunTask_PartialFailure(t *testing.T) {
	task := &Task{
		ID:     "partial",
		Prompt: "p",
		Scorers: []ScorerSpec{
			{Type: "file_contains", Path: "greeting.txt", Substr: "hello"},
			{Type: "output_contains", Substr: "NEVER"},
		},
	}
	agent := mockAgent{writeFile: "greeting.txt", writeBody: "hello", output: "done"}

	res, err := RunTask(context.Background(), task, agent)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if res.Passed {
		t.Error("expected overall fail when one scorer fails")
	}
	if res.Score != 0.5 {
		t.Errorf("score = %v, want 0.5 (1 of 2)", res.Score)
	}
}

func TestRunTask_SetupRuns(t *testing.T) {
	task := &Task{
		ID:      "with-setup",
		Prompt:  "p",
		Setup:   "echo seeded > seed.txt",
		Scorers: []ScorerSpec{{Type: "file_contains", Path: "seed.txt", Substr: "seeded"}},
	}
	// Agent does nothing; only setup creates the file.
	res, err := RunTask(context.Background(), task, mockAgent{output: "ok"})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if !res.Passed {
		t.Errorf("expected setup to seed file so scorer passes, got %+v", res)
	}
}

func TestRunTask_UnknownScorerErrors(t *testing.T) {
	task := &Task{ID: "bad", Prompt: "p", Scorers: []ScorerSpec{{Type: "bogus"}}}
	if _, err := RunTask(context.Background(), task, mockAgent{}); err == nil {
		t.Error("expected error for unknown scorer type")
	}
}
