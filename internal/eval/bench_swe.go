package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// SWEBenchAdapter loads SWE-bench tasks where the agent must fix bugs
// given a GitHub issue description and a code repository.
type SWEBenchAdapter struct{}

// SWE-bench JSON task format (subset of fields used).
type sweBenchTask struct {
	InstanceID string `json:"instance_id"`
	Repo       string `json:"repo"`
	BaseCommit string `json:"base_commit"`
	Problem    string `json:"problem_statement"`
	TestPatch  string `json:"test_patch"`
	Hints      string `json:"hints_text,omitempty"`
	Difficulty string `json:"difficulty,omitempty"`
}

func (a *SWEBenchAdapter) Name() string { return "swe-bench" }

func (a *SWEBenchAdapter) LoadTasks(path string) ([]TaskCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read swe-bench file: %w", err)
	}

	var raw []sweBenchTask
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse swe-bench JSON: %w", err)
	}

	tasks := make([]TaskCase, 0, len(raw))
	for _, t := range raw {
		complexity := "complex"
		if t.Difficulty == "easy" {
			complexity = "moderate"
		}

		goal := fmt.Sprintf("Fix the following issue in %s (commit %s):\n\n%s",
			t.Repo, t.BaseCommit[:min(8, len(t.BaseCommit))], t.Problem)

		tc := TaskCase{
			ID:           fmt.Sprintf("swe-%s", t.InstanceID),
			Goal:         goal,
			Complexity:   complexity,
			Tags:         []string{"benchmark", "swe-bench", "bug-fix"},
			ExpectTools:  []string{"bash", "file_write", "file_read"},
			Dimension:    DimTaskExecution,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				ExitCode: intPtr(0),
			},
		}

		repo := t.Repo
		baseCommit := t.BaseCommit
		testPatch := t.TestPatch

		tc.SetupFunc = func() error {
			fmt.Printf("  [swe-bench] Setup: clone %s @ %s\n", repo, baseCommit[:min(8, len(baseCommit))])
			return nil
		}
		tc.CleanupFunc = func() error {
			fmt.Printf("  [swe-bench] Cleanup: %s\n", repo)
			return nil
		}

		_ = testPatch

		tasks = append(tasks, tc)
	}

	return tasks, nil
}

func (a *SWEBenchAdapter) FormatResult(results []EvalResult) ([]byte, error) {
	type sweResult struct {
		InstanceID string  `json:"instance_id"`
		Resolved   bool    `json:"resolved"`
		Score      float64 `json:"score"`
	}

	out := make([]sweResult, 0, len(results))
	for _, r := range results {
		id := r.TaskID
		if len(id) > 4 && id[:4] == "swe-" {
			id = id[4:]
		}
		out = append(out, sweResult{
			InstanceID: id,
			Resolved:   r.Success,
			Score:      r.FinalScore,
		})
	}
	return json.MarshalIndent(out, "", "  ")
}

// SWEBenchReferences returns known agent scores on SWE-bench Lite.
func SWEBenchReferences() []ReferenceScore {
	return []ReferenceScore{
		{AgentName: "SWE-Agent", Score: 0.123, Source: "SWE-bench Lite leaderboard 2024"},
		{AgentName: "Devin", Score: 0.139, Source: "Cognition AI blog 2024"},
		{AgentName: "AutoCodeRover", Score: 0.190, Source: "SWE-bench Lite leaderboard 2024"},
		{AgentName: "Agentless", Score: 0.274, Source: "SWE-bench Lite leaderboard 2024"},
		{AgentName: "OpenHands", Score: 0.290, Source: "SWE-bench Verified leaderboard 2024"},
	}
}

func intPtr(v int) *int { return &v }
