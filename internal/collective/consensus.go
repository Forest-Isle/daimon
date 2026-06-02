package collective

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ConsensusEngine enables multi-agent voting on critical decisions.
type ConsensusEngine struct {
	reputation *ReputationSystem
	quorum     int
	threshold  float64 // fraction of weighted votes needed to pass
	timeout    time.Duration
}

// NewConsensusEngine creates a new consensus engine.
func NewConsensusEngine(rep *ReputationSystem, quorum int, threshold float64) *ConsensusEngine {
	if quorum < 2 {
		quorum = 2
	}
	if threshold <= 0 || threshold > 1 {
		threshold = 0.6
	}
	return &ConsensusEngine{
		reputation: rep,
		quorum:     quorum,
		threshold:  threshold,
		timeout:    60 * time.Second,
	}
}

// ConsensusResult holds the outcome of a consensus vote.
type ConsensusResult struct {
	Question      string    `json:"question"`
	Approved      bool      `json:"approved"`
	ForWeight     float64   `json:"for_weight"`
	AgainstWeight float64   `json:"against_weight"`
	VoterCount    int       `json:"voter_count"`
	QuorumMet     bool      `json:"quorum_met"`
	Votes         []*Vote   `json:"votes"`
	StartedAt     time.Time `json:"started_at"`
	CompletedAt   time.Time `json:"completed_at"`
}

// Vote is a single agent's decision on a question.
type Vote struct {
	AgentID  string  `json:"agent_id"`
	Decision bool    `json:"decision"` // true = for, false = against
	Reason   string  `json:"reason"`
	Weight   float64 `json:"weight"` // reputation-weighted
}

// VoterFn is called for each agent to collect their vote.
// It receives the question and returns (decision, reason, error).
type VoterFn func(ctx context.Context, agentID, question string) (bool, string, error)

// ReachConsensus collects votes from all agents and determines the outcome.
func (ce *ConsensusEngine) ReachConsensus(
	ctx context.Context,
	agentIDs []string,
	question string,
	voter VoterFn,
) (*ConsensusResult, error) {
	if len(agentIDs) < ce.quorum {
		return nil, fmt.Errorf("consensus: insufficient agents (%d < quorum %d)", len(agentIDs), ce.quorum)
	}

	ctx, cancel := context.WithTimeout(ctx, ce.timeout)
	defer cancel()

	result := &ConsensusResult{
		Question:  question,
		Votes:     make([]*Vote, 0, len(agentIDs)),
		StartedAt: time.Now(),
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, agentID := range agentIDs {
		wg.Add(1)
		go func(aid string) {
			defer wg.Done()

			decision, reason, err := voter(ctx, aid, question)
			if err != nil {
				slog.Warn("consensus: voter failed", "agent", aid, "err", err)
				return
			}

			weight := ce.reputation.GetReputation(aid)

			mu.Lock()
			result.Votes = append(result.Votes, &Vote{
				AgentID:  aid,
				Decision: decision,
				Reason:   reason,
				Weight:   weight,
			})
			mu.Unlock()
		}(agentID)
	}

	wg.Wait()
	result.CompletedAt = time.Now()

	// Tally weighted votes
	var forWeight, againstWeight float64
	for _, v := range result.Votes {
		if v.Decision {
			forWeight += v.Weight
		} else {
			againstWeight += v.Weight
		}
	}

	result.ForWeight = forWeight
	result.AgainstWeight = againstWeight
	result.VoterCount = len(result.Votes)
	result.QuorumMet = result.VoterCount >= ce.quorum

	if !result.QuorumMet {
		result.Approved = false
		return result, nil
	}

	totalWeight := forWeight + againstWeight
	if totalWeight > 0 {
		result.Approved = forWeight/totalWeight >= ce.threshold
	}

	slog.Info("consensus: vote complete",
		"question", question[:min(50, len(question))],
		"approved", result.Approved,
		"for", forWeight,
		"against", againstWeight,
		"voters", result.VoterCount,
	)

	return result, nil
}

// MajorityVote is a simple helper for non-critical decisions.
// Returns the majority decision (simple count, not weighted).
func (ce *ConsensusEngine) MajorityVote(
	ctx context.Context,
	agentIDs []string,
	question string,
	voter VoterFn,
) (bool, error) {
	result, err := ce.ReachConsensus(ctx, agentIDs, question, voter)
	if err != nil {
		return false, err
	}

	if !result.QuorumMet {
		return false, fmt.Errorf("quorum not met: %d/%d voters", result.VoterCount, ce.quorum)
	}

	var forCount, againstCount int
	for _, v := range result.Votes {
		if v.Decision {
			forCount++
		} else {
			againstCount++
		}
	}

	return forCount > againstCount, nil
}

// FormatMarkdown returns a human-readable summary of the consensus result.
func (cr *ConsensusResult) FormatMarkdown() string {
	s := fmt.Sprintf(`## Consensus Result

**Question**: %s
**Approved**: %v
**Voters**: %d (quorum: %v)
**For**: %.2f | **Against**: %.2f

| Agent | Decision | Weight | Reason |
|-------|----------|--------|--------|
`, cr.Question, cr.Approved, cr.VoterCount, cr.QuorumMet,
		cr.ForWeight, cr.AgainstWeight)

	for _, v := range cr.Votes {
		dec := "❌"
		if v.Decision {
			dec = "✅"
		}
		r := v.Reason
		if len(r) > 80 {
			r = r[:80] + "..."
		}
		s += fmt.Sprintf("| %s | %s | %.2f | %s |\n", v.AgentID, dec, v.Weight, r)
	}

	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
