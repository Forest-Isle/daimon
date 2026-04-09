package rl

import (
	"context"
	"log/slog"
)

// ExperienceAdder is the subset of Trainer that MemoryRLHandler needs.
// Using a minimal interface enables testing with mocks and decouples
// MemoryRLHandler from the full Trainer type.
type ExperienceAdder interface {
	AddExperience(exp Experience)
}

// MemoryRLRewards configures reward magnitudes for memory lifecycle events.
// Positive values reinforce the corresponding behaviour; negative values
// penalise it (e.g., ConflictReward discourages storing conflicting facts).
type MemoryRLRewards struct {
	AddReward      float64 // Reward for successfully storing a new memory.
	UpdateReward   float64 // Reward for replacing an outdated memory.
	DeleteReward   float64 // Reward for invalidating/archiving a memory.
	ConflictReward float64 // Penalty for producing a fact that conflicts with existing memory.
}

// DefaultMemoryRLRewards returns sensible default reward magnitudes.
// Values are intentionally small so that memory events do not dominate
// the overall reward signal relative to episode-level outcomes.
func DefaultMemoryRLRewards() MemoryRLRewards {
	return MemoryRLRewards{
		AddReward:      0.1,
		UpdateReward:   0.3,
		DeleteReward:   0.2,
		ConflictReward: -0.5,
	}
}

// MemoryRLHandler converts memory lifecycle events into RL experiences and
// forwards them to the trainer's replay buffer via the ExperienceAdder interface.
//
// It satisfies the memory.RLEventHandler interface; that interface compliance is
// asserted at the gateway wiring layer (Task 5) to avoid a circular import between
// the rl and memory packages.
//
// All methods are fire-and-forget: they never block and never return errors.
// When adder is nil (e.g., RL is disabled), all methods are safe no-ops.
type MemoryRLHandler struct {
	adder   ExperienceAdder
	rewards MemoryRLRewards
}

// NewMemoryRLHandler constructs a MemoryRLHandler.
// Passing nil for adder is valid; every event method becomes a no-op.
func NewMemoryRLHandler(adder ExperienceAdder, rewards MemoryRLRewards) *MemoryRLHandler {
	return &MemoryRLHandler{adder: adder, rewards: rewards}
}

// OnMemoryAdd is called after a new memory fact is successfully stored.
func (h *MemoryRLHandler) OnMemoryAdd(_ context.Context, factID, content string, importance int) {
	h.emit(h.rewards.AddReward, "memory_add", factID)
}

// OnMemoryUpdate is called when an existing memory is replaced with a newer version.
// oldID identifies the archived entry; newID identifies the replacement.
func (h *MemoryRLHandler) OnMemoryUpdate(_ context.Context, oldID, newID, content string) {
	h.emit(h.rewards.UpdateReward, "memory_update", newID)
}

// OnMemoryDelete is called after a memory fact is archived (invalidated).
func (h *MemoryRLHandler) OnMemoryDelete(_ context.Context, factID string) {
	h.emit(h.rewards.DeleteReward, "memory_delete", factID)
}

// OnMemoryConflict is called when a new fact conflicts with one or more existing memories.
// The negative reward discourages the agent from storing contradictory information.
func (h *MemoryRLHandler) OnMemoryConflict(_ context.Context, factID string, conflictIDs []string) {
	h.emit(h.rewards.ConflictReward, "memory_conflict", factID)
	slog.Debug("rl: memory conflict detected", "fact", factID, "conflicts", conflictIDs)
}

// emit constructs a bandit-level Experience and adds it to the replay buffer.
// It is a no-op when adder is nil.
func (h *MemoryRLHandler) emit(reward float64, eventType, factID string) {
	if h.adder == nil {
		return
	}
	h.adder.AddExperience(Experience{
		State:  &RLState{},
		Action: []float64{reward},
		Reward: reward,
		Done:   true,
		Level:  LevelBandit,
	})
	slog.Debug("rl: memory event recorded", "type", eventType, "id", factID, "reward", reward)
}
