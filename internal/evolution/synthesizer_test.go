package evolution

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helper: create a minimal episode event with the given tool sequence and reward.
func episodeWith(tools []string, reward float64) EpisodeEvent {
	return EpisodeEvent{
		SessionID:    "sess-1",
		EpisodeID:    "ep-1",
		Goal:         "unit test goal",
		Complexity:   "low",
		Succeeded:    true,
		TotalReward:  reward,
		ToolSequence: tools,
		Timestamp:    time.Now(),
	}
}

func TestSynthesizer_Name(t *testing.T) {
	s := NewSkillSynthesizer(SynthesizerConfig{})
	if s.Name() != "skill_synthesizer" {
		t.Errorf("Name() = %q, want %q", s.Name(), "skill_synthesizer")
	}
}

func TestSynthesizer_HookInterface(t *testing.T) {
	// Compile-time check: SkillSynthesizer must satisfy Hook.
	var _ Hook = (*SkillSynthesizer)(nil)
}

func TestSynthesizer_NoopMethods(t *testing.T) {
	s := NewSkillSynthesizer(SynthesizerConfig{Enabled: true})
	ctx := context.Background()

	// Must not panic.
	s.OnReflectionComplete(ctx, ReflectionEvent{})
	s.OnToolExecuted(ctx, ToolExecEvent{})
}

func TestSynthesizer_DisabledProducesNoDrafts(t *testing.T) {
	dir := t.TempDir()
	s := NewSkillSynthesizer(SynthesizerConfig{
		Enabled:          false,
		PatternThreshold: 2,
		RewardThreshold:  0.5,
		DraftsDir:        dir,
	})
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		s.OnEpisodeComplete(ctx, episodeWith([]string{"a", "b"}, 0.9))
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("disabled synthesizer wrote %d files, want 0", len(entries))
	}
}

func TestSynthesizer_BasicDraftGeneration(t *testing.T) {
	dir := t.TempDir()
	s := NewSkillSynthesizer(SynthesizerConfig{
		Enabled:          true,
		PatternThreshold: 3,
		RewardThreshold:  0.5,
		DraftsDir:        dir,
	})
	ctx := context.Background()

	// First two episodes: below threshold — no file yet.
	for i := 0; i < 2; i++ {
		s.OnEpisodeComplete(ctx, episodeWith([]string{"read", "write"}, 0.8))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("below threshold: want 0 files, got %d", len(entries))
	}

	// Third episode crosses the threshold.
	s.OnEpisodeComplete(ctx, episodeWith([]string{"write", "read"}, 0.7))

	entries, _ = os.ReadDir(dir)
	if len(entries) == 0 {
		t.Fatal("expected at least 1 draft file after threshold crossed")
	}

	// Verify the expected filename exists.
	found := false
	for _, e := range entries {
		if e.Name() == "SKILL_read_write.md" {
			found = true
		}
	}
	if !found {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("SKILL_read_write.md not found among %v", names)
	}
}

func TestSynthesizer_DraftContent(t *testing.T) {
	dir := t.TempDir()
	s := NewSkillSynthesizer(SynthesizerConfig{
		Enabled:          true,
		PatternThreshold: 2,
		RewardThreshold:  0.0,
		DraftsDir:        dir,
	})
	ctx := context.Background()

	s.OnEpisodeComplete(ctx, episodeWith([]string{"bash", "file_write"}, 0.9))
	s.OnEpisodeComplete(ctx, episodeWith([]string{"file_write", "bash"}, 0.7))

	path := filepath.Join(dir, "SKILL_bash_file_write.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"---",
		"name: auto_bash_file_write",
		"status: draft",
		"auto_generated: true",
		"source: evolution",
		"description:",
		"## Suggested procedure",
		"## What this captures",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("draft missing %q\n---\n%s", want, content)
		}
	}
}

func TestSynthesizer_Dedup(t *testing.T) {
	dir := t.TempDir()
	s := NewSkillSynthesizer(SynthesizerConfig{
		Enabled:          true,
		PatternThreshold: 2,
		RewardThreshold:  0.0,
		DraftsDir:        dir,
	})
	ctx := context.Background()

	// Trigger the draft.
	s.OnEpisodeComplete(ctx, episodeWith([]string{"x", "y"}, 1.0))
	s.OnEpisodeComplete(ctx, episodeWith([]string{"x", "y"}, 1.0))

	entries1, _ := os.ReadDir(dir)

	// Send many more episodes with the same pattern.
	for i := 0; i < 10; i++ {
		s.OnEpisodeComplete(ctx, episodeWith([]string{"y", "x"}, 1.0))
	}

	entries2, _ := os.ReadDir(dir)
	if len(entries2) != len(entries1) {
		t.Errorf("dedup failed: files went from %d to %d", len(entries1), len(entries2))
	}
}

func TestSynthesizer_RewardThresholdFilter(t *testing.T) {
	dir := t.TempDir()
	s := NewSkillSynthesizer(SynthesizerConfig{
		Enabled:          true,
		PatternThreshold: 2,
		RewardThreshold:  0.8,
		DraftsDir:        dir,
	})
	ctx := context.Background()

	// Low-reward pattern: many episodes but avg reward below 0.8.
	for i := 0; i < 10; i++ {
		s.OnEpisodeComplete(ctx, episodeWith([]string{"a", "b"}, 0.3))
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("low-reward pattern should not produce drafts, got %d files", len(entries))
	}
}

func TestSynthesizer_FiveEpisodesCommonSequence(t *testing.T) {
	// Five episodes share [grep, edit, test] as a common sub-sequence.
	dir := t.TempDir()
	s := NewSkillSynthesizer(SynthesizerConfig{
		Enabled:          true,
		PatternThreshold: 5,
		RewardThreshold:  0.6,
		DraftsDir:        dir,
	})
	ctx := context.Background()

	sequences := [][]string{
		{"grep", "edit", "test"},
		{"read", "grep", "edit", "test"},
		{"grep", "edit", "test", "deploy"},
		{"grep", "edit", "test"},
		{"lint", "grep", "edit", "test"},
	}

	for _, seq := range sequences {
		s.OnEpisodeComplete(ctx, episodeWith(seq, 0.85))
	}

	// The pair "edit|grep" and "edit|test" and "grep|test" should all appear
	// at least 5 times. Verify at least one draft was generated.
	entries, _ := os.ReadDir(dir)
	if len(entries) == 0 {
		t.Fatal("expected at least 1 draft from 5 shared episodes")
	}

	// Specifically check that the common pair "edit|grep" got a draft.
	path := filepath.Join(dir, "SKILL_edit_grep.md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("expected SKILL_edit_grep.md to exist")
	}
}

// TestSynthesizer_AcceptanceHeuristic validates post-refactor drafts: run-length collapse,
// min-unique filter, and task context (goal + lessons) — not a raw tool stack.
func TestSynthesizer_AcceptanceHeuristic(t *testing.T) {
	dir := t.TempDir()
	s := NewSkillSynthesizer(SynthesizerConfig{
		Enabled:          true,
		PatternThreshold: 3,
		RewardThreshold:  0.5,
		MinUniqueTools:   2,
		DraftsDir:        dir,
		LLMEnabled:       false,
	})
	ctx := context.Background()
	seq := []string{
		"file_read", "file_read", "bash", "bash", "file_write", "file_write",
	}
	goal := "Add regression tests and fix the failing CI job"
	lessons := []string{
		"Run tests locally before push.",
		"Prefer smaller commits when fixing CI.",
	}
	for range 3 {
		s.OnEpisodeComplete(ctx, EpisodeEvent{
			SessionID:      "s-acc",
			Goal:           goal,
			Complexity:     "complex",
			Succeeded:      true,
			TotalReward:    0.86,
			ToolSequence:   append([]string(nil), seq...),
			LessonsLearned: lessons,
			Timestamp:      time.Now(),
		})
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) == 0 {
		t.Fatal("expected at least one draft")
	}
	// file_read|file_write|bash after sorting per window: expect file_read+file_write pattern etc.
	var content string
	for _, e := range ents {
		b, eerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if eerr != nil {
			t.Fatal(eerr)
		}
		if strings.HasPrefix(e.Name(), "SKILL_") && strings.HasSuffix(e.Name(), ".md") {
			content = string(b)
			t.Logf("=== sample draft %s ===\n%s", e.Name(), content)
			break
		}
	}
	if content == "" {
		t.Fatal("no SKILL_*.md content read")
	}
	for _, need := range []string{
		"## What this captures",
		"## User goal (most recent session that matched this pattern)",
		goal,
		"## Task complexity (from that session)",
		"complex",
		"## Notes from prior reflections",
		"Run tests locally",
		"## Suggested procedure",
	} {
		if !strings.Contains(content, need) {
			t.Errorf("draft should contain %q", need)
		}
	}
	if strings.Contains(content, "bash -> bash -> bash") || strings.Contains(content, "bash → bash → bash") {
		t.Error("draft should not advertise raw repeated bash as the core workflow")
	}
}

func TestSynthesizer_CreatesDirAutomatically(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "deep", "nested", "drafts")

	s := NewSkillSynthesizer(SynthesizerConfig{
		Enabled:          true,
		PatternThreshold: 1,
		RewardThreshold:  0.0,
		DraftsDir:        nested,
	})

	s.OnEpisodeComplete(context.Background(), episodeWith([]string{"a", "b"}, 1))

	entries, err := os.ReadDir(nested)
	if err != nil {
		t.Fatalf("nested dir not created: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected at least 1 file in auto-created dir")
	}
}
