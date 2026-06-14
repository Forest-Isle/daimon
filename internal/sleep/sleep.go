// Package sleep is the consolidation phase: offline maintenance jobs that run
// while no one is waiting (on a timer or on demand) to keep the world model
// coherent — regenerating digests, folding the journal, distilling skills,
// detecting value drift. This is the substrate; jobs are added incrementally.
//
// A sleep cycle runs its jobs sequentially with per-job error isolation: one
// failing job never aborts the cycle, so a bad job cannot starve the others.
package sleep

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Summarizer turns a system prompt + content into synthesized text (an LLM).
// It matches the shape used elsewhere (memory.Completer) so the gateway's
// existing provider adapter satisfies it directly.
type Summarizer interface {
	Complete(ctx context.Context, systemPrompt, userMessage string) (string, error)
}

// Job is one maintenance task in a sleep cycle. Run returns a short
// human-readable summary of what it did (for the cycle report); a returned error
// is isolated to this job and recorded, not propagated to siblings.
type Job interface {
	Name() string
	Run(ctx context.Context) (string, error)
}

// Runner executes registered jobs in order, isolating failures. Cycles are
// serialized: only one cycle runs at a time, so an on-demand /sleep and a
// future heart-triggered cycle cannot overlap and clobber each other's writes
// from a stale snapshot.
type Runner struct {
	mu   sync.Mutex // held for the duration of a cycle (TryLock-gated)
	jobs []Job
}

// NewRunner builds a sleep runner over the given jobs (order preserved).
func NewRunner(jobs ...Job) *Runner {
	return &Runner{jobs: append([]Job(nil), jobs...)}
}

// Register appends a job to the cycle.
func (r *Runner) Register(j Job) { r.jobs = append(r.jobs, j) }

// JobResult is the outcome of one job in a cycle.
type JobResult struct {
	Name    string
	Summary string
	Err     error
}

// Report is the outcome of a full sleep cycle.
type Report struct {
	Results []JobResult
}

// Failed reports whether any job in the cycle errored.
func (rep Report) Failed() bool {
	for _, r := range rep.Results {
		if r.Err != nil {
			return true
		}
	}
	return false
}

// Run executes every job in order, isolating per-job failures (and panics), and
// returns a report of each outcome. A nil/empty runner runs nothing and reports
// cleanly. Cycles are mutually exclusive: if one is already in flight, Run
// returns immediately with a single "skipped" result rather than overlapping.
func (r *Runner) Run(ctx context.Context) Report {
	var rep Report
	if r == nil {
		return rep
	}
	if !r.mu.TryLock() {
		rep.Results = append(rep.Results, JobResult{
			Name:    "cycle",
			Summary: "skipped — a sleep cycle is already in progress",
		})
		return rep
	}
	defer r.mu.Unlock()

	for _, j := range r.jobs {
		// Honor cancellation between jobs so a shutdown mid-cycle stops promptly.
		if err := ctx.Err(); err != nil {
			rep.Results = append(rep.Results, JobResult{Name: "cycle", Err: err})
			break
		}
		summary, err := runJob(ctx, j)
		if err != nil {
			slog.Warn("sleep: job failed", "job", j.Name(), "err", err)
		}
		rep.Results = append(rep.Results, JobResult{Name: j.Name(), Summary: summary, Err: err})
	}
	return rep
}

// runJob executes a single job, converting a panic into an error so one
// misbehaving job cannot abort the whole cycle.
func runJob(ctx context.Context, j Job) (summary string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("job panicked: %v", p)
		}
	}()
	return j.Run(ctx)
}
