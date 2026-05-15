package agent

import (
	"github.com/Forest-Isle/IronClaw/internal/util"
	"encoding/json"
	"fmt"
	"strings"
)

// DebateConfig configures the debate mode parameters.
type DebateConfig struct {
	MaxRounds int     `yaml:"max_rounds"` // max debate rounds, default 3
	Threshold float64 `yaml:"threshold"`  // consensus threshold, default 0.8
}

// DefaultDebateConfig returns sensible defaults for debate mode.
func DefaultDebateConfig() DebateConfig {
	return DebateConfig{
		MaxRounds: 3,
		Threshold: 0.8,
	}
}

// DebateResult holds the outcome of a debate between two agents.
type DebateResult struct {
	ProposalAgent string        // agent that proposed
	CritiqueAgent string        // agent that critiqued
	Rounds        []DebateRound // debate rounds
	Synthesis     string        // Reflector's final synthesis
	Consensus     float64       // consensus score (0.0–1.0)
}

// DebateRound captures one round of proposal → critique → rebuttal.
type DebateRound struct {
	Round    int
	Proposal string
	Critique string
	Rebuttal string
}

// BuildDebatePlan generates a TaskPlan that implements a structured debate
// between two agent_* tools. The Planner can use this when it detects a
// scenario requiring adversarial evaluation (high-risk decisions, multiple approaches).
//
// The plan alternates between proposer and critic:
//
//	t1: proposer proposes → t2: critic critiques (depends t1) → t3: proposer rebuts (depends t2) → ...
func BuildDebatePlan(topic string, proposerAgent, criticAgent string, cfg DebateConfig) *TaskPlan {
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = DefaultDebateConfig().MaxRounds
	}

	var subtasks []*SubTask
	taskID := 1
	var prevID string

	for round := 1; round <= cfg.MaxRounds; round++ {
		// Proposal (or rebuttal in subsequent rounds)
		proposalID := fmt.Sprintf("t%d", taskID)
		taskID++

		var proposalTask string
		if round == 1 {
			proposalTask = fmt.Sprintf("Propose a solution or approach for: %s", topic)
		} else {
			proposalTask = fmt.Sprintf("Round %d: Respond to the critique and refine your proposal for: %s", round, topic)
		}

		proposalInput, _ := json.Marshal(agentToolInput{
			Task:    proposalTask,
			Context: contextRef(prevID, round),
		})

		deps := []string{}
		if prevID != "" {
			deps = []string{prevID}
		}

		subtasks = append(subtasks, &SubTask{
			ID:          proposalID,
			Description: fmt.Sprintf("Round %d: %s proposes", round, proposerAgent),
			ToolName:    proposerAgent,
			ToolInput:   string(proposalInput),
			DependsOn:   deps,
			Confidence:  0.8,
			Status:      SubTaskPending,
		})

		// Critique
		critiqueID := fmt.Sprintf("t%d", taskID)
		taskID++

		critiqueTask := fmt.Sprintf("Round %d: Critically evaluate the proposal. Identify weaknesses, risks, and suggest improvements for: %s", round, topic)
		critiqueInput, _ := json.Marshal(agentToolInput{
			Task:    critiqueTask,
			Context: fmt.Sprintf("Previous proposal output will be provided from task %s", proposalID),
		})

		subtasks = append(subtasks, &SubTask{
			ID:          critiqueID,
			Description: fmt.Sprintf("Round %d: %s critiques", round, criticAgent),
			ToolName:    criticAgent,
			ToolInput:   string(critiqueInput),
			DependsOn:   []string{proposalID},
			Confidence:  0.8,
			Status:      SubTaskPending,
		})

		prevID = critiqueID
	}

	return &TaskPlan{
		Summary:           fmt.Sprintf("Debate: %s vs %s on %q (%d rounds)", proposerAgent, criticAgent, util.TruncateStr(topic, 50), cfg.MaxRounds),
		SubTasks:          subtasks,
		OverallConfidence: 0.7,
	}
}

// SynthesizeDebate takes a list of observations from a debate plan and produces
// a structured synthesis prompt that the Reflector can use for its final evaluation.
func SynthesizeDebate(observations []Observation, proposerAgent, criticAgent string) string {
	var sb strings.Builder
	sb.WriteString("## Debate Summary\n\n")

	round := 1
	for i := 0; i < len(observations); i += 2 {
		_, _ = fmt.Fprintf(&sb, "### Round %d\n\n", round)

		// Proposal
		if i < len(observations) {
			obs := observations[i]
			_, _ = fmt.Fprintf(&sb, "**%s (Proposal):**\n%s\n\n", proposerAgent, util.TruncateStr(obs.Output, 500))
		}

		// Critique
		if i+1 < len(observations) {
			obs := observations[i+1]
			_, _ = fmt.Fprintf(&sb, "**%s (Critique):**\n%s\n\n", criticAgent, util.TruncateStr(obs.Output, 500))
		}

		round++
	}

	sb.WriteString("---\n")
	sb.WriteString("Synthesize the above debate into a final recommendation. Consider the strongest arguments from both sides.\n")

	return sb.String()
}

func contextRef(prevID string, round int) string {
	if round == 1 || prevID == "" {
		return ""
	}
	return fmt.Sprintf("Previous critique output will be provided from task %s", prevID)
}

// SelectDebateAgents selects two agents for debate based on tags or defaults to first two.
// Returns (proposer, critic) agent names.
func SelectDebateAgents(specs []*AgentSpec, topic string) (string, string) {
	if len(specs) < 2 {
		return "", ""
	}

	// Try to find agents with "proposer" and "critic" tags
	var proposer, critic string
	for _, spec := range specs {
		for _, tag := range spec.Tags {
			if strings.Contains(strings.ToLower(tag), "propos") && proposer == "" {
				proposer = "agent_" + spec.Name
			}
			if strings.Contains(strings.ToLower(tag), "critic") && critic == "" {
				critic = "agent_" + spec.Name
			}
		}
	}

	// Fallback: use first two agents
	if proposer == "" {
		proposer = "agent_" + specs[0].Name
	}
	if critic == "" {
		if len(specs) > 1 {
			critic = "agent_" + specs[1].Name
		} else {
			critic = proposer // same agent debates itself if only one available
		}
	}

	return proposer, critic
}
