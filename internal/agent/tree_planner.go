package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
)

// PlanCandidate is one plan alternative with evaluation metadata.
type PlanCandidate struct {
	Plan          *TaskPlan
	Strategy      string
	LLMScore      float64
	Visits        int
	TotalReward   float64
	Failed        bool
	FailureReason string
}

// StrategicTreeNode holds candidates at one depth level.
type StrategicTreeNode struct {
	Candidates  []*PlanCandidate
	Depth       int
	TotalVisits int
	Parent      *StrategicTreeNode
}

// StrategicTreePlanner wraps Planner with tree-search logic.
type StrategicTreePlanner struct {
	planner       *Planner
	provider      Provider
	baseState     *CognitiveState
	llmModel      string
	maxCandidates int
	maxDepth      int
	explorationC  float64
	nodes         []*StrategicTreeNode
	currentNode   *StrategicTreeNode
	currentIdx    int
}

func NewStrategicTreePlanner(planner *Planner, provider Provider, llmModel string) *StrategicTreePlanner {
	return &StrategicTreePlanner{
		planner:       planner,
		provider:      provider,
		llmModel:      llmModel,
		maxCandidates: 3,
		maxDepth:      2,
		explorationC:  1.4,
		currentIdx:    -1,
	}
}

func (p *StrategicTreePlanner) GenerateCandidates(ctx context.Context, state *CognitiveState) error {
	if p == nil || p.planner == nil || state == nil {
		return fmt.Errorf("tree-planner unavailable")
	}

	p.baseState = state
	p.nodes = nil
	p.currentNode = nil
	p.currentIdx = -1

	candidates, err := p.generateCandidatesForHints(ctx, state, []strategyHint{
		{
			Name: "direct",
			Hint: "Prefer the simplest, most direct approach. Minimize tool calls. Use the most obvious solution.",
		},
		{
			Name: "thorough",
			Hint: "Verify each step before proceeding. Include diagnostic and validation checks. Prefer reliability over speed.",
		},
		{
			Name: "incremental",
			Hint: "Break the task into the smallest possible sub-steps. Solve one small piece at a time.",
		},
	})
	if err != nil {
		return err
	}

	if evalErr := p.evaluateCandidates(ctx, state, candidates); evalErr != nil {
		slog.Warn("tree-planner: candidate evaluation failed, using planner confidence", "err", evalErr)
	}

	node := &StrategicTreeNode{
		Candidates: candidates,
		Depth:      0,
	}
	p.nodes = append(p.nodes, node)
	p.currentNode = node

	slog.Info("tree-planner: generated candidates", "count", len(candidates), "depth", node.Depth)
	return nil
}

func (p *StrategicTreePlanner) Select() *TaskPlan {
	if p == nil || p.planner == nil || p.currentNode == nil {
		return nil
	}

	bestIdx := -1
	bestScore := math.Inf(-1)
	totalVisits := float64(p.currentNode.TotalVisits + 1)

	for idx, candidate := range p.currentNode.Candidates {
		if candidate == nil || candidate.Plan == nil || candidate.Failed {
			continue
		}

		var ucb float64
		if candidate.Visits == 0 {
			ucb = candidate.LLMScore + p.explorationC*math.Sqrt(math.Log(totalVisits))
		} else {
			avgReward := candidate.TotalReward / float64(candidate.Visits)
			ucb = avgReward + p.explorationC*math.Sqrt(math.Log(totalVisits)/float64(candidate.Visits))
		}

		if ucb > bestScore {
			bestScore = ucb
			bestIdx = idx
		}
	}

	if bestIdx < 0 {
		return nil
	}

	p.currentIdx = bestIdx
	selected := p.currentNode.Candidates[bestIdx]
	selected.Visits++
	p.currentNode.TotalVisits++

	slog.Info("tree-planner: selected candidate",
		"strategy", selected.Strategy,
		"score", selected.LLMScore,
		"visits", selected.Visits,
		"depth", p.currentNode.Depth,
	)

	return selected.Plan
}

func (p *StrategicTreePlanner) Backprop(reward float64) {
	if p == nil || p.planner == nil || p.currentNode == nil || p.currentIdx < 0 || p.currentIdx >= len(p.currentNode.Candidates) {
		return
	}

	candidate := p.currentNode.Candidates[p.currentIdx]
	if candidate == nil {
		return
	}

	candidate.TotalReward += reward
	if reward < 0.5 {
		candidate.Failed = true
		candidate.FailureReason = fmt.Sprintf("reward %.2f below threshold", reward)
	}

	slog.Info("tree-planner: backprop complete",
		"strategy", candidate.Strategy,
		"reward", reward,
		"total_reward", candidate.TotalReward,
		"failed", candidate.Failed,
	)
}

func (p *StrategicTreePlanner) HasAlternatives() bool {
	if p == nil || p.planner == nil || p.currentNode == nil {
		return false
	}
	for _, candidate := range p.currentNode.Candidates {
		if candidate != nil && candidate.Plan != nil && !candidate.Failed {
			return true
		}
	}
	return false
}

func (p *StrategicTreePlanner) Expand(ctx context.Context, failureSummary string) error {
	if p == nil || p.planner == nil || p.baseState == nil {
		return fmt.Errorf("tree-planner unavailable")
	}
	if p.currentNode == nil {
		return fmt.Errorf("no current node to expand")
	}
	if len(p.nodes) >= p.maxDepth+1 {
		return fmt.Errorf("max depth exceeded")
	}

	baseHint := strings.TrimSpace(failureSummary)
	if baseHint == "" {
		baseHint = "Previous approaches failed. Try a fundamentally different strategy. Consider alternative tools."
	} else {
		baseHint = "Previous approaches failed: " + baseHint + ". Try a fundamentally different strategy. Consider alternative tools."
	}

	nextDepth := p.currentNode.Depth + 1
	candidates, err := p.generateCandidatesForHints(ctx, p.baseState, []strategyHint{
		{
			Name: "direct",
			Hint: baseHint + " Prefer the simplest, most direct approach. Minimize tool calls. Use the most obvious solution.",
		},
		{
			Name: "thorough",
			Hint: baseHint + " Verify each step before proceeding. Include diagnostic and validation checks. Prefer reliability over speed.",
		},
		{
			Name: "incremental",
			Hint: baseHint + " Break the task into the smallest possible sub-steps. Solve one small piece at a time.",
		},
	})
	if err != nil {
		return err
	}

	if evalErr := p.evaluateCandidates(ctx, p.baseState, candidates); evalErr != nil {
		slog.Warn("tree-planner: expanded candidate evaluation failed, using planner confidence", "err", evalErr)
	}

	node := &StrategicTreeNode{
		Candidates: candidates,
		Depth:      nextDepth,
		Parent:     p.currentNode,
	}
	p.nodes = append(p.nodes, node)
	p.currentNode = node
	p.currentIdx = -1

	slog.Info("tree-planner: expanded node", "depth", node.Depth, "count", len(candidates))
	return nil
}

func (p *StrategicTreePlanner) evaluateCandidates(ctx context.Context, state *CognitiveState, candidates []*PlanCandidate) error {
	if p == nil || p.provider == nil || state == nil || len(candidates) == 0 {
		return fmt.Errorf("tree-planner evaluation unavailable")
	}

	var prompt strings.Builder
	prompt.WriteString("Task: ")
	prompt.WriteString(strings.TrimSpace(state.UserMessage))
	prompt.WriteString("\n\n")

	labels := []string{"A", "B", "C", "D", "E"}
	for i, candidate := range candidates {
		if candidate == nil || candidate.Plan == nil {
			continue
		}
		label := fmt.Sprintf("Plan %d", i+1)
		if i < len(labels) {
			label = "Plan " + labels[i]
		}
		_, _ = fmt.Fprintf(&prompt, "%s (%s): %s - %d subtasks\n", label, candidate.Strategy, candidate.Plan.Summary, len(candidate.Plan.SubTasks))
		for _, subTask := range candidate.Plan.SubTasks {
			if subTask == nil || strings.TrimSpace(subTask.Description) == "" {
				continue
			}
			_, _ = fmt.Fprintf(&prompt, "- %s\n", strings.TrimSpace(subTask.Description))
		}
		prompt.WriteString("\n")
	}
	prompt.WriteString(`Return: [{"plan_index": 0, "feasibility": 0.0, "efficiency": 0.0, "robustness": 0.0, "total": 0.0}]`)

	model := p.llmModel
	if model == "" && p.planner != nil {
		model = p.planner.llmModel
	}
	if state.ModelOverride != "" {
		model = state.ModelOverride
	}

	req := CompletionRequest{
		Model:  model,
		System: "You are a plan evaluator. Score each plan on feasibility, efficiency, and robustness. Return ONLY a JSON array.",
		Messages: []CompletionMessage{{
			Role:    "user",
			Content: prompt.String(),
		}},
		MaxTokens: 1024,
	}

	resp, err := p.provider.Complete(ctx, req)
	if err != nil {
		return fmt.Errorf("evaluate candidates: %w", err)
	}

	evals, err := parseCandidateEvaluations(resp.Text)
	if err != nil {
		return err
	}

	updated := 0
	for _, eval := range evals {
		if eval.PlanIndex < 0 || eval.PlanIndex >= len(candidates) || candidates[eval.PlanIndex] == nil {
			continue
		}
		score := eval.Total
		if score <= 0 {
			score = (eval.Feasibility + eval.Efficiency + eval.Robustness) / 3
		}
		if score < 0 {
			score = 0
		}
		if score > 1 {
			score = 1
		}
		candidates[eval.PlanIndex].LLMScore = score
		updated++
	}
	if updated == 0 {
		return fmt.Errorf("no candidate scores parsed")
	}

	slog.Info("tree-planner: evaluated candidates", "updated", updated)
	return nil
}

func (p *StrategicTreePlanner) generateCandidatesForHints(ctx context.Context, state *CognitiveState, hints []strategyHint) ([]*PlanCandidate, error) {
	candidates := make([]*PlanCandidate, 0, len(hints))
	for _, strategy := range hints {
		strategyState := *state
		strategyState.StrategyHints = strings.TrimSpace(strategy.Hint)

		plan, err := p.planner.Run(ctx, &strategyState)
		if err != nil {
			slog.Warn("tree-planner: candidate generation failed", "strategy", strategy.Name, "err", err)
			continue
		}
		if plan == nil {
			continue
		}

		candidates = append(candidates, &PlanCandidate{
			Plan:     plan,
			Strategy: strategy.Name,
			LLMScore: clampTreeScore(plan.OverallConfidence),
		})
		if len(candidates) >= p.maxCandidates {
			break
		}
	}

	if len(candidates) < 2 {
		return nil, fmt.Errorf("insufficient candidates")
	}
	return candidates, nil
}

type candidateEvaluation struct {
	PlanIndex   int     `json:"plan_index"`
	Feasibility float64 `json:"feasibility"`
	Efficiency  float64 `json:"efficiency"`
	Robustness  float64 `json:"robustness"`
	Total       float64 `json:"total"`
}

func parseCandidateEvaluations(text string) ([]candidateEvaluation, error) {
	raw := strings.TrimSpace(text)
	var evals []candidateEvaluation
	if err := json.Unmarshal([]byte(raw), &evals); err == nil && len(evals) > 0 {
		return evals, nil
	}

	if start := strings.Index(raw, "```json"); start >= 0 {
		block := raw[start+7:]
		if end := strings.Index(block, "```"); end >= 0 {
			block = strings.TrimSpace(block[:end])
			if err := json.Unmarshal([]byte(block), &evals); err == nil && len(evals) > 0 {
				return evals, nil
			}
		}
	}

	if start := strings.Index(raw, "```"); start >= 0 {
		block := raw[start+3:]
		if end := strings.Index(block, "```"); end >= 0 {
			block = strings.TrimSpace(block[:end])
			if err := json.Unmarshal([]byte(block), &evals); err == nil && len(evals) > 0 {
				return evals, nil
			}
		}
	}

	start := strings.Index(raw, "[")
	end := strings.LastIndex(raw, "]")
	if start >= 0 && end > start {
		block := strings.TrimSpace(raw[start : end+1])
		if err := json.Unmarshal([]byte(block), &evals); err == nil && len(evals) > 0 {
			return evals, nil
		}
	}

	return nil, fmt.Errorf("unable to parse candidate evaluation response")
}

func clampTreeScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func computeTreeReward(reflection *Reflection, obsResult *ObservationResult) float64 {
	if reflection == nil {
		return 0
	}
	if reflection.Succeeded {
		return clampTreeScore(reflection.OverallConfidence)
	}
	if obsResult != nil && len(obsResult.Assertions) > 0 {
		passed := 0
		for _, assertion := range obsResult.Assertions {
			if assertion.Passed {
				passed++
			}
		}
		assertionRate := float64(passed) / float64(len(obsResult.Assertions))
		return clampTreeScore(reflection.OverallConfidence*0.6 + assertionRate*0.4)
	}
	return clampTreeScore(reflection.OverallConfidence * 0.5)
}
