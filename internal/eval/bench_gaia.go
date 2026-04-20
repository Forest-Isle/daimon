package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// GAIAAdapter loads GAIA benchmark tasks — real-world multi-step
// reasoning problems that require tool use and planning.
type GAIAAdapter struct{}

type gaiaTask struct {
	TaskID      string   `json:"task_id"`
	Question    string   `json:"Question"`
	Level       int      `json:"Level"`
	FinalAnswer string   `json:"Final answer"`
	Steps       int      `json:"Annotator Metadata.Steps,omitempty"`
	Tools       []string `json:"Annotator Metadata.Tools,omitempty"`
}

func (a *GAIAAdapter) Name() string { return "gaia" }

func (a *GAIAAdapter) LoadTasks(path string) ([]TaskCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read gaia file: %w", err)
	}

	var raw []gaiaTask
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse gaia JSON: %w", err)
	}

	tasks := make([]TaskCase, 0, len(raw))
	for _, t := range raw {
		complexity := "moderate"
		if t.Level >= 2 {
			complexity = "complex"
		}

		tc := TaskCase{
			ID:           fmt.Sprintf("gaia-%s", t.TaskID),
			Goal:         t.Question,
			Complexity:   complexity,
			Tags:         []string{"benchmark", "gaia", "reasoning"},
			ExpectTools:  []string{"bash", "http"},
			Dimension:    DimPlanning,
			VerifyMethod: VerifyHybrid,
		}

		if t.FinalAnswer != "" {
			tc.Reference = &Reference{
				Answer:      t.FinalAnswer,
				MustContain: []string{t.FinalAnswer},
			}
		}

		tc.Rubric = &Rubric{
			Criteria: []JudgeCriterion{
				{Name: "correctness", Description: "Is the final answer correct?", Weight: 0.6},
				{Name: "reasoning", Description: "Is the reasoning process logical and well-structured?", Weight: 0.2},
				{Name: "efficiency", Description: "Was the solution approach efficient?", Weight: 0.2},
			},
		}

		tasks = append(tasks, tc)
	}

	return tasks, nil
}

func (a *GAIAAdapter) FormatResult(results []EvalResult) ([]byte, error) {
	type gaiaResult struct {
		TaskID  string  `json:"task_id"`
		Correct bool    `json:"correct"`
		Score   float64 `json:"score"`
	}

	out := make([]gaiaResult, 0, len(results))
	for _, r := range results {
		id := r.TaskID
		if len(id) > 5 && id[:5] == "gaia-" {
			id = id[5:]
		}
		out = append(out, gaiaResult{
			TaskID:  id,
			Correct: r.FinalScore >= 0.5,
			Score:   r.FinalScore,
		})
	}
	return json.MarshalIndent(out, "", "  ")
}

// GAIAReferences returns known agent scores on GAIA benchmark.
func GAIAReferences() []ReferenceScore {
	return []ReferenceScore{
		{AgentName: "GPT-4 + Plugins", Score: 0.154, Source: "GAIA paper 2023"},
		{AgentName: "AutoGPT-4", Score: 0.053, Source: "GAIA paper 2023"},
		{AgentName: "Human Average", Score: 0.920, Source: "GAIA paper 2023"},
	}
}
