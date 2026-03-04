package rl

import (
	"math"

	"github.com/punkopunko/ironclaw/internal/config"
	"github.com/punkopunko/ironclaw/internal/rl/nn"
)

// PPO implements Proximal Policy Optimization for plan strategy.
type PPO struct {
	policyNet *nn.Network
	valueNet  *nn.Network
	optimizer nn.Optimizer
	cfg       config.PPOConfig
}

// NewPPO creates a new PPO agent.
func NewPPO(cfg config.PPOConfig) *PPO {
	// Policy network: state (21) -> hidden (64) -> hidden (32) -> action (3)
	policyNet := nn.NewNetwork().
		Add(nn.NewLayer(StateDim, 64, &nn.ReLU{})).
		Add(nn.NewLayer(64, 32, &nn.ReLU{})).
		Add(nn.NewLayer(32, 3, &nn.Tanh{})) // Output: [-1, 1] for each action dim

	// Value network: state (21) -> hidden (64) -> hidden (32) -> value (1)
	valueNet := nn.NewNetwork().
		Add(nn.NewLayer(StateDim, 64, &nn.ReLU{})).
		Add(nn.NewLayer(64, 32, &nn.ReLU{})).
		Add(nn.NewLayer(32, 1, nil)) // Linear output for value

	lr := cfg.LearningRate
	if lr <= 0 {
		lr = 0.0003
	}

	return &PPO{
		policyNet: policyNet,
		valueNet:  valueNet,
		optimizer: nn.NewAdam(lr),
		cfg:       cfg,
	}
}

// SelectAction samples an action from the policy network.
func (p *PPO) SelectAction(state *RLState) *PlanStrategyAction {
	stateVec := state.ToVector()
	actionVec := p.policyNet.Forward(stateVec)

	// Add exploration noise
	for i := range actionVec {
		actionVec[i] += randNormal(0, 0.1)
	}

	return PlanStrategyFromVector(actionVec)
}

// ComputeValue computes the state value using the value network.
func (p *PPO) ComputeValue(state *RLState) float64 {
	stateVec := state.ToVector()
	valueVec := p.valueNet.Forward(stateVec)
	if len(valueVec) > 0 {
		return valueVec[0]
	}
	return 0
}

// Update performs a PPO update using a batch of experiences.
func (p *PPO) Update(experiences []Experience) float64 {
	if len(experiences) == 0 {
		return 0
	}

	// Compute advantages using GAE
	advantages := p.computeGAE(experiences)
	returns := make([]float64, len(experiences))
	for i := range experiences {
		returns[i] = advantages[i] + p.ComputeValue(experiences[i].State)
	}

	// Normalize advantages
	advantages = normalizeSlice(advantages)

	// Store old action probabilities
	oldLogProbs := make([]float64, len(experiences))
	for i, exp := range experiences {
		oldLogProbs[i] = p.computeLogProb(exp.State, exp.Action)
	}

	totalLoss := 0.0
	epochs := p.cfg.Epochs
	if epochs <= 0 {
		epochs = 4
	}

	// PPO epochs
	for epoch := 0; epoch < epochs; epoch++ {
		epochLoss := 0.0

		for i, exp := range experiences {
			// Policy loss
			newLogProb := p.computeLogProb(exp.State, exp.Action)
			ratio := math.Exp(newLogProb - oldLogProbs[i])

			clipEpsilon := p.cfg.ClipEpsilon
			if clipEpsilon <= 0 {
				clipEpsilon = 0.2
			}

			policyLoss, policyGrad := nn.ClippedSurrogateLoss(
				[]float64{ratio},
				[]float64{advantages[i]},
				clipEpsilon,
			)

			// Value loss
			predicted := p.ComputeValue(exp.State)
			valueLoss, valueGrad := nn.ValueLoss([]float64{predicted}, []float64{returns[i]})

			// Combined loss
			loss := policyLoss + 0.5*valueLoss
			epochLoss += loss

			// Backprop policy network
			p.policyNet.ZeroGrads()
			p.policyNet.Backward(policyGrad)
			p.optimizer.Step(p.policyNet)

			// Backprop value network
			p.valueNet.ZeroGrads()
			p.valueNet.Backward(valueGrad)
			p.optimizer.Step(p.valueNet)
		}

		totalLoss += epochLoss / float64(len(experiences))
	}

	return totalLoss / float64(epochs)
}

// computeGAE computes Generalized Advantage Estimation.
func (p *PPO) computeGAE(experiences []Experience) []float64 {
	n := len(experiences)
	advantages := make([]float64, n)

	gamma := p.cfg.Gamma
	if gamma <= 0 {
		gamma = 0.99
	}
	lambda := p.cfg.GAELambda
	if lambda <= 0 {
		lambda = 0.95
	}

	gae := 0.0
	for i := n - 1; i >= 0; i-- {
		exp := experiences[i]
		value := p.ComputeValue(exp.State)
		nextValue := 0.0
		if !exp.Done && exp.NextState != nil {
			nextValue = p.ComputeValue(exp.NextState)
		}

		delta := exp.Reward + gamma*nextValue - value
		gae = delta + gamma*lambda*gae
		advantages[i] = gae
	}

	return advantages
}

// computeLogProb computes the log probability of an action under the current policy.
func (p *PPO) computeLogProb(state *RLState, action []float64) float64 {
	stateVec := state.ToVector()
	mean := p.policyNet.Forward(stateVec)

	// Assume Gaussian policy with fixed std=0.1
	std := 0.1
	logProb := 0.0
	for i := 0; i < len(action) && i < len(mean); i++ {
		diff := action[i] - mean[i]
		logProb -= 0.5 * (diff * diff) / (std * std)
		logProb -= math.Log(std * math.Sqrt(2*math.Pi))
	}
	return logProb
}

// GetWeights serializes policy and value network weights.
func (p *PPO) GetWeights() []byte {
	policyWeights := p.policyNet.GetWeights()
	valueWeights := p.valueNet.GetWeights()

	// Simple concatenation with length prefix
	totalLen := len(policyWeights) + len(valueWeights) + 8
	buf := make([]byte, totalLen*8)

	// Store lengths
	writeFloat64(buf, 0, float64(len(policyWeights)))
	writeFloat64(buf, 8, float64(len(valueWeights)))

	// Store weights
	offset := 16
	for _, w := range policyWeights {
		writeFloat64(buf, offset, w)
		offset += 8
	}
	for _, w := range valueWeights {
		writeFloat64(buf, offset, w)
		offset += 8
	}

	return buf
}

// SetWeights deserializes and loads policy and value network weights.
func (p *PPO) SetWeights(data []byte) {
	if len(data) < 16 {
		return
	}

	policyLen := int(readFloat64(data, 0))
	valueLen := int(readFloat64(data, 8))

	offset := 16
	policyWeights := make([]float64, policyLen)
	for i := range policyWeights {
		policyWeights[i] = readFloat64(data, offset)
		offset += 8
	}

	valueWeights := make([]float64, valueLen)
	for i := range valueWeights {
		valueWeights[i] = readFloat64(data, offset)
		offset += 8
	}

	p.policyNet.SetWeights(policyWeights)
	p.valueNet.SetWeights(valueWeights)
}

func normalizeSlice(s []float64) []float64 {
	if len(s) == 0 {
		return s
	}

	mean := 0.0
	for _, v := range s {
		mean += v
	}
	mean /= float64(len(s))

	variance := 0.0
	for _, v := range s {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(len(s))
	std := math.Sqrt(variance)

	if std < 1e-8 {
		return s
	}

	normalized := make([]float64, len(s))
	for i, v := range s {
		normalized[i] = (v - mean) / std
	}
	return normalized
}

func randNormal(mean, std float64) float64 {
	return mean + std*randStdNormal()
}

func randStdNormal() float64 {
	// Box-Muller transform
	u1 := math.Max(1e-10, math.Min(1-1e-10, math.Float64frombits(uint64(randInt63()))))
	u2 := math.Max(1e-10, math.Min(1-1e-10, math.Float64frombits(uint64(randInt63()))))
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

func randInt63() int64 {
	return int64(uint64(randUint32())<<31 | uint64(randUint32()))
}

func randUint32() uint32 {
	return uint32(math.Float64bits(math.Abs(randStdNormal())) & 0xFFFFFFFF)
}
