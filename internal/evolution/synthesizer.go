package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SkillSynthesizer implements [Hook]. It watches completed episodes for
// recurring tool-usage patterns and, when a pattern exceeds the configured
// frequency and reward thresholds, writes a SKILL.md draft to disk.
//
// Each unique pattern produces at most one draft file (dedup via the generated
// map). The synthesizer is safe for concurrent use.
type SkillSynthesizer struct {
	cfg       SynthesizerConfig
	tracker   *PatternTracker
	generated map[string]bool // tracks pattern keys that already have a draft
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
func (s *SkillSynthesizer) OnEpisodeComplete(_ context.Context, event EpisodeEvent) {
	if !s.cfg.Enabled {
		return
	}

	s.tracker.TrackEpisode(event)

	candidates := s.tracker.GetCandidates(s.cfg.PatternThreshold, s.cfg.RewardThreshold)
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

		content := s.generateSkillDraft(*c)
		if err := s.writeDraft(c.ID, content); err != nil {
			slog.Warn("skill_synthesizer: failed to write draft",
				"pattern", c.ID, "error", err)
			continue
		}

		s.generated[c.ID] = true
		slog.Info("skill_synthesizer: draft generated",
			"pattern", c.ID,
			"count", c.Count,
			"avg_reward", c.AvgReward)
	}
}

// generateSkillDraft returns SKILL.md content with YAML front-matter and a
// human-readable markdown body describing the discovered pattern.
func (s *SkillSynthesizer) generateSkillDraft(pattern ToolPattern) string {
	toolList := strings.Join(pattern.Tools, ", ")

	name := "auto_" + strings.ReplaceAll(pattern.ID, "|", "_")

	description := fmt.Sprintf(
		"Skill auto-generated from %d occurrences of tool pattern [%s] with avg reward %.3f.",
		pattern.Count, pattern.ID, pattern.AvgReward,
	)

	var b strings.Builder

	// YAML front-matter
	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "description: %s\n", description)
	fmt.Fprintf(&b, "status: draft\n")
	fmt.Fprintf(&b, "auto_generated: true\n")
	fmt.Fprintf(&b, "---\n\n")

	// Markdown body
	fmt.Fprintf(&b, "# %s\n\n", name)
	fmt.Fprintf(&b, "## Pattern Summary\n\n")
	fmt.Fprintf(&b, "This skill was automatically synthesized from observed tool-usage patterns.\n\n")
	fmt.Fprintf(&b, "- **Tools:** %s\n", toolList)
	fmt.Fprintf(&b, "- **Occurrences:** %d\n", pattern.Count)
	fmt.Fprintf(&b, "- **Average Reward:** %.3f\n", pattern.AvgReward)
	fmt.Fprintf(&b, "- **First Seen:** %s\n", pattern.FirstSeen.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "- **Last Seen:** %s\n\n", pattern.LastSeen.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "## Usage\n\n")
	fmt.Fprintf(&b, "Review and refine the tool sequence below before promoting to production.\n\n")
	fmt.Fprintf(&b, "```\n%s\n```\n", strings.Join(pattern.Tools, " -> "))

	return b.String()
}

// writeDraft persists the draft to DraftsDir, creating the directory if needed.
func (s *SkillSynthesizer) writeDraft(patternKey, content string) error {
	dir := s.cfg.DraftsDir
	if dir == "" {
		return fmt.Errorf("synthesizer: DraftsDir is not configured")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create drafts dir: %w", err)
	}

	filename := "SKILL_" + strings.ReplaceAll(patternKey, "|", "_") + ".md"
	path := filepath.Join(dir, filename)

	return os.WriteFile(path, []byte(content), 0o644)
}
