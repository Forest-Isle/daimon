package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// HumanEvalAdapter loads HumanEval tasks where the agent must implement
// a function given its signature and docstring.
type HumanEvalAdapter struct{}

type humanEvalTask struct {
	TaskID            string `json:"task_id"`
	Prompt            string `json:"prompt"`
	EntryPoint        string `json:"entry_point"`
	CanonicalSolution string `json:"canonical_solution,omitempty"`
	Test              string `json:"test"`
}

func (a *HumanEvalAdapter) Name() string { return "humaneval" }

func (a *HumanEvalAdapter) LoadTasks(path string) ([]TaskCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read humaneval file: %w", err)
	}

	var raw []humanEvalTask
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse humaneval JSON: %w", err)
	}

	tasks := make([]TaskCase, 0, len(raw))
	for _, t := range raw {
		goal := fmt.Sprintf("Implement the following Python function:\n\n%s\n\nWrite the implementation to /tmp/ironclaw_humaneval/%s.py and make sure the test passes.",
			t.Prompt, t.EntryPoint)

		tc := TaskCase{
			ID:           fmt.Sprintf("he-%s", t.TaskID),
			Goal:         goal,
			Complexity:   "moderate",
			Tags:         []string{"benchmark", "humaneval", "coding"},
			ExpectTools:  []string{"file_write", "bash"},
			Dimension:    DimTaskExecution,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				ExitCode: intPtr(0),
			},
		}

		entryPoint := t.EntryPoint
		testCode := t.Test

		tc.SetupFunc = func() error {
			if err := os.MkdirAll("/tmp/ironclaw_humaneval", 0o755); err != nil {
				return err
			}
			testPath := fmt.Sprintf("/tmp/ironclaw_humaneval/test_%s.py", entryPoint)
			return os.WriteFile(testPath, []byte(testCode), 0o644)
		}

		tasks = append(tasks, tc)
	}

	return tasks, nil
}

func (a *HumanEvalAdapter) FormatResult(results []EvalResult) ([]byte, error) {
	type heResult struct {
		TaskID string `json:"task_id"`
		Passed bool   `json:"passed"`
	}

	out := make([]heResult, 0, len(results))
	for _, r := range results {
		id := r.TaskID
		if len(id) > 3 && id[:3] == "he-" {
			id = id[3:]
		}
		out = append(out, heResult{TaskID: id, Passed: r.Success})
	}
	return json.MarshalIndent(out, "", "  ")
}

// HumanEvalReferences returns known agent/model scores on HumanEval.
func HumanEvalReferences() []ReferenceScore {
	return []ReferenceScore{
		{AgentName: "GPT-4", Score: 0.670, Source: "OpenAI technical report 2023"},
		{AgentName: "Claude 3.5 Sonnet", Score: 0.649, Source: "Anthropic benchmark 2024"},
		{AgentName: "GPT-4o", Score: 0.907, Source: "OpenAI benchmark 2024"},
		{AgentName: "DeepSeek-Coder-V2", Score: 0.901, Source: "DeepSeek paper 2024"},
	}
}
