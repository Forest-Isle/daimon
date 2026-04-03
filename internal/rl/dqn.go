package rl

import (
	"math"
	"math/rand"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/rl/nn"
)

// DQN implements Deep Q-Network for replan decisions.
type DQN struct {
	qNet       *nn.Network
	targetNet  *nn.Network
	optimizer  nn.Optimizer
	cfg        config.DQNConfig
	epsilon    float64
	updateStep int
}

// NewDQN creates a new DQN agent.
func NewDQN(cfg config.DQNConfig) *DQN {
	// Q-network: state (21) -> hidden (64) -> hidden (32) -> Q-values (3)
	qNet := nn.NewNetwork().
		Add(nn.NewLayer(StateDim, 64, &nn.ReLU{})).
		Add(nn.NewLayer(64, 32, &nn.ReLU{})).
		Add(nn.NewLayer(32, NumReplanActions, nil)) // Linear output for Q-values

	targetNet := qNet.Clone()

	lr := cfg.LearningRate
	if lr <= 0 {
		lr = 0.001
	}

	epsilon := cfg.EpsilonStart
	if epsilon <= 0 {
		epsilon = 0.9
	}

	return &DQN{
		qNet:      qNet,
		targetNet: targetNet,
		optimizer: nn.NewAdam(lr),
		cfg:       cfg,
		epsilon:   epsilon,
	}
}

// SelectAction selects a replan action using epsilon-greedy.
func (d *DQN) SelectAction(state *RLState) ReplanActionType {
	// Epsilon-greedy exploration
	if rand.Float64() < d.epsilon {
		return ReplanActionType(rand.Intn(NumReplanActions))
	}

	// Greedy action
	stateVec := state.ToVector()
	qValues := d.qNet.Forward(stateVec)

	bestAction := 0
	bestQ := qValues[0]
	for i := 1; i < len(qValues) && i < NumReplanActions; i++ {
		if qValues[i] > bestQ {
			bestQ = qValues[i]
			bestAction = i
		}
	}

	return ReplanActionType(bestAction)
}

// ComputeQValues computes Q-values for all actions given a state.
func (d *DQN) ComputeQValues(state *RLState) []float64 {
	stateVec := state.ToVector()
	return d.qNet.Forward(stateVec)
}

// Update performs a DQN update using a batch of experiences.
func (d *DQN) Update(experiences []Experience) float64 {
	if len(experiences) == 0 {
		return 0
	}

	gamma := d.cfg.Gamma
	if gamma <= 0 {
		gamma = 0.99
	}

	totalLoss := 0.0

	for _, exp := range experiences {
		// Compute target Q-value
		targetQ := exp.Reward
		if !exp.Done && exp.NextState != nil {
			nextQValues := d.targetNet.Forward(exp.NextState.ToVector())
			maxNextQ := nextQValues[0]
			for _, q := range nextQValues[1:] {
				if q > maxNextQ {
					maxNextQ = q
				}
			}
			targetQ += gamma * maxNextQ
		}

		// Compute current Q-values
		currentQValues := d.qNet.Forward(exp.State.ToVector())

		// Extract action index from exp.Action
		actionIdx := 0
		if len(exp.Action) > 0 {
			actionIdx = int(exp.Action[0])
			if actionIdx < 0 || actionIdx >= NumReplanActions {
				actionIdx = 0
			}
		}

		// Compute loss only for the taken action
		target := make([]float64, len(currentQValues))
		copy(target, currentQValues)
		target[actionIdx] = targetQ

		loss, grad := nn.MSELoss(currentQValues, target)
		totalLoss += loss

		// Backprop
		d.qNet.ZeroGrads()
		d.qNet.Backward(grad)
		d.optimizer.Step(d.qNet)
	}

	// Update target network periodically
	d.updateStep++
	targetUpdateFreq := d.cfg.TargetUpdateFreq
	if targetUpdateFreq <= 0 {
		targetUpdateFreq = 500
	}
	if d.updateStep%targetUpdateFreq == 0 {
		d.targetNet.CopyFrom(d.qNet)
	}

	// Decay epsilon
	decay := d.cfg.EpsilonDecay
	if decay <= 0 {
		decay = 0.995
	}
	epsilonEnd := d.cfg.EpsilonEnd
	if epsilonEnd <= 0 {
		epsilonEnd = 0.05
	}
	d.epsilon = math.Max(epsilonEnd, d.epsilon*decay)

	return totalLoss / float64(len(experiences))
}

// GetWeights serializes Q-network weights.
func (d *DQN) GetWeights() []byte {
	weights := d.qNet.GetWeights()
	buf := make([]byte, len(weights)*8)
	for i, w := range weights {
		writeFloat64(buf, i*8, w)
	}
	return buf
}

// SetWeights deserializes and loads Q-network weights.
func (d *DQN) SetWeights(data []byte) {
	n := len(data) / 8
	weights := make([]float64, n)
	for i := 0; i < n; i++ {
		weights[i] = readFloat64(data, i*8)
	}
	d.qNet.SetWeights(weights)
	d.targetNet.CopyFrom(d.qNet)
}

// GetEpsilon returns the current exploration rate.
func (d *DQN) GetEpsilon() float64 {
	return d.epsilon
}

// Helper functions for binary serialization
func writeFloat64(buf []byte, offset int, val float64) {
	bits := math.Float64bits(val)
	buf[offset] = byte(bits)
	buf[offset+1] = byte(bits >> 8)
	buf[offset+2] = byte(bits >> 16)
	buf[offset+3] = byte(bits >> 24)
	buf[offset+4] = byte(bits >> 32)
	buf[offset+5] = byte(bits >> 40)
	buf[offset+6] = byte(bits >> 48)
	buf[offset+7] = byte(bits >> 56)
}

func readFloat64(buf []byte, offset int) float64 {
	bits := uint64(buf[offset]) |
		uint64(buf[offset+1])<<8 |
		uint64(buf[offset+2])<<16 |
		uint64(buf[offset+3])<<24 |
		uint64(buf[offset+4])<<32 |
		uint64(buf[offset+5])<<40 |
		uint64(buf[offset+6])<<48 |
		uint64(buf[offset+7])<<56
	return math.Float64frombits(bits)
}
