package sleep

import (
	"context"
	"errors"
	"testing"
)

// fakeJob is a programmable Job for runner tests.
type fakeJob struct {
	name    string
	summary string
	err     error
	ran     *bool
}

func (f fakeJob) Name() string { return f.name }
func (f fakeJob) Run(_ context.Context) (string, error) {
	if f.ran != nil {
		*f.ran = true
	}
	return f.summary, f.err
}

func TestRunnerIsolatesJobFailures(t *testing.T) {
	var thirdRan bool
	r := NewRunner(
		fakeJob{name: "a", summary: "did a"},
		fakeJob{name: "b", err: errors.New("boom")},
		fakeJob{name: "c", summary: "did c", ran: &thirdRan},
	)
	rep := r.Run(context.Background())

	if len(rep.Results) != 3 {
		t.Fatalf("want 3 results, got %d", len(rep.Results))
	}
	if !thirdRan {
		t.Fatal("a failing job must not abort the cycle; job c should still run")
	}
	if !rep.Failed() {
		t.Fatal("report should mark the cycle as having a failure")
	}
	if rep.Results[1].Err == nil {
		t.Fatal("job b error not recorded")
	}
	if rep.Results[0].Err != nil || rep.Results[2].Err != nil {
		t.Fatal("successful jobs should have nil error")
	}
}

func TestRunnerStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ran bool
	r := NewRunner(fakeJob{name: "x", ran: &ran})
	rep := r.Run(ctx)
	if ran {
		t.Fatal("job should not run under a cancelled context")
	}
	if !rep.Failed() {
		t.Fatal("cancellation should surface as a failed result")
	}
}

func TestNilRunnerIsSafe(t *testing.T) {
	var r *Runner
	if rep := r.Run(context.Background()); rep.Failed() || len(rep.Results) != 0 {
		t.Fatalf("nil runner should report cleanly, got %+v", rep)
	}
}
