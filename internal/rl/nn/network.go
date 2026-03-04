package nn

import (
	"math"
	"math/rand"
	"sync"
)

// Layer represents a single neural network layer.
type Layer struct {
	Weights  [][]float64 // [outputDim][inputDim]
	Biases   []float64   // [outputDim]
	InDim    int
	OutDim   int
	ActFn    ActivationFn
	// Cached for backprop
	lastInput  []float64
	lastOutput []float64
	lastPreAct []float64
	// Gradients
	WeightGrads [][]float64
	BiasGrads   []float64
}

// ActivationFn defines an activation function and its derivative.
type ActivationFn interface {
	Forward(x float64) float64
	Backward(x float64) float64 // derivative w.r.t. pre-activation
	Name() string
}

// NewLayer creates a fully connected layer with Xavier initialization.
func NewLayer(inDim, outDim int, actFn ActivationFn) *Layer {
	l := &Layer{
		Weights:     make([][]float64, outDim),
		Biases:      make([]float64, outDim),
		InDim:       inDim,
		OutDim:      outDim,
		ActFn:       actFn,
		WeightGrads: make([][]float64, outDim),
		BiasGrads:   make([]float64, outDim),
	}
	// Xavier initialization
	scale := math.Sqrt(2.0 / float64(inDim+outDim))
	for i := 0; i < outDim; i++ {
		l.Weights[i] = make([]float64, inDim)
		l.WeightGrads[i] = make([]float64, inDim)
		for j := 0; j < inDim; j++ {
			l.Weights[i][j] = rand.NormFloat64() * scale
		}
	}
	return l
}

// Forward computes the layer output: activation(W*x + b).
func (l *Layer) Forward(input []float64) []float64 {
	l.lastInput = make([]float64, len(input))
	copy(l.lastInput, input)

	l.lastPreAct = make([]float64, l.OutDim)
	l.lastOutput = make([]float64, l.OutDim)

	for i := 0; i < l.OutDim; i++ {
		sum := l.Biases[i]
		for j := 0; j < l.InDim && j < len(input); j++ {
			sum += l.Weights[i][j] * input[j]
		}
		l.lastPreAct[i] = sum
		if l.ActFn != nil {
			l.lastOutput[i] = l.ActFn.Forward(sum)
		} else {
			l.lastOutput[i] = sum
		}
	}
	return l.lastOutput
}

// Backward computes gradients and returns the gradient w.r.t. input.
func (l *Layer) Backward(gradOutput []float64) []float64 {
	gradPreAct := make([]float64, l.OutDim)
	for i := 0; i < l.OutDim; i++ {
		if l.ActFn != nil {
			gradPreAct[i] = gradOutput[i] * l.ActFn.Backward(l.lastPreAct[i])
		} else {
			gradPreAct[i] = gradOutput[i]
		}
	}

	// Compute weight and bias gradients
	for i := 0; i < l.OutDim; i++ {
		l.BiasGrads[i] += gradPreAct[i]
		for j := 0; j < l.InDim; j++ {
			l.WeightGrads[i][j] += gradPreAct[i] * l.lastInput[j]
		}
	}

	// Compute gradient w.r.t. input
	gradInput := make([]float64, l.InDim)
	for j := 0; j < l.InDim; j++ {
		for i := 0; i < l.OutDim; i++ {
			gradInput[j] += l.Weights[i][j] * gradPreAct[i]
		}
	}
	return gradInput
}

// ZeroGrads resets all gradients to zero.
func (l *Layer) ZeroGrads() {
	for i := range l.BiasGrads {
		l.BiasGrads[i] = 0
		for j := range l.WeightGrads[i] {
			l.WeightGrads[i][j] = 0
		}
	}
}

// ParamCount returns the total number of trainable parameters.
func (l *Layer) ParamCount() int {
	return l.InDim*l.OutDim + l.OutDim
}

// Network is a sequential stack of layers.
type Network struct {
	Layers []*Layer
	mu     sync.RWMutex
}

// NewNetwork creates an empty sequential network.
func NewNetwork() *Network {
	return &Network{}
}

// Add appends a layer to the network.
func (n *Network) Add(l *Layer) *Network {
	n.Layers = append(n.Layers, l)
	return n
}

// Forward runs the input through all layers sequentially.
func (n *Network) Forward(input []float64) []float64 {
	n.mu.RLock()
	defer n.mu.RUnlock()

	x := input
	for _, l := range n.Layers {
		x = l.Forward(x)
	}
	return x
}

// Backward propagates gradients through all layers in reverse.
func (n *Network) Backward(gradOutput []float64) {
	n.mu.Lock()
	defer n.mu.Unlock()

	grad := gradOutput
	for i := len(n.Layers) - 1; i >= 0; i-- {
		grad = n.Layers[i].Backward(grad)
	}
}

// ZeroGrads resets gradients for all layers.
func (n *Network) ZeroGrads() {
	for _, l := range n.Layers {
		l.ZeroGrads()
	}
}

// ParamCount returns total trainable parameters across all layers.
func (n *Network) ParamCount() int {
	total := 0
	for _, l := range n.Layers {
		total += l.ParamCount()
	}
	return total
}

// Clone creates a deep copy of the network (for target networks in DQN).
func (n *Network) Clone() *Network {
	n.mu.RLock()
	defer n.mu.RUnlock()

	clone := NewNetwork()
	for _, l := range n.Layers {
		cl := NewLayer(l.InDim, l.OutDim, l.ActFn)
		for i := range l.Weights {
			copy(cl.Weights[i], l.Weights[i])
		}
		copy(cl.Biases, l.Biases)
		clone.Add(cl)
	}
	return clone
}

// CopyFrom copies weights from another network (for target network updates).
func (n *Network) CopyFrom(src *Network) {
	n.mu.Lock()
	defer n.mu.Unlock()

	for i, l := range n.Layers {
		if i >= len(src.Layers) {
			break
		}
		sl := src.Layers[i]
		for j := range l.Weights {
			copy(l.Weights[j], sl.Weights[j])
		}
		copy(l.Biases, sl.Biases)
	}
}

// GetWeights serializes all weights to a flat float64 slice.
func (n *Network) GetWeights() []float64 {
	n.mu.RLock()
	defer n.mu.RUnlock()

	var weights []float64
	for _, l := range n.Layers {
		for _, row := range l.Weights {
			weights = append(weights, row...)
		}
		weights = append(weights, l.Biases...)
	}
	return weights
}

// SetWeights deserializes a flat float64 slice into network weights.
func (n *Network) SetWeights(weights []float64) {
	n.mu.Lock()
	defer n.mu.Unlock()

	idx := 0
	for _, l := range n.Layers {
		for i := range l.Weights {
			for j := range l.Weights[i] {
				if idx < len(weights) {
					l.Weights[i][j] = weights[idx]
					idx++
				}
			}
		}
		for i := range l.Biases {
			if idx < len(weights) {
				l.Biases[i] = weights[idx]
				idx++
			}
		}
	}
}

// --- Activation Functions ---

// ReLU activation function.
type ReLU struct{}

func (r *ReLU) Forward(x float64) float64 {
	if x > 0 {
		return x
	}
	return 0
}

func (r *ReLU) Backward(x float64) float64 {
	if x > 0 {
		return 1
	}
	return 0
}

func (r *ReLU) Name() string { return "relu" }

// Tanh activation function.
type Tanh struct{}

func (t *Tanh) Forward(x float64) float64  { return math.Tanh(x) }
func (t *Tanh) Backward(x float64) float64 { th := math.Tanh(x); return 1 - th*th }
func (t *Tanh) Name() string               { return "tanh" }

// Sigmoid activation function.
type Sigmoid struct{}

func (s *Sigmoid) Forward(x float64) float64  { return 1.0 / (1.0 + math.Exp(-x)) }
func (s *Sigmoid) Backward(x float64) float64 { sig := 1.0 / (1.0 + math.Exp(-x)); return sig * (1 - sig) }
func (s *Sigmoid) Name() string               { return "sigmoid" }

// Softmax applies softmax to a vector (used as a post-processing step, not per-element).
func Softmax(x []float64) []float64 {
	max := x[0]
	for _, v := range x[1:] {
		if v > max {
			max = v
		}
	}
	out := make([]float64, len(x))
	sum := 0.0
	for i, v := range x {
		out[i] = math.Exp(v - max)
		sum += out[i]
	}
	for i := range out {
		out[i] /= sum
	}
	return out
}
