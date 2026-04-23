package evolution

import (
	"context"
	"time"
)

// SkillProposer turns a statistically stable tool pattern plus recent episode context
// into a human-usable SKILL.md body (Hermes-style procedure, not raw tool spam).
// Implemented in the gateway using the main LLM provider to avoid import cycles.
type SkillProposer interface {
	Propose(ctx context.Context, in SkillProposeInput) (markdown string, fileStem string, err error)
}

// SkillProposeInput bundles pattern aggregates with the last episode that updated the pattern.
type SkillProposeInput struct {
	PatternID       string
	Tools           []string // sorted canonical multiset (pattern.Tools)
	OccurrenceCount int
	AvgReward       float64
	FirstSeen       time.Time
	LastSeen        time.Time

	Goal             string
	Complexity       string
	Succeeded        bool
	LastTotalReward  float64
	LessonsLearned   []string
	LastToolSequence []string // run-length collapsed full-episode sequence; Goal/Complexity/Succeeded from that episode
}
