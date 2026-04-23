package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

// TaskIDSkillEvolutionDraftQuality is the built-in eval task that runs an
// offline skill-synthesizer check (no live LLM). It gates the skill_evolution
// dimension: drafts must meet minimum structure and avoid repeated-bash spam.
const TaskIDSkillEvolutionDraftQuality = "ev-skill-evolution-draft-quality"

// RunSkillEvolutionDimensionCheck runs the evolution SkillSynthesizer in a temp dir with
// synthetic episodes, scores the resulting SKILL.md, and returns an EvalResult.
// Used by CognitiveAgentRunner, DryRunner, and tests.
func RunSkillEvolutionDimensionCheck(ctx context.Context, task TaskCase) (*EvalResult, error) {
	start := time.Now()
	dir, err := os.MkdirTemp("", "ironclaw-ev-skill-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	synth := evolution.NewSkillSynthesizer(evolution.SynthesizerConfig{
		Enabled:          true,
		PatternThreshold: 3,
		RewardThreshold:  0.5,
		MinUniqueTools:   2,
		DraftsDir:        dir,
		LLMEnabled:       false,
	})
	seq := []string{
		"file_read", "file_read", "bash", "bash", "file_write", "file_write",
	}
	goal := "Eval: verify skill draft quality pipeline"
	lessons := []string{"Run tests before merge."}
	for range 3 {
		synth.OnEpisodeComplete(ctx, evolution.EpisodeEvent{
			SessionID:      "eval-skill-evo",
			Goal:           goal,
			Complexity:     "complex",
			Succeeded:      true,
			TotalReward:    0.86,
			ToolSequence:   append([]string(nil), seq...),
			LessonsLearned: lessons,
			Timestamp:      time.Now(),
		})
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var samplePath, body string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "SKILL_") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			continue
		}
		body = string(b)
		samplePath = p
		break
	}
	if body == "" {
		return &EvalResult{
			TaskID:     task.ID,
			Goal:       task.Goal,
			Complexity: task.Complexity,
			Dimension:  task.Dimension,
			Success:    false,
			Error:      "no SKILL draft generated in temp dir",
			Duration:   time.Since(start),
			Timestamp:  time.Now(),
		}, nil
	}
	score, pass, fail := evolution.ScoreSkillDraftMarkdown(body)
	const minPass = 0.65
	ok := score >= minPass
	out := &EvalResult{
		TaskID:     task.ID,
		Goal:       task.Goal,
		Complexity: task.Complexity,
		Dimension:  task.Dimension,
		Success:    ok,
		Confidence: score,
		FinalScore: score,
		Duration:   time.Since(start),
		Timestamp:  time.Now(),
		SkillEvolution: &SkillEvolutionEval{
			Score:        score,
			ChecksPassed: pass,
			ChecksFailed: fail,
			SamplePath:   samplePath,
			MinPass:      minPass,
		},
		AgentOutput: fmt.Sprintf("skill evolution offline check: score=%.2f", score),
	}
	if !ok {
		out.FailureCategory = "skill_draft_quality"
	}
	return out, nil
}
