package rl

import (
	"encoding/binary"
	"math"
)

// Action levels in the RL system.
const (
	LevelBandit = "bandit"
	LevelPPO    = "ppo"
	LevelDQN    = "dqn"
)

// ToolSelectionAction represents a bandit arm selection for tool choice.
type ToolSelectionAction struct {
	ToolName   string
	ToolIndex  int
	Confidence float64 // Thompson sampling confidence
}

// PlanStrategyAction represents PPO output for plan parameter adjustment.
type PlanStrategyAction struct {
	SubTaskCountBias float64 // [-1, 1] bias on number of subtasks
	ParallelBias     float64 // [-1, 1] bias on parallelism
	ConfidenceAdj    float64 // [-0.2, 0.2] adjustment to confidence threshold
}

// ToVector converts plan strategy to a float64 slice.
func (a *PlanStrategyAction) ToVector() []float64 {
	return []float64{a.SubTaskCountBias, a.ParallelBias, a.ConfidenceAdj}
}

// PlanStrategyFromVector creates a PlanStrategyAction from a vector.
func PlanStrategyFromVector(v []float64) *PlanStrategyAction {
	if len(v) < 3 {
		return &PlanStrategyAction{}
	}
	return &PlanStrategyAction{
		SubTaskCountBias: clamp(v[0], -1, 1),
		ParallelBias:     clamp(v[1], -1, 1),
		ConfidenceAdj:    clamp(v[2], -0.2, 0.2),
	}
}

// ReplanActionType represents the DQN output for replan decisions.
type ReplanActionType int

const (
	ReplanActionContinue ReplanActionType = 0
	ReplanActionAdjust   ReplanActionType = 1
	ReplanActionAbort    ReplanActionType = 2
	NumReplanActions                      = 3
)

// String returns the string representation of a replan action.
func (a ReplanActionType) String() string {
	switch a {
	case ReplanActionContinue:
		return "continue"
	case ReplanActionAdjust:
		return "adjust"
	case ReplanActionAbort:
		return "abort"
	default:
		return "unknown"
	}
}

// EncodeAction serializes an action to bytes.
// Format: [level_byte][action_data...]
func EncodeAction(level string, actionData []float64) []byte {
	var levelByte byte
	switch level {
	case LevelBandit:
		levelByte = 0
	case LevelPPO:
		levelByte = 1
	case LevelDQN:
		levelByte = 2
	}
	buf := make([]byte, 1+len(actionData)*8)
	buf[0] = levelByte
	for i, f := range actionData {
		binary.LittleEndian.PutUint64(buf[1+i*8:], math.Float64bits(f))
	}
	return buf
}

// DecodeAction deserializes bytes back to level and action data.
func DecodeAction(data []byte) (string, []float64) {
	if len(data) < 1 {
		return "", nil
	}
	var level string
	switch data[0] {
	case 0:
		level = LevelBandit
	case 1:
		level = LevelPPO
	case 2:
		level = LevelDQN
	}
	n := (len(data) - 1) / 8
	actionData := make([]float64, n)
	for i := range actionData {
		actionData[i] = math.Float64frombits(binary.LittleEndian.Uint64(data[1+i*8:]))
	}
	return level, actionData
}
