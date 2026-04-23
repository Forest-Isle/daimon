package evolution

import (
	"strings"
)

// SafetyGate validates whether a skill draft should be promoted to active.
type SafetyGate interface {
	Name() string
	Check(draft SkillDraft) (passed bool, reason string)
}

// SkillDraft represents a candidate skill awaiting validation through safety gates.
type SkillDraft struct {
	Name              string
	Description       string
	ToolSequence      []string
	OccurrenceCount   int
	AvgReward         float64
	LastCollapsed     string
	UserRejected      bool
	ConflictingSkills []string
}

// RunGates executes all gates in order. Returns (allPassed, failedGate, reason).
// Stops at the first failing gate.
func RunGates(draft SkillDraft, gates []SafetyGate) (bool, string, string) {
	for _, g := range gates {
		if passed, reason := g.Check(draft); !passed {
			return false, g.Name(), reason
		}
	}
	return true, "", ""
}

// DefaultGates returns the standard set of 7 safety gates in evaluation order.
func DefaultGates() []SafetyGate {
	return []SafetyGate{
		&FrequencyGate{MinOccurrences: 5},
		&RewardGate{MinAvgReward: 0.7},
		&DestructiveToolGate{},
		&UserConsentGate{},
		&SemanticGate{MinDescriptionLen: 20},
		&SandboxTestGate{},
		&ConflictGate{},
	}
}

// --- Gate 1: FrequencyGate ---

// FrequencyGate requires the draft to have been observed at least MinOccurrences times.
type FrequencyGate struct {
	MinOccurrences int
}

func (g *FrequencyGate) Name() string { return "frequency" }

func (g *FrequencyGate) Check(draft SkillDraft) (bool, string) {
	min := g.MinOccurrences
	if min <= 0 {
		min = 5
	}
	if draft.OccurrenceCount >= min {
		return true, ""
	}
	return false, "insufficient occurrences"
}

// --- Gate 2: RewardGate ---

// RewardGate requires the draft's average reward to meet a minimum threshold.
type RewardGate struct {
	MinAvgReward float64
}

func (g *RewardGate) Name() string { return "reward" }

func (g *RewardGate) Check(draft SkillDraft) (bool, string) {
	min := g.MinAvgReward
	if min <= 0 {
		min = 0.7
	}
	if draft.AvgReward >= min {
		return true, ""
	}
	return false, "average reward below threshold"
}

// --- Gate 3: DestructiveToolGate ---

// destructiveKeywords lists tool names or substrings that indicate destructive operations.
var destructiveKeywords = []string{
	"rm",
	"drop",
	"force-push",
	"reset --hard",
	"delete",
	"truncate",
	"destroy",
}

// DestructiveToolGate rejects drafts whose tool sequence contains destructive operations.
type DestructiveToolGate struct{}

func (g *DestructiveToolGate) Name() string { return "destructive_tool" }

func (g *DestructiveToolGate) Check(draft SkillDraft) (bool, string) {
	for _, tool := range draft.ToolSequence {
		lower := strings.ToLower(tool)
		for _, kw := range destructiveKeywords {
			if strings.Contains(lower, kw) {
				return false, "destructive tool detected: " + tool
			}
		}
	}
	return true, ""
}

// --- Gate 4: UserConsentGate ---

// UserConsentGate blocks drafts that the user has explicitly rejected.
type UserConsentGate struct{}

func (g *UserConsentGate) Name() string { return "user_consent" }

func (g *UserConsentGate) Check(draft SkillDraft) (bool, string) {
	if draft.UserRejected {
		return false, "user rejected this skill"
	}
	return true, ""
}

// --- Gate 5: SemanticGate ---

// SemanticGate validates that the draft has a meaningful description.
// This is a simple length check; a future version may use LLM-based validation.
type SemanticGate struct {
	MinDescriptionLen int
}

func (g *SemanticGate) Name() string { return "semantic" }

func (g *SemanticGate) Check(draft SkillDraft) (bool, string) {
	min := g.MinDescriptionLen
	if min <= 0 {
		min = 20
	}
	desc := strings.TrimSpace(draft.Description)
	if desc == "" {
		return false, "description is empty"
	}
	if len(desc) < min {
		return false, "description too short"
	}
	return true, ""
}

// --- Gate 6: SandboxTestGate ---

// SandboxTestGate is a placeholder for future sandbox-based validation.
// TODO: Implement actual sandbox execution to validate skill behavior.
type SandboxTestGate struct{}

func (g *SandboxTestGate) Name() string { return "sandbox_test" }

func (g *SandboxTestGate) Check(_ SkillDraft) (bool, string) {
	// TODO: run skill in a sandboxed environment to validate behavior
	return true, ""
}

// --- Gate 7: ConflictGate ---

// ConflictGate rejects drafts that conflict with existing active skills.
type ConflictGate struct{}

func (g *ConflictGate) Name() string { return "conflict" }

func (g *ConflictGate) Check(draft SkillDraft) (bool, string) {
	if len(draft.ConflictingSkills) > 0 {
		return false, "conflicts with: " + strings.Join(draft.ConflictingSkills, ", ")
	}
	return true, ""
}
