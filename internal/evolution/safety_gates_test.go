package evolution

import (
	"strings"
	"testing"
)

func TestFrequencyGate_Pass(t *testing.T) {
	g := &FrequencyGate{MinOccurrences: 5}
	draft := SkillDraft{OccurrenceCount: 5}
	if passed, _ := g.Check(draft); !passed {
		t.Error("expected pass for OccurrenceCount == 5")
	}
}

func TestFrequencyGate_Fail(t *testing.T) {
	g := &FrequencyGate{MinOccurrences: 5}
	draft := SkillDraft{OccurrenceCount: 4}
	if passed, _ := g.Check(draft); passed {
		t.Error("expected fail for OccurrenceCount == 4")
	}
}

func TestFrequencyGate_DefaultThreshold(t *testing.T) {
	g := &FrequencyGate{} // MinOccurrences == 0 → default 5
	draft := SkillDraft{OccurrenceCount: 5}
	if passed, _ := g.Check(draft); !passed {
		t.Error("expected pass with default threshold and count=5")
	}
}

func TestRewardGate_Pass(t *testing.T) {
	g := &RewardGate{MinAvgReward: 0.7}
	draft := SkillDraft{AvgReward: 0.75}
	if passed, _ := g.Check(draft); !passed {
		t.Error("expected pass for AvgReward 0.75")
	}
}

func TestRewardGate_Fail(t *testing.T) {
	g := &RewardGate{MinAvgReward: 0.7}
	draft := SkillDraft{AvgReward: 0.5}
	if passed, _ := g.Check(draft); passed {
		t.Error("expected fail for AvgReward 0.5")
	}
}

func TestRewardGate_ExactThreshold(t *testing.T) {
	g := &RewardGate{MinAvgReward: 0.7}
	draft := SkillDraft{AvgReward: 0.7}
	if passed, _ := g.Check(draft); !passed {
		t.Error("expected pass for AvgReward exactly at threshold")
	}
}

func TestDestructiveToolGate_Pass(t *testing.T) {
	g := &DestructiveToolGate{}
	draft := SkillDraft{ToolSequence: []string{"file_read", "bash", "file_write"}}
	if passed, _ := g.Check(draft); !passed {
		t.Error("expected pass for safe tools")
	}
}

func TestDestructiveToolGate_FailRM(t *testing.T) {
	g := &DestructiveToolGate{}
	draft := SkillDraft{ToolSequence: []string{"file_read", "rm -rf", "file_write"}}
	passed, reason := g.Check(draft)
	if passed {
		t.Error("expected fail for rm -rf")
	}
	if !strings.Contains(reason, "rm -rf") {
		t.Errorf("expected reason to mention rm -rf, got: %s", reason)
	}
}

func TestDestructiveToolGate_FailForcePush(t *testing.T) {
	g := &DestructiveToolGate{}
	draft := SkillDraft{ToolSequence: []string{"git force-push"}}
	if passed, _ := g.Check(draft); passed {
		t.Error("expected fail for force-push")
	}
}

func TestDestructiveToolGate_FailResetHard(t *testing.T) {
	g := &DestructiveToolGate{}
	draft := SkillDraft{ToolSequence: []string{"git reset --hard"}}
	if passed, _ := g.Check(draft); passed {
		t.Error("expected fail for reset --hard")
	}
}

func TestDestructiveToolGate_EmptySequence(t *testing.T) {
	g := &DestructiveToolGate{}
	draft := SkillDraft{ToolSequence: nil}
	if passed, _ := g.Check(draft); !passed {
		t.Error("expected pass for empty tool sequence")
	}
}

func TestUserConsentGate_Pass(t *testing.T) {
	g := &UserConsentGate{}
	draft := SkillDraft{UserRejected: false}
	if passed, _ := g.Check(draft); !passed {
		t.Error("expected pass when user has not rejected")
	}
}

func TestUserConsentGate_Fail(t *testing.T) {
	g := &UserConsentGate{}
	draft := SkillDraft{UserRejected: true}
	if passed, _ := g.Check(draft); passed {
		t.Error("expected fail when user has rejected")
	}
}

func TestSemanticGate_Pass(t *testing.T) {
	g := &SemanticGate{MinDescriptionLen: 20}
	draft := SkillDraft{Description: "This is a sufficiently long description for testing purposes."}
	if passed, _ := g.Check(draft); !passed {
		t.Error("expected pass for long description")
	}
}

func TestSemanticGate_FailEmpty(t *testing.T) {
	g := &SemanticGate{MinDescriptionLen: 20}
	draft := SkillDraft{Description: ""}
	passed, reason := g.Check(draft)
	if passed {
		t.Error("expected fail for empty description")
	}
	if reason != "description is empty" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestSemanticGate_FailShort(t *testing.T) {
	g := &SemanticGate{MinDescriptionLen: 20}
	draft := SkillDraft{Description: "short"}
	passed, reason := g.Check(draft)
	if passed {
		t.Error("expected fail for short description")
	}
	if reason != "description too short" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestSemanticGate_WhitespaceOnly(t *testing.T) {
	g := &SemanticGate{MinDescriptionLen: 20}
	draft := SkillDraft{Description: "   \t\n  "}
	if passed, _ := g.Check(draft); passed {
		t.Error("expected fail for whitespace-only description")
	}
}

func TestSandboxTestGate_AlwaysPasses(t *testing.T) {
	g := &SandboxTestGate{}
	draft := SkillDraft{}
	if passed, _ := g.Check(draft); !passed {
		t.Error("sandbox gate should always pass (placeholder)")
	}
}

func TestConflictGate_Pass(t *testing.T) {
	g := &ConflictGate{}
	draft := SkillDraft{ConflictingSkills: nil}
	if passed, _ := g.Check(draft); !passed {
		t.Error("expected pass when no conflicts")
	}
}

func TestConflictGate_PassEmpty(t *testing.T) {
	g := &ConflictGate{}
	draft := SkillDraft{ConflictingSkills: []string{}}
	if passed, _ := g.Check(draft); !passed {
		t.Error("expected pass for empty conflict list")
	}
}

func TestConflictGate_Fail(t *testing.T) {
	g := &ConflictGate{}
	draft := SkillDraft{ConflictingSkills: []string{"existing_skill"}}
	passed, reason := g.Check(draft)
	if passed {
		t.Error("expected fail when conflicts exist")
	}
	if !strings.Contains(reason, "existing_skill") {
		t.Errorf("expected reason to mention conflicting skill, got: %s", reason)
	}
}

func TestRunGates_AllPass(t *testing.T) {
	draft := SkillDraft{
		Name:            "test_skill",
		Description:     "A well-formed description for a test skill draft.",
		ToolSequence:    []string{"read", "write"},
		OccurrenceCount: 10,
		AvgReward:       0.9,
	}
	gates := DefaultGates()
	passed, gate, reason := RunGates(draft, gates)
	if !passed {
		t.Errorf("expected all gates to pass, failed at %q: %s", gate, reason)
	}
}

func TestRunGates_StopsAtFirstFailure(t *testing.T) {
	draft := SkillDraft{
		OccurrenceCount: 1,   // fails frequency
		AvgReward:       0.1, // also fails reward
	}
	gates := DefaultGates()
	passed, gate, _ := RunGates(draft, gates)
	if passed {
		t.Error("expected failure")
	}
	if gate != "frequency" {
		t.Errorf("expected first failure at frequency gate, got %q", gate)
	}
}

func TestRunGates_EmptyGates(t *testing.T) {
	draft := SkillDraft{}
	passed, gate, reason := RunGates(draft, nil)
	if !passed {
		t.Errorf("empty gates should pass, failed at %q: %s", gate, reason)
	}
}

func TestDefaultGates_Count(t *testing.T) {
	gates := DefaultGates()
	if len(gates) != 7 {
		t.Errorf("expected 7 default gates, got %d", len(gates))
	}
}

func TestDefaultGates_Names(t *testing.T) {
	gates := DefaultGates()
	expected := []string{
		"frequency", "reward", "destructive_tool",
		"user_consent", "semantic", "sandbox_test", "conflict",
	}
	for i, g := range gates {
		if g.Name() != expected[i] {
			t.Errorf("gate %d: expected name %q, got %q", i, expected[i], g.Name())
		}
	}
}
