package rl

import (
	"math/rand"
	"sync"
)

// Experience represents a single (state, action, reward, next_state, done) tuple.
type Experience struct {
	State     *RLState
	Action    []float64
	Reward    float64
	NextState *RLState
	Done      bool
	Level     string // "bandit", "ppo", "dqn"
}

// ExperienceBuffer is a circular replay buffer for RL training.
type ExperienceBuffer struct {
	buffer   []Experience
	capacity int
	size     int
	idx      int
	mu       sync.RWMutex
}

// NewExperienceBuffer creates a new experience replay buffer.
func NewExperienceBuffer(capacity int) *ExperienceBuffer {
	return &ExperienceBuffer{
		buffer:   make([]Experience, capacity),
		capacity: capacity,
	}
}

// Add inserts a new experience into the buffer.
func (b *ExperienceBuffer) Add(exp Experience) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buffer[b.idx] = exp
	b.idx = (b.idx + 1) % b.capacity
	if b.size < b.capacity {
		b.size++
	}
}

// Sample randomly samples a batch of experiences.
func (b *ExperienceBuffer) Sample(batchSize int) []Experience {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.size == 0 {
		return nil
	}
	if batchSize > b.size {
		batchSize = b.size
	}

	batch := make([]Experience, batchSize)
	indices := rand.Perm(b.size)[:batchSize]
	for i, idx := range indices {
		batch[i] = b.buffer[idx]
	}
	return batch
}

// SampleByLevel samples experiences for a specific RL level.
func (b *ExperienceBuffer) SampleByLevel(level string, batchSize int) []Experience {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var filtered []Experience
	for i := 0; i < b.size; i++ {
		if b.buffer[i].Level == level {
			filtered = append(filtered, b.buffer[i])
		}
	}

	if len(filtered) == 0 {
		return nil
	}
	if batchSize > len(filtered) {
		batchSize = len(filtered)
	}

	batch := make([]Experience, batchSize)
	indices := rand.Perm(len(filtered))[:batchSize]
	for i, idx := range indices {
		batch[i] = filtered[idx]
	}
	return batch
}

// Size returns the current number of experiences in the buffer.
func (b *ExperienceBuffer) Size() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.size
}

// Clear empties the buffer.
func (b *ExperienceBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.size = 0
	b.idx = 0
}

// GetAll returns all experiences in the buffer (for PPO on-policy updates).
func (b *ExperienceBuffer) GetAll() []Experience {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.size == 0 {
		return nil
	}
	batch := make([]Experience, b.size)
	copy(batch, b.buffer[:b.size])
	return batch
}

// PrioritizedExperienceBuffer implements prioritized experience replay.
type PrioritizedExperienceBuffer struct {
	buffer     []Experience
	priorities []float64
	capacity   int
	size       int
	idx        int
	alpha      float64 // priority exponent
	mu         sync.RWMutex
}

// NewPrioritizedExperienceBuffer creates a prioritized replay buffer.
func NewPrioritizedExperienceBuffer(capacity int, alpha float64) *PrioritizedExperienceBuffer {
	return &PrioritizedExperienceBuffer{
		buffer:     make([]Experience, capacity),
		priorities: make([]float64, capacity),
		capacity:   capacity,
		alpha:      alpha,
	}
}

// Add inserts a new experience with maximum priority.
func (b *PrioritizedExperienceBuffer) Add(exp Experience) {
	b.mu.Lock()
	defer b.mu.Unlock()

	maxPriority := 1.0
	if b.size > 0 {
		for i := 0; i < b.size; i++ {
			if b.priorities[i] > maxPriority {
				maxPriority = b.priorities[i]
			}
		}
	}

	b.buffer[b.idx] = exp
	b.priorities[b.idx] = maxPriority
	b.idx = (b.idx + 1) % b.capacity
	if b.size < b.capacity {
		b.size++
	}
}

// Sample samples experiences based on priority.
func (b *PrioritizedExperienceBuffer) Sample(batchSize int) []Experience {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.size == 0 {
		return nil
	}
	if batchSize > b.size {
		batchSize = b.size
	}

	// Compute sampling probabilities
	probs := make([]float64, b.size)
	sum := 0.0
	for i := 0; i < b.size; i++ {
		probs[i] = pow(b.priorities[i], b.alpha)
		sum += probs[i]
	}
	for i := range probs {
		probs[i] /= sum
	}

	// Sample indices
	batch := make([]Experience, batchSize)
	for i := 0; i < batchSize; i++ {
		idx := sampleCategorical(probs)
		batch[i] = b.buffer[idx]
	}
	return batch
}

// UpdatePriorities updates priorities for sampled experiences.
func (b *PrioritizedExperienceBuffer) UpdatePriorities(indices []int, priorities []float64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for i, idx := range indices {
		if idx < b.size {
			b.priorities[idx] = priorities[i]
		}
	}
}

func pow(x, y float64) float64 {
	if x <= 0 {
		return 0
	}
	result := 1.0
	for i := 0; i < int(y); i++ {
		result *= x
	}
	return result
}

func sampleCategorical(probs []float64) int {
	r := rand.Float64()
	cumsum := 0.0
	for i, p := range probs {
		cumsum += p
		if r < cumsum {
			return i
		}
	}
	return len(probs) - 1
}
