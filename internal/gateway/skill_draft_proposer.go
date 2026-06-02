package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

// skillDraftProposer uses the main LLM to turn episode context into a procedure-style
// SKILL.md (pattern inspired by Hermes agent/skill_learner.py).
type skillDraftProposer struct {
	provider  agent.Provider
	model     string
	maxTokens int
}

func newSkillDraftProposer(p agent.Provider, model string) *skillDraftProposer {
	if p == nil || model == "" {
		return nil
	}
	return &skillDraftProposer{provider: p, model: model, maxTokens: 2048}
}

func (p *skillDraftProposer) Propose(ctx context.Context, in evolution.SkillProposeInput) (markdown, fileStem string, err error) {
	if p == nil || p.provider == nil {
		return "", "", fmt.Errorf("skill proposer: no provider")
	}

	prompt := buildSkillExtractPrompt(in)
	req := agent.CompletionRequest{
		Model:     p.model,
		System:    "You are a skill extraction expert. You identify reusable task procedures from tool-usage context. Output only the JSON object requested, no surrounding prose.",
		Messages:  []agent.CompletionMessage{{Role: "user", Content: prompt}},
		Tools:     nil,
		MaxTokens: p.maxTokens,
	}

	resp, err := p.provider.Complete(ctx, req)
	if err != nil {
		return "", "", err
	}
	if resp == nil || strings.TrimSpace(resp.Text) == "" {
		return "", "", fmt.Errorf("empty LLM response")
	}

	raw, jerr := extractJSON(resp.Text)
	if jerr != nil {
		return "", "", jerr
	}

	var proposal skillProposal
	if err := json.Unmarshal([]byte(raw), &proposal); err != nil {
		return "", "", fmt.Errorf("parse skill JSON: %w", err)
	}
	if proposal.NoSkill {
		return "", "", nil
	}
	if strings.TrimSpace(proposal.SkillName) == "" || len(proposal.Steps) == 0 {
		return "", "", nil
	}

	stem := evolution.SanitizeDraftFileStem(proposal.SkillName)
	md := buildSkillMDFromProposal(proposal, in, stem)
	return md, stem, nil
}

// Ensure skillDraftProposer implements evolution.SkillProposer
var _ evolution.SkillProposer = (*skillDraftProposer)(nil)

type skillProposal struct {
	NoSkill     bool     `json:"no_skill"`
	Reasoning   string   `json:"reasoning"`
	SkillName   string   `json:"skill_name"`
	Description string   `json:"description"`
	Steps       []string `json:"procedure_steps"`
	PreReq      []string `json:"prerequisites"`
	Success     []string `json:"success_criteria"`
	Confidence  float64  `json:"confidence"`
	General     string   `json:"generalizability"`
}

func buildSkillExtractPrompt(in evolution.SkillProposeInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Analyze the execution context below. Propose a reusable *task skill* (procedure) if it would help a coding agent on similar work later.\n\n")
	fmt.Fprintf(&b, "A good skill: solves a class of user goals, lists concrete steps (not a raw list of tool names), has prerequisites and success checks.\n\n")
	fmt.Fprintf(&b, "Aggregate statistics for the recurring tool pattern: pattern_id=%q, tool multiset=%v, seen_in_episodes=%d, avg_r=%.3f, first=%s, last=%s\n\n",
		in.PatternID, in.Tools, in.OccurrenceCount, in.AvgReward, in.FirstSeen.Format(time.RFC3339), in.LastSeen.Format(time.RFC3339))
	fmt.Fprintf(&b, "Last matching episode:\n- goal: %q\n- complexity: %q\n- succeeded: %v\n- episode_reward: %.3f\n",
		in.Goal, in.Complexity, in.Succeeded, in.LastTotalReward)
	if len(in.LessonsLearned) > 0 {
		fmt.Fprintf(&b, "\nReflection lessons from that episode:\n")
		for _, l := range in.LessonsLearned {
			fmt.Fprintf(&b, "  * %s\n", l)
		}
	}
	if len(in.LastToolSequence) > 0 {
		fmt.Fprintf(&b, "\nCollapsed tool flow (consecutive duplicate tools removed): %s\n", strings.Join(in.LastToolSequence, " → "))
	} else {
		b.WriteString("\n(No collapsed tool list — infer from tool multiset.)\n")
	}
	b.WriteString(`
Respond with JSON only:
{
  "no_skill": false,
  "reasoning": "one sentence",
  "skill_name": "snake_case_lowercase",
  "description": "one paragraph",
  "procedure_steps": ["imperative step 1", "..."],
  "prerequisites": ["env or access needed, if any"],
  "success_criteria": ["checkable outcome 1", "..."],
  "confidence": 0.0,
  "generalizability": "high|medium|low"
}
If the pattern is too generic (e.g. only "run bash many times") or not reusable, use {"no_skill": true, "reasoning": "..."} instead.
`)
	return b.String()
}

func extractJSON(s string) (string, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		rest = strings.TrimPrefix(rest, "json")
		rest = strings.TrimSpace(rest)
		if j := strings.Index(rest, "```"); j >= 0 {
			rest = rest[:j]
		}
		s = strings.TrimSpace(rest)
	}
	l := strings.Index(s, "{")
	r := strings.LastIndex(s, "}")
	if l < 0 || r <= l {
		return "", fmt.Errorf("no JSON object in model output")
	}
	return s[l : r+1], nil
}

func buildSkillMDFromProposal(p skillProposal, in evolution.SkillProposeInput, nameStem string) string {
	title := p.SkillName
	if nameStem != "" {
		title = nameStem
	}
	fullName := title
	if !strings.HasPrefix(fullName, "auto_") {
		fullName = "auto_" + fullName
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "name: %s\n", fullName)
	fmt.Fprintf(&b, "description: %s\n", strings.TrimSpace(p.Description))
	fmt.Fprintf(&b, "status: draft\n")
	fmt.Fprintf(&b, "auto_generated: true\n")
	fmt.Fprintf(&b, "source: evolution\n")
	if p.Confidence > 0 {
		fmt.Fprintf(&b, "evolution_confidence: %.2f\n", p.Confidence)
	}
	if p.General != "" {
		fmt.Fprintf(&b, "generalizability: %s\n", p.General)
	}
	fmt.Fprintf(&b, "pattern_id: %s\n", in.PatternID)
	fmt.Fprintf(&b, "---\n\n")

	fmt.Fprintf(&b, "# %s\n\n", fullName)
	fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(p.Description))

	fmt.Fprintf(&b, "## When to use\n\nApply when the user’s goal is similar to:\n\n> %s\n\n", strings.TrimSpace(in.Goal))

	if len(p.PreReq) > 0 {
		fmt.Fprintf(&b, "## Prerequisites\n\n")
		for _, x := range p.PreReq {
			if strings.TrimSpace(x) == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(x))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Procedure\n\n")
	for i, st := range p.Steps {
		if strings.TrimSpace(st) == "" {
			continue
		}
		fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(st))
	}
	b.WriteString("\n")

	if len(p.Success) > 0 {
		fmt.Fprintf(&b, "## Success criteria\n\n")
		for _, x := range p.Success {
			if strings.TrimSpace(x) == "" {
				continue
			}
			fmt.Fprintf(&b, "- [ ] %s\n", strings.TrimSpace(x))
		}
		b.WriteString("\n")
	}

	if p.Reasoning != "" {
		fmt.Fprintf(&b, "## Synthesizer note\n\n%s\n\n", strings.TrimSpace(p.Reasoning))
	}

	if len(in.LastToolSequence) > 0 {
		fmt.Fprintf(&b, "## Tool flow (from observed episode, deduplicated)\n\n`%s`\n", strings.Join(in.LastToolSequence, " → "))
	}

	fmt.Fprintf(&b, "\n## Evidence (automation)\n\n- Occurrences: %d — avg reward: %.3f\n", in.OccurrenceCount, in.AvgReward)

	return b.String()
}
