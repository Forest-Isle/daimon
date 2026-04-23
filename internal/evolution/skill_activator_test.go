package evolution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const promotableDraft = `---
name: auto_read_write
description: Task workflow distilled from 10 successful episodes (avg reward 0.85) using tools read and write.
status: draft
auto_generated: true
source: evolution
occurrence_count: 10
avg_reward: 0.85
tool_sequence:
  - read
  - write
---

# auto_read_write

## What this captures

A multi-tool workflow for reading and writing files.
`

const lowFrequencyDraft = `---
name: auto_low_freq
description: Task workflow distilled from 2 episodes (avg reward 0.90) using tools read and write.
status: draft
auto_generated: true
source: evolution
occurrence_count: 2
avg_reward: 0.90
tool_sequence:
  - read
  - write
---

# auto_low_freq

## What this captures

A skill that hasn't been seen enough times.
`

const destructiveDraft = `---
name: auto_destructive
description: Task workflow distilled from 10 episodes using destructive tools for cleanup.
status: draft
auto_generated: true
source: evolution
occurrence_count: 10
avg_reward: 0.85
tool_sequence:
  - read
  - rm -rf
  - write
---

# auto_destructive

## What this captures

A dangerous workflow.
`

func TestSkillActivator_PromoteDraft_Pass(t *testing.T) {
	draftsDir := t.TempDir()
	activeDir := t.TempDir()
	activator := NewSkillActivator(draftsDir, activeDir)

	draft := SkillDraft{
		Name:            "good_skill",
		Description:     "A valid skill with sufficient quality metrics for promotion.",
		ToolSequence:    []string{"read", "write"},
		OccurrenceCount: 10,
		AvgReward:       0.85,
	}

	promoted, gate, reason := activator.PromoteDraft(draft)
	if !promoted {
		t.Errorf("expected promotion, failed at gate %q: %s", gate, reason)
	}
}

func TestSkillActivator_PromoteDraft_FailFrequency(t *testing.T) {
	draftsDir := t.TempDir()
	activeDir := t.TempDir()
	activator := NewSkillActivator(draftsDir, activeDir)

	draft := SkillDraft{
		Name:            "low_freq",
		Description:     "A skill that hasn't been observed enough times to warrant promotion.",
		ToolSequence:    []string{"read", "write"},
		OccurrenceCount: 2,
		AvgReward:       0.9,
	}

	promoted, gate, _ := activator.PromoteDraft(draft)
	if promoted {
		t.Error("expected failure for low frequency")
	}
	if gate != "frequency" {
		t.Errorf("expected failure at frequency gate, got %q", gate)
	}
}

func TestSkillActivator_ScanAndPromote(t *testing.T) {
	draftsDir := t.TempDir()
	activeDir := t.TempDir()

	// Write a promotable draft
	os.WriteFile(
		filepath.Join(draftsDir, "SKILL_promotable.md"),
		[]byte(promotableDraft), 0o644,
	)

	activator := NewSkillActivator(draftsDir, activeDir)
	promoted, rejected := activator.ScanAndPromote()

	if promoted != 1 {
		t.Errorf("expected 1 promoted, got %d", promoted)
	}
	if rejected != 0 {
		t.Errorf("expected 0 rejected, got %d", rejected)
	}

	// Verify file moved to active dir
	activeEntries, _ := os.ReadDir(activeDir)
	if len(activeEntries) != 1 {
		t.Fatalf("expected 1 file in active dir, got %d", len(activeEntries))
	}

	// Verify status changed to active
	data, _ := os.ReadFile(filepath.Join(activeDir, activeEntries[0].Name()))
	if !strings.Contains(string(data), "status: active") {
		t.Error("promoted file should have status: active")
	}

	// Verify draft was removed
	draftEntries, _ := os.ReadDir(draftsDir)
	if len(draftEntries) != 0 {
		t.Errorf("draft should be removed after promotion, got %d files", len(draftEntries))
	}
}

func TestSkillActivator_ScanAndPromote_Rejects(t *testing.T) {
	draftsDir := t.TempDir()
	activeDir := t.TempDir()

	// Write a low-frequency draft (will fail frequency gate)
	os.WriteFile(
		filepath.Join(draftsDir, "SKILL_low.md"),
		[]byte(lowFrequencyDraft), 0o644,
	)

	activator := NewSkillActivator(draftsDir, activeDir)
	promoted, rejected := activator.ScanAndPromote()

	if promoted != 0 {
		t.Errorf("expected 0 promoted, got %d", promoted)
	}
	if rejected != 1 {
		t.Errorf("expected 1 rejected, got %d", rejected)
	}

	// Draft should still be in drafts dir
	draftEntries, _ := os.ReadDir(draftsDir)
	if len(draftEntries) != 1 {
		t.Errorf("rejected draft should remain, got %d files", len(draftEntries))
	}
}

func TestSkillActivator_ScanAndPromote_Mixed(t *testing.T) {
	draftsDir := t.TempDir()
	activeDir := t.TempDir()

	os.WriteFile(
		filepath.Join(draftsDir, "SKILL_good.md"),
		[]byte(promotableDraft), 0o644,
	)
	os.WriteFile(
		filepath.Join(draftsDir, "SKILL_bad.md"),
		[]byte(destructiveDraft), 0o644,
	)

	activator := NewSkillActivator(draftsDir, activeDir)
	promoted, rejected := activator.ScanAndPromote()

	if promoted != 1 {
		t.Errorf("expected 1 promoted, got %d", promoted)
	}
	if rejected != 1 {
		t.Errorf("expected 1 rejected, got %d", rejected)
	}
}

func TestSkillActivator_ScanAndPromote_EmptyDir(t *testing.T) {
	draftsDir := t.TempDir()
	activeDir := t.TempDir()

	activator := NewSkillActivator(draftsDir, activeDir)
	promoted, rejected := activator.ScanAndPromote()

	if promoted != 0 || rejected != 0 {
		t.Errorf("expected 0/0 for empty dir, got %d/%d", promoted, rejected)
	}
}

func TestSkillActivator_ScanAndPromote_NonexistentDir(t *testing.T) {
	activator := NewSkillActivator("/tmp/nonexistent-dir-xyz", t.TempDir())
	promoted, rejected := activator.ScanAndPromote()

	if promoted != 0 || rejected != 0 {
		t.Errorf("expected 0/0 for nonexistent dir, got %d/%d", promoted, rejected)
	}
}

func TestSkillActivator_CustomGates(t *testing.T) {
	draftsDir := t.TempDir()
	activeDir := t.TempDir()

	activator := NewSkillActivator(draftsDir, activeDir)
	// Override with only the sandbox gate (always passes)
	activator.SetGates([]SafetyGate{&SandboxTestGate{}})

	draft := SkillDraft{
		Name:            "anything",
		OccurrenceCount: 1, // would fail default frequency gate
	}

	promoted, _, _ := activator.PromoteDraft(draft)
	if !promoted {
		t.Error("expected promotion with only sandbox gate")
	}
}

func TestParseDraftFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte(promotableDraft), 0o644)

	draft, err := parseDraftFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Name != "auto_read_write" {
		t.Errorf("expected name 'auto_read_write', got %q", draft.Name)
	}
	if draft.OccurrenceCount != 10 {
		t.Errorf("expected occurrence_count 10, got %d", draft.OccurrenceCount)
	}
	if draft.AvgReward != 0.85 {
		t.Errorf("expected avg_reward 0.85, got %f", draft.AvgReward)
	}
	if len(draft.ToolSequence) != 2 {
		t.Errorf("expected 2 tools, got %d", len(draft.ToolSequence))
	}
}
