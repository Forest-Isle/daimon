package nn

import (
	"math"
)

// Optimizer updates network parameters using computed gradients.
type Optimizer interface {
	Step(net *Network)
	SetLR(lr float64)
}

// Adam implements the Adam optimizer.
type Adam struct {
	LR      float64
	Beta1   float64
	Beta2   float64
	Epsilon float64
	t       int
	m       [][][]float64 // first moment (weights)
	v       [][][]float64 // second moment (weights)
	mBias   [][]float64   // first moment (biases)
	vBias   [][]float64   // second moment (biases)
	inited  bool
}

// NewAdam creates an Adam optimizer with default hyperparameters.
func NewAdam(lr float64) *Adam {
	return &Adam{
		LR:      lr,
		Beta1:   0.9,
		Beta2:   0.999,
		Epsilon: 1e-8,
	}
}

func (a *Adam) SetLR(lr float64) { a.LR = lr }

// Step performs one optimization step.
func (a *Adam) Step(net *Network) {
	if !a.inited {
		a.init(net)
	}
	a.t++

	for li, l := range net.Layers {
		// Update weights
		for i := range l.Weights {
			for j := range l.Weights[i] {
				g := l.WeightGrads[i][j]
				a.m[li][i][j] = a.Beta1*a.m[li][i][j] + (1-a.Beta1)*g
				a.v[li][i][j] = a.Beta2*a.v[li][i][j] + (1-a.Beta2)*g*g

				mHat := a.m[li][i][j] / (1 - math.Pow(a.Beta1, float64(a.t)))
				vHat := a.v[li][i][j] / (1 - math.Pow(a.Beta2, float64(a.t)))

				l.Weights[i][j] -= a.LR * mHat / (math.Sqrt(vHat) + a.Epsilon)
			}
		}
		// Update biases
		for i := range l.Biases {
			g := l.BiasGrads[i]
			a.mBias[li][i] = a.Beta1*a.mBias[li][i] + (1-a.Beta1)*g
			a.vBias[li][i] = a.Beta2*a.vBias[li][i] + (1-a.Beta2)*g*g

			mHat := a.mBias[li][i] / (1 - math.Pow(a.Beta1, float64(a.t)))
			vHat := a.vBias[li][i] / (1 - math.Pow(a.Beta2, float64(a.t)))

			l.Biases[i] -= a.LR * mHat / (math.Sqrt(vHat) + a.Epsilon)
		}
	}
}

func (a *Adam) init(net *Network) {
	a.m = make([][][]float64, len(net.Layers))
	a.v = make([][][]float64, len(net.Layers))
	a.mBias = make([][]float64, len(net.Layers))
	a.vBias = make([][]float64, len(net.Layers))

	for li, l := range net.Layers {
		a.m[li] = make([][]float64, l.OutDim)
		a.v[li] = make([][]float64, l.OutDim)
		for i := 0; i < l.OutDim; i++ {
			a.m[li][i] = make([]float64, l.InDim)
			a.v[li][i] = make([]float64, l.InDim)
		}
		a.mBias[li] = make([]float64, l.OutDim)
		a.vBias[li] = make([]float64, l.OutDim)
	}
	a.inited = true
}

// SGD implements stochastic gradient descent with optional momentum.
type SGD struct {
	LR       float64
	Momentum float64
	vel      [][][]float64
	velBias  [][]float64
	inited   bool
}

// NewSGD creates an SGD optimizer.
func NewSGD(lr, momentum float64) *SGD {
	return &SGD{LR: lr, Momentum: momentum}
}

func (s *SGD) SetLR(lr float64) { s.LR = lr }

// Step performs one SGD optimization step.
func (s *SGD) Step(net *Network) {
	if !s.inited {
		s.init(net)
	}

	for li, l := range net.Layers {
		for i := range l.Weights {
			for j := range l.Weights[i] {
				s.vel[li][i][j] = s.Momentum*s.vel[li][i][j] - s.LR*l.WeightGrads[i][j]
				l.Weights[i][j] += s.vel[li][i][j]
			}
		}
		for i := range l.Biases {
			s.velBias[li][i] = s.Momentum*s.velBias[li][i] - s.LR*l.BiasGrads[i]
			l.Biases[i] += s.velBias[li][i]
		}
	}
}

func (s *SGD) init(net *Network) {
	s.vel = make([][][]float64, len(net.Layers))
	s.velBias = make([][]float64, len(net.Layers))
	for li, l := range net.Layers {
		s.vel[li] = make([][]float64, l.OutDim)
		for i := 0; i < l.OutDim; i++ {
			s.vel[li][i] = make([]float64, l.InDim)
		}
		s.velBias[li] = make([]float64, l.OutDim)
	}
	s.inited = true
}
