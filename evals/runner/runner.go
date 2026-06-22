// Package runner loads the recorded replay corpus and runs the deterministic
// evals over it, producing an aggregate result for the scorecard. It is the
// outer "observer" layer: it may import internal/* trace types but is logically
// independent of the agent it grades.
package runner

import (
	"encoding/json"

	"github.com/Forest-Isle/daimon/evals/checks"
	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/replay"
)

// CorpusResult is the aggregate of running the deterministic evals over a set of
// replay sessions.
type CorpusResult struct {
	Sessions  int
	ToolCalls int
	Salvaged  int
	Failures  checks.FailureSummary
}

// LoadCorpus reads the replay journals under dir into sessions. It returns the
// number of unparseable lines skipped alongside the sessions.
func LoadCorpus(dir string) (sessions []replay.Session, skipped int, err error) {
	return replay.LoadDir(dir)
}

// toolResult is the minimal shape needed to read a tool result's error string.
type toolResult struct {
	Error string
}

// ExtractFailures collects every failed tool call across the sessions as a
// checks.ToolFailure. A failed call with an empty error string is recorded with
// a placeholder so it still classifies and is never silently dropped.
func ExtractFailures(sessions []replay.Session) []checks.ToolFailure {
	var out []checks.ToolFailure
	for _, s := range sessions {
		for _, tr := range s.Tools {
			if tr.Succeeded {
				continue
			}
			out = append(out, checks.ToolFailure{
				Tool:  tr.ToolName,
				Error: toolError(tr),
			})
		}
	}
	return out
}

// toolError decodes the error string from a tool round trip's result payload,
// falling back to a placeholder when the payload is absent or undecodable so a
// failed call always classifies as a real failure.
func toolError(tr agent.ToolRoundTrip) string {
	if len(tr.ResultJSON) > 0 {
		var r toolResult
		if err := json.Unmarshal(tr.ResultJSON, &r); err == nil && r.Error != "" {
			return r.Error
		}
	}
	return "unknown failure"
}

// Run aggregates the deterministic evals over the sessions.
func Run(sessions []replay.Session) CorpusResult {
	res := CorpusResult{Sessions: len(sessions)}
	for _, s := range sessions {
		res.ToolCalls += len(s.Tools)
		if s.Salvaged {
			res.Salvaged++
		}
	}
	res.Failures = checks.SummarizeFailures(ExtractFailures(sessions))
	return res
}
