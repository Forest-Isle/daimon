package collective

import (
	"math"
	"sort"
	"sync"
	"time"
)

// ReputationRecord tracks a single task outcome for reputation scoring.
type ReputationRecord struct {
	AgentID      string    `json:"agent_id"`
	TaskID       string    `json:"task_id"`
	TaskCategory string    `json:"task_category"`
	Success      bool      `json:"success"`
	QualityScore float64   `json:"quality_score"`
	CompletedAt  time.Time `json:"completed_at"`
}

// ReputationSystem tracks agent reputation with time decay and category specialization.
type ReputationSystem struct {
	records   map[string][]*ReputationRecord // agentID → records
	category  map[string]map[string][]*ReputationRecord // agentID → category → records
	decayRate float64
	mu        sync.RWMutex
}

// NewReputationSystem creates a reputation system with the given decay rate.
// decayRate controls how quickly old records lose weight (in days^-1).
// Default: 0.05 (records from 20 days ago have ~37% weight).
func NewReputationSystem(decayRate float64) *ReputationSystem {
	if decayRate <= 0 {
		decayRate = 0.05
	}
	return &ReputationSystem{
		records:   make(map[string][]*ReputationRecord),
		category:  make(map[string]map[string][]*ReputationRecord),
		decayRate: decayRate,
	}
}

// RecordOutcome stores a task outcome for reputation tracking.
func (rs *ReputationSystem) RecordOutcome(agentID, taskID, category string, success bool, qualityScore float64) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	rec := &ReputationRecord{
		AgentID:      agentID,
		TaskID:       taskID,
		TaskCategory: category,
		Success:      success,
		QualityScore: qualityScore,
		CompletedAt:  time.Now(),
	}

	rs.records[agentID] = append(rs.records[agentID], rec)

	if rs.category[agentID] == nil {
		rs.category[agentID] = make(map[string][]*ReputationRecord)
	}
	rs.category[agentID][category] = append(rs.category[agentID][category], rec)

	// Prune records older than 60 days
	rs.pruneLocked(agentID, 60*24*time.Hour)
}

// GetReputation computes the current reputation score for an agent.
// Score is weighted by recency: recent outcomes count more.
func (rs *ReputationSystem) GetReputation(agentID string) float64 {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	records := rs.records[agentID]
	if len(records) == 0 {
		return 0.5 // neutral starting reputation
	}

	var weightedSum, totalWeight float64
	now := time.Now()

	for _, rec := range records {
		age := now.Sub(rec.CompletedAt).Hours() / 24 // days
		weight := math.Exp(-rs.decayRate * age)

		score := 0.0
		if rec.Success {
			score = rec.QualityScore
			if score == 0 {
				score = 0.7
			}
		}

		weightedSum += score * weight
		totalWeight += weight
	}

	if totalWeight == 0 {
		return 0.5
	}
	return weightedSum / totalWeight
}

// GetCategoryReputation computes reputation for a specific task category.
func (rs *ReputationSystem) GetCategoryReputation(agentID, category string) float64 {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	catMap := rs.category[agentID]
	if catMap == nil {
		return 0.5
	}

	records := catMap[category]
	if len(records) == 0 {
		return 0.5
	}

	var weightedSum, totalWeight float64
	now := time.Now()

	for _, rec := range records {
		age := now.Sub(rec.CompletedAt).Hours() / 24
		weight := math.Exp(-rs.decayRate * age)

		score := 0.0
		if rec.Success {
			score = rec.QualityScore
			if score == 0 {
				score = 0.7
			}
		}
		weightedSum += score * weight
		totalWeight += weight
	}

	if totalWeight == 0 {
		return 0.5
	}
	return weightedSum / totalWeight
}

// GetTopCategories returns the agent's best-performing categories.
func (rs *ReputationSystem) GetTopCategories(agentID string, topN int) []string {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	catMap := rs.category[agentID]
	if catMap == nil {
		return nil
	}

	type catScore struct {
		name  string
		score float64
	}
	var scores []catScore

	for cat, records := range catMap {
		if len(records) < 3 {
			continue
		}
		var sum float64
		for _, rec := range records {
			if rec.Success {
				q := rec.QualityScore
				if q == 0 {
					q = 0.7
				}
				sum += q
			}
		}
		scores = append(scores, catScore{cat, sum / float64(len(records))})
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	result := make([]string, 0, topN)
	for i := 0; i < topN && i < len(scores); i++ {
		result = append(result, scores[i].name)
	}
	return result
}

// GetAgentStats returns summary statistics for an agent.
func (rs *ReputationSystem) GetAgentStats(agentID string) *AgentStats {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	records := rs.records[agentID]
	if len(records) == 0 {
		return &AgentStats{AgentID: agentID}
	}

	var successes, failures int
	var totalQuality float64

	for _, rec := range records {
		if rec.Success {
			successes++
			totalQuality += rec.QualityScore
		} else {
			failures++
		}
	}

	total := float64(successes + failures)
	return &AgentStats{
		AgentID:        agentID,
		TotalTasks:     successes + failures,
		SuccessRate:    float64(successes) / total,
		AvgQuality:     totalQuality / float64(successes),
		Reputation:     rs.GetReputation(agentID),
		TopCategories:  rs.GetTopCategories(agentID, 3),
	}
}

// AgentStats summarizes an agent's performance.
type AgentStats struct {
	AgentID       string   `json:"agent_id"`
	TotalTasks    int      `json:"total_tasks"`
	SuccessRate   float64  `json:"success_rate"`
	AvgQuality    float64  `json:"avg_quality"`
	Reputation    float64  `json:"reputation"`
	TopCategories []string `json:"top_categories"`
}

func (rs *ReputationSystem) pruneLocked(agentID string, maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	records := rs.records[agentID]
	filtered := records[:0]
	for _, rec := range records {
		if rec.CompletedAt.After(cutoff) {
			filtered = append(filtered, rec)
		}
	}
	rs.records[agentID] = filtered
}
