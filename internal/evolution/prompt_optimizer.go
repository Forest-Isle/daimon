package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"time"
)

// PromptRole identifies which system prompt is being optimized.
type PromptRole string

const (
	RolePlan     PromptRole = "plan"
	RoleReflect  PromptRole = "reflect"
	RoleAct      PromptRole = "act"
)

// PromptCandidate is a single prompt template variant under evaluation.
type PromptCandidate struct {
	ID       string        `json:"id"`
	Role     PromptRole    `json:"role"`
	Template string        `json:"template"`
	Version  int           `json:"version"`
	Metrics  PromptMetrics `json:"metrics"`
	Active   bool          `json:"active"`
	CreatedAt time.Time    `json:"created_at"`
}

// PromptMetrics tracks A/B test performance for a prompt variant.
type PromptMetrics struct {
	Impressions    int     `json:"impressions"`
	Successes      int     `json:"successes"`
	AvgConfidence  float64 `json:"avg_confidence"`
	AvgUserRating  float64 `json:"avg_user_rating"`
	AvgLatencyMs   int64   `json:"avg_latency_ms"`
	LastUsed       time.Time `json:"last_used"`
}

// PromptOptimizer manages prompt template evolution using Thompson Sampling.
type PromptOptimizer struct {
	candidates map[PromptRole][]*PromptCandidate
	mu         sync.RWMutex
	compiler   *PromptCompiler
	minImpressions int  // minimum impressions before a candidate can be pruned
	maxCandidates  int  // max candidates per role
}

// NewPromptOptimizer creates a new prompt optimizer.
func NewPromptOptimizer() *PromptOptimizer {
	return &PromptOptimizer{
		candidates:     make(map[PromptRole][]*PromptCandidate),
		compiler:       &PromptCompiler{},
		minImpressions: 10,
		maxCandidates:  5,
	}
}

// RegisterBaseline adds the initial prompt template as the baseline candidate.
func (po *PromptOptimizer) RegisterBaseline(role PromptRole, template string) {
	po.mu.Lock()
	defer po.mu.Unlock()

	if _, exists := po.candidates[role]; exists {
		return // already registered
	}

	po.candidates[role] = []*PromptCandidate{{
		ID:       fmt.Sprintf("%s_baseline", role),
		Role:     role,
		Template: template,
		Version:  1,
		Active:   true,
		Metrics:  PromptMetrics{},
		CreatedAt: time.Now(),
	}}
}

// RecordOutcome updates metrics for the prompt variant used in a session.
func (po *PromptOptimizer) RecordOutcome(role PromptRole, candidateID string, success bool, confidence float64, userRating float64, latencyMs int64) {
	po.mu.Lock()
	defer po.mu.Unlock()

	candidates := po.candidates[role]
	for _, c := range candidates {
		if c.ID == candidateID {
			c.Metrics.Impressions++
			if success {
				c.Metrics.Successes++
			}
			n := float64(c.Metrics.Impressions)
			c.Metrics.AvgConfidence = (c.Metrics.AvgConfidence*(n-1) + confidence) / n
			c.Metrics.AvgUserRating = (c.Metrics.AvgUserRating*(n-1) + userRating) / n
			c.Metrics.AvgLatencyMs = int64((float64(c.Metrics.AvgLatencyMs)*(n-1) + float64(latencyMs)) / n)
			c.Metrics.LastUsed = time.Now()
			return
		}
	}
}

// SelectBest uses Thompson Sampling to select the best prompt variant.
// This automatically balances exploration vs exploitation.
func (po *PromptOptimizer) SelectBest(role PromptRole) *PromptCandidate {
	po.mu.RLock()
	defer po.mu.RUnlock()

	candidates := po.candidates[role]
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	var best *PromptCandidate
	var bestSample float64

	for _, c := range candidates {
		if !c.Active {
			continue
		}
		// Beta(α=successes+1, β=impressions-successes+1)
		alpha := float64(c.Metrics.Successes + 1)
		beta := float64(c.Metrics.Impressions - c.Metrics.Successes + 1)
		sample := sampleBeta(alpha, beta)
		if sample > bestSample {
			bestSample = sample
			best = c
		}
	}

	return best
}

// GetCandidate returns a specific candidate by role and ID.
func (po *PromptOptimizer) GetCandidate(role PromptRole, id string) *PromptCandidate {
	po.mu.RLock()
	defer po.mu.RUnlock()
	for _, c := range po.candidates[role] {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// MutateAndAdd generates a new variant from the worst-performing candidate.
func (po *PromptOptimizer) MutateAndAdd(ctx context.Context, role PromptRole, completer func(ctx context.Context, system, user string) (string, error)) error {
	po.mu.Lock()
	candidates := po.candidates[role]
	if len(candidates) == 0 {
		po.mu.Unlock()
		return fmt.Errorf("no candidates for role %s", role)
	}

	worst := po.findWorstLocked(candidates)
	if worst == nil || worst.Metrics.Impressions < po.minImpressions {
		po.mu.Unlock()
		return nil // not enough data to mutate
	}
	po.mu.Unlock()

	// Generate improved variant via LLM
	newTemplate, err := po.compiler.Mutate(ctx, completer, worst)
	if err != nil {
		return fmt.Errorf("mutate prompt: %w", err)
	}

	po.mu.Lock()
	defer po.mu.Unlock()

	newCandidate := &PromptCandidate{
		ID:       fmt.Sprintf("%s_v%d_%d", role, worst.Version+1, time.Now().UnixNano()),
		Role:     role,
		Template: newTemplate,
		Version:  worst.Version + 1,
		Active:   true,
		Metrics:  PromptMetrics{},
		CreatedAt: time.Now(),
	}

	po.candidates[role] = append(po.candidates[role], newCandidate)

	// Prune if over max
	if len(po.candidates[role]) > po.maxCandidates {
		po.pruneLocked(role)
	}

	slog.Info("prompt-optimizer: new variant created",
		"role", role,
		"version", newCandidate.Version,
		"candidates", len(po.candidates[role]),
	)

	return nil
}

func (po *PromptOptimizer) findWorstLocked(candidates []*PromptCandidate) *PromptCandidate {
	var worst *PromptCandidate
	var worstRate float64 = 1.0
	for _, c := range candidates {
		if c.Metrics.Impressions == 0 {
			continue
		}
		rate := float64(c.Metrics.Successes) / float64(c.Metrics.Impressions)
		if rate < worstRate {
			worstRate = rate
			worst = c
		}
	}
	return worst
}

func (po *PromptOptimizer) pruneLocked(role PromptRole) {
	candidates := po.candidates[role]
	// Sort by success rate, keep top maxCandidates
	sortBySuccessRate(candidates)
	// Deactivate the worst ones
	for i := po.maxCandidates; i < len(candidates); i++ {
		candidates[i].Active = false
	}
	// Keep only active + those with insufficient data to judge
	filtered := make([]*PromptCandidate, 0, po.maxCandidates)
	for _, c := range candidates {
		if c.Active || c.Metrics.Impressions < po.minImpressions {
			filtered = append(filtered, c)
		}
	}
	po.candidates[role] = filtered
}

func sortBySuccessRate(candidates []*PromptCandidate) {
	// Simple bubble sort by success rate descending
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			ri := safeRate(candidates[i])
			rj := safeRate(candidates[j])
			if rj > ri {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
}

func safeRate(c *PromptCandidate) float64 {
	if c.Metrics.Impressions == 0 {
		return 0
	}
	return float64(c.Metrics.Successes) / float64(c.Metrics.Impressions)
}

// PromptCompiler generates improved prompt variants via LLM.
type PromptCompiler struct{}

func (pc *PromptCompiler) Mutate(ctx context.Context, completer func(ctx context.Context, system, user string) (string, error), worst *PromptCandidate) (string, error) {
	system := `You are a prompt engineer. Given a prompt template and its performance metrics,
generate an improved version.

Diagnose failures:
- Low success rate → unclear instructions, wrong examples, missing constraints
- Low confidence → ambiguous success criteria
- High latency → too verbose, unnecessary steps

Improvement strategies:
- Add concrete examples
- Clarify edge cases
- Add explicit constraints
- Reduce verbosity
- Improve output format specification

Output ONLY the improved template text, no explanation.`

	rate := 0.0
	if worst.Metrics.Impressions > 0 {
		rate = float64(worst.Metrics.Successes) / float64(worst.Metrics.Impressions)
	}

	user := fmt.Sprintf(`Current template:
---
%s
---
Performance:
- Success rate: %.1f%%
- Avg confidence: %.2f
- Avg user rating: %.2f
- Avg latency: %dms

Generate an improved version of this template.`,
		worst.Template, rate*100, worst.Metrics.AvgConfidence,
		worst.Metrics.AvgUserRating, worst.Metrics.AvgLatencyMs)

	return completer(ctx, system, user)
}

// sampleBeta generates a random sample from Beta(α, β) using the
// gamma distribution method (Marsaglia & Tsang).
func sampleBeta(alpha, beta float64) float64 {
	if alpha <= 0 || beta <= 0 {
		return 0.5
	}
	// Use simple approximation for Thompson sampling
	x := rand.Float64()
	y := rand.Float64()
	for math.Pow(x, 1/alpha)+math.Pow(y, 1/beta) > 1 {
		x = rand.Float64()
		y = rand.Float64()
	}
	return math.Pow(x, 1/alpha) / (math.Pow(x, 1/alpha) + math.Pow(y, 1/beta))
}
