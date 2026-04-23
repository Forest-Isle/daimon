package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// SkillSynthesizer implements [Hook]. It watches completed episodes for
// recurring tool-usage patterns and, when a pattern exceeds the configured
// frequency and reward thresholds, writes a SKILL.md draft to disk.
//
// When a SkillProposer is set and LLMEnabled, drafts are generated as
// task-oriented procedures (Hermes-style) instead of raw tool chains.
type SkillSynthesizer struct {
	cfg       SynthesizerConfig
	tracker   *PatternTracker
	generated map[string]bool // tracks pattern keys that already have a draft
	proposer  SkillProposer
	activator *SkillActivator // optional: auto-promote drafts through safety gates
	mu        sync.Mutex
}

// NewSkillSynthesizer creates a synthesizer with a fresh pattern tracker.
func NewSkillSynthesizer(cfg SynthesizerConfig) *SkillSynthesizer {
	return &SkillSynthesizer{
		cfg:       cfg,
		tracker:   NewPatternTracker(),
		generated: make(map[string]bool),
	}
}

// SetSkillProposer wires an optional LLM-backed proposer (typically from the gateway).
func (s *SkillSynthesizer) SetSkillProposer(p SkillProposer) {
	s.proposer = p
}

// SetActivator wires an optional SkillActivator for automatic draft promotion.
// When set, newly written drafts are immediately validated through safety gates
// and promoted to the active directory if they pass.
func (s *SkillSynthesizer) SetActivator(a *SkillActivator) {
	s.activator = a
}

func (s *SkillSynthesizer) minUniqueTools() int {
	if s.cfg.MinUniqueTools < 1 {
		return 2
	}
	return s.cfg.MinUniqueTools
}

// DraftCount returns the number of skill drafts that have been generated.
func (s *SkillSynthesizer) DraftCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.generated)
}

// Name returns the hook identifier used in logs.
func (s *SkillSynthesizer) Name() string { return "skill_synthesizer" }

// OnReflectionComplete is a no-op for the synthesizer.
func (s *SkillSynthesizer) OnReflectionComplete(_ context.Context, _ ReflectionEvent) {}

// OnToolExecuted is a no-op for the synthesizer.
func (s *SkillSynthesizer) OnToolExecuted(_ context.Context, _ ToolExecEvent) {}

// OnEpisodeComplete feeds the episode into the pattern tracker, then checks
// whether any pattern has crossed both thresholds. For every NEW qualifying
// pattern a SKILL.md draft is written to cfg.DraftsDir.
func (s *SkillSynthesizer) OnEpisodeComplete(ctx context.Context, event EpisodeEvent) {
	if !s.cfg.Enabled {
		return
	}

	s.tracker.TrackEpisode(event)

	candidates := s.tracker.GetCandidates(
		s.cfg.PatternThreshold,
		s.cfg.RewardThreshold,
		s.minUniqueTools(),
	)
	if len(candidates) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range candidates {
		c := &candidates[i]
		if s.generated[c.ID] {
			continue
		}

		var content string
		var fileKey string
		usedLLM := false
		if s.proposer != nil && s.cfg.LLMEnabled {
			in := SkillProposeInput{
				PatternID:        c.ID,
				Tools:            c.Tools,
				OccurrenceCount:  c.Count,
				AvgReward:        c.AvgReward,
				FirstSeen:        c.FirstSeen,
				LastSeen:         c.LastSeen,
				Goal:             c.LastGoal,
				Complexity:       c.LastComplexity,
				Succeeded:        c.LastSucceeded,
				LastTotalReward:  c.LastTotalReward,
				LessonsLearned:   c.LastLessons,
				LastToolSequence: c.LastCollapsedSequence,
			}
			md, stem, err := s.proposer.Propose(ctx, in)
			if err != nil {
				slog.Warn("skill_synthesizer: LLM draft failed, using heuristic",
					"pattern", c.ID, "err", err)
			} else if strings.TrimSpace(md) != "" && strings.TrimSpace(stem) != "" {
				content = md
				fileKey = stem
				usedLLM = true
			}
		}
		if content == "" {
			content = s.generateHeuristicDraft(*c)
			fileKey = c.ID
		}

		if err := s.writeDraft(fileKey, content); err != nil {
			slog.Warn("skill_synthesizer: failed to write draft",
				"pattern", c.ID, "error", err)
			continue
		}

		s.generated[c.ID] = true
		slog.Info("skill_synthesizer: draft generated",
			"pattern", c.ID,
			"count", c.Count,
			"avg_reward", c.AvgReward,
			"llm", usedLLM,
		)

		// Attempt auto-promotion through safety gates if activator is set.
		if s.activator != nil {
			draft := SkillDraft{
				Name:            fileKey,
				Description:     content,
				ToolSequence:    c.Tools,
				OccurrenceCount: c.Count,
				AvgReward:       c.AvgReward,
				LastCollapsed:   strings.Join(c.LastCollapsedSequence, " → "),
			}
			if promoted, gate, reason := s.activator.PromoteDraft(draft); promoted {
				slog.Info("skill_synthesizer: draft auto-promoted", "pattern", c.ID)
			} else {
				slog.Info("skill_synthesizer: draft not auto-promoted",
					"pattern", c.ID, "gate", gate, "reason", reason)
			}
		}
	}
}

var draftFileStemSanitizer = regexp.MustCompile(`[^a-z0-9_]+`)

// SanitizeDraftFileStem normalizes a pattern id or LLM-provided name for a filename prefix.
func SanitizeDraftFileStem(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = draftFileStemSanitizer.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "skill"
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// generateHeuristicDraft produces a task-aware template without an LLM.
func (s *SkillSynthesizer) generateHeuristicDraft(pattern ToolPattern) string {
	toolList := joinHumanReadableList(pattern.Tools)
	name := "auto_" + SanitizeDraftFileStem(strings.ReplaceAll(pattern.ID, "|", "_"))

	var collapsedFlow string
	if len(pattern.LastCollapsedSequence) > 0 {
		collapsedFlow = strings.Join(pattern.LastCollapsedSequence, " → ")
	} else {
		collapsedFlow = strings.Join(pattern.Tools, " → ")
	}
	if collapsedFlow == "" {
		collapsedFlow = "—"
	}

	desc := fmt.Sprintf(
		"Task workflow distilled from %d successful episodes (avg reward %.2f) using tools: %s.",
		pattern.Count, pattern.AvgReward, toolList,
	)

	var b strings.Builder
	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "description: %s\n", desc)
	fmt.Fprintf(&b, "status: draft\n")
	fmt.Fprintf(&b, "auto_generated: true\n")
	fmt.Fprintf(&b, "source: evolution\n")
	fmt.Fprintf(&b, "---\n\n")

	fmt.Fprintf(&b, "# %s\n\n", name)
	fmt.Fprintf(&b, "## What this captures\n\n")
	fmt.Fprintf(&b, "This draft was synthesized from **repeated successful runs** of a multi-tool workflow, ")
	fmt.Fprintf(&b, "not from a single long bash session. The statistics below are aggregated across episodes.\n\n")

	if strings.TrimSpace(pattern.LastGoal) != "" {
		fmt.Fprintf(&b, "## User goal (most recent session that matched this pattern)\n\n> %s\n\n", strings.TrimSpace(pattern.LastGoal))
	}
	if strings.TrimSpace(pattern.LastComplexity) != "" {
		fmt.Fprintf(&b, "## Task complexity (from that session)\n\n%s\n\n", strings.TrimSpace(pattern.LastComplexity))
	}

	fmt.Fprintf(&b, "## When to use\n\n")
	fmt.Fprintf(&b, "Apply when the task requires working with these tools together: **%s**.\n\n", toolList)

	fmt.Fprintf(&b, "## Suggested procedure\n\n")
	fmt.Fprintf(&b, "1. Clarify the deliverable and constraints from the user request.\n")
	fmt.Fprintf(&b, "2. Follow a phase that matches this collapsed tool flow (duplicates removed):\n\n")
	fmt.Fprintf(&b, "   `%s`\n\n", collapsedFlow)
	fmt.Fprintf(&b, "3. Verify results (tests, file contents, or command output) before declaring done.\n\n")

	if len(pattern.LastLessons) > 0 {
		fmt.Fprintf(&b, "## Notes from prior reflections\n\n")
		for _, l := range pattern.LastLessons {
			if strings.TrimSpace(l) == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(l))
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Evidence (automation)\n\n")
	fmt.Fprintf(&b, "- **Pattern key:** `%s`\n", pattern.ID)
	fmt.Fprintf(&b, "- **Occurrences:** %d\n", pattern.Count)
	fmt.Fprintf(&b, "- **Average reward:** %.3f\n", pattern.AvgReward)
	fmt.Fprintf(&b, "- **First seen:** %s\n", pattern.FirstSeen.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "- **Last seen:** %s\n\n", pattern.LastSeen.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "## Promoting to production\n\n")
	fmt.Fprintf(&b, "Edit this file: add domain-specific steps, failure modes, and examples. ")
	fmt.Fprintf(&b, "Then move it out of `drafts/` or change `status` to `active` per your team convention.\n")

	return b.String()
}

func joinHumanReadableList(tools []string) string {
	if len(tools) == 0 {
		return ""
	}
	// tools are sorted; list unique in order of first appearance in sorted list = sorted unique
	uniq := make([]string, 0, len(tools))
	for i, t := range tools {
		if i == 0 || t != tools[i-1] {
			uniq = append(uniq, t)
		}
	}
	return strings.Join(uniq, ", ")
}

// writeDraft persists the draft to DraftsDir, creating the directory if needed.
func (s *SkillSynthesizer) writeDraft(fileStem, content string) error {
	dir := s.cfg.DraftsDir
	if dir == "" {
		return fmt.Errorf("synthesizer: DraftsDir is not configured")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create drafts dir: %w", err)
	}

	stem := SanitizeDraftFileStem(fileStem)
	if stem == "" {
		stem = "skill"
	}
	filename := "SKILL_" + stem + ".md"
	path := filepath.Join(dir, filename)

	return os.WriteFile(path, []byte(content), 0o644)
}