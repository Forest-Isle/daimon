package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/agent"
)

type LLMJudge struct {
	provider agent.Provider
}

func NewLLMJudge(provider agent.Provider) *LLMJudge {
	return &LLMJudge{provider: provider}
}

func (j *LLMJudge) Judge(ctx context.Context, task TaskCase, agentOutput string) (*JudgeResult, error) {
	if task.Rubric == nil || len(task.Rubric.Criteria) == 0 {
		return &JudgeResult{
			Scores:    map[string]float64{},
			Overall:   0.5,
			Reasoning: "No rubric provided; default score assigned.",
		}, nil
	}

	if j.provider == nil {
		return &JudgeResult{
			Scores:    map[string]float64{},
			Overall:   0.5,
			Reasoning: "No LLM provider configured for judge.",
		}, nil
	}

	prompt := j.buildPrompt(task, agentOutput)

	resp, err := j.provider.Complete(ctx, agent.CompletionRequest{
		System:    "You are an evaluation judge. Score the agent output against the given criteria. Respond ONLY with a JSON object.",
		Messages:  []agent.CompletionMessage{{Role: "user", Content: prompt}},
		MaxTokens: 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("judge LLM call: %w", err)
	}

	result := j.parseResponse(resp.Text, task.Rubric)
	return result, nil
}

func (j *LLMJudge) buildPrompt(task TaskCase, agentOutput string) string {
	var b strings.Builder

	b.WriteString("## Task\n")
	b.WriteString(task.Goal)
	b.WriteString("\n\n")

	if task.Reference != nil && task.Reference.Answer != "" {
		b.WriteString("## Reference Answer\n")
		b.WriteString(task.Reference.Answer)
		b.WriteString("\n\n")
	}

	b.WriteString("## Agent Output\n")
	b.WriteString(agentOutput)
	b.WriteString("\n\n")

	b.WriteString("## Scoring Criteria\n")
	for _, c := range task.Rubric.Criteria {
		fmt.Fprintf(&b, "- **%s** (weight %.1f): %s\n", c.Name, c.Weight, c.Description)
	}

	b.WriteString("\n## Instructions\n")
	b.WriteString("Score each criterion from 0.0 to 1.0. Respond with a JSON object:\n")
	b.WriteString("```json\n")
	b.WriteString(`{"scores": {"criterion_name": 0.0-1.0, ...}, "overall": 0.0-1.0, "reasoning": "...", "weaknesses": ["..."]}`)
	b.WriteString("\n```\n")
	b.WriteString("The 'overall' should be the weighted average of all criterion scores.")

	return b.String()
}

func (j *LLMJudge) parseResponse(text string, rubric *Rubric) *JudgeResult {
	text = extractJSON(text)

	var result JudgeResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		slog.Warn("judge: failed to parse LLM response, using fallback", "err", err)
		return &JudgeResult{
			Scores:    map[string]float64{},
			Overall:   0.5,
			Reasoning: "Failed to parse judge response; fallback score assigned.",
		}
	}

	if result.Scores == nil {
		result.Scores = map[string]float64{}
	}

	if len(result.Scores) > 0 && rubric != nil {
		weighted := 0.0
		totalWeight := 0.0
		for _, c := range rubric.Criteria {
			if s, ok := result.Scores[c.Name]; ok {
				weighted += s * c.Weight
				totalWeight += c.Weight
			}
		}
		if totalWeight > 0 {
			result.Overall = weighted / totalWeight
		}
	}

	return &result
}

func extractJSON(text string) string {
	text = strings.TrimSpace(text)

	if idx := strings.Index(text, "```json"); idx >= 0 {
		text = text[idx+7:]
		if end := strings.Index(text, "```"); end >= 0 {
			text = text[:end]
		}
	} else if idx := strings.Index(text, "```"); idx >= 0 {
		text = text[idx+3:]
		if end := strings.Index(text, "```"); end >= 0 {
			text = text[:end]
		}
	}

	text = strings.TrimSpace(text)

	if start := strings.Index(text, "{"); start >= 0 {
		depth := 0
		for i := start; i < len(text); i++ {
			if text[i] == '{' {
				depth++
			} else if text[i] == '}' {
				depth--
				if depth == 0 {
					return text[start : i+1]
				}
			}
		}
	}

	return text
}
