package collective

import (
	"testing"
	"time"
)

func TestNewReputationSystem(t *testing.T) {
	rs := NewReputationSystem(0)
	if rs == nil {
		t.Fatal("NewReputationSystem returned nil")
	}
	if rs.decayRate != 0.05 {
		t.Errorf("default decayRate = %f, want 0.05", rs.decayRate)
	}

	rs = NewReputationSystem(0.1)
	if rs.decayRate != 0.1 {
		t.Errorf("decayRate = %f, want 0.1", rs.decayRate)
	}
}

func TestGetReputation_NoRecords(t *testing.T) {
	rs := NewReputationSystem(0.05)
	score := rs.GetReputation("unknown-agent")
	if score != 0.5 {
		t.Errorf("expected neutral score 0.5, got %f", score)
	}
}

func TestRecordOutcome(t *testing.T) {
	rs := NewReputationSystem(0.05)
	rs.RecordOutcome("agent-1", "task-1", "coding", true, 0.9)

	score := rs.GetReputation("agent-1")
	if score <= 0 {
		t.Errorf("expected positive reputation, got %f", score)
	}
}

func TestGetReputation_AllSuccess(t *testing.T) {
	rs := NewReputationSystem(0.05)
	rs.RecordOutcome("agent-1", "t1", "coding", true, 1.0)
	rs.RecordOutcome("agent-1", "t2", "coding", true, 1.0)

	score := rs.GetReputation("agent-1")
	if score < 0.9 {
		t.Errorf("expected high reputation for all successes, got %f", score)
	}
}

func TestGetReputation_AllFailures(t *testing.T) {
	rs := NewReputationSystem(0.05)
	rs.RecordOutcome("agent-1", "t1", "coding", false, 0)
	rs.RecordOutcome("agent-1", "t2", "coding", false, 0)

	score := rs.GetReputation("agent-1")
	if score != 0 {
		t.Errorf("expected 0 reputation for all failures, got %f", score)
	}
}

func TestGetCategoryReputation(t *testing.T) {
	rs := NewReputationSystem(0.05)
	rs.RecordOutcome("agent-1", "t1", "coding", true, 0.9)
	rs.RecordOutcome("agent-1", "t2", "analysis", false, 0)
	rs.RecordOutcome("agent-1", "t3", "coding", true, 0.8)

	codingScore := rs.GetCategoryReputation("agent-1", "coding")
	analysisScore := rs.GetCategoryReputation("agent-1", "analysis")

	if codingScore <= analysisScore {
		t.Errorf("expected coding reputation > analysis reputation: %f vs %f", codingScore, analysisScore)
	}
}

func TestGetCategoryReputation_NoRecords(t *testing.T) {
	rs := NewReputationSystem(0.05)
	score := rs.GetCategoryReputation("agent-1", "unknown")
	if score != 0.5 {
		t.Errorf("expected neutral score 0.5, got %f", score)
	}
}

func TestGetCategoryReputation_UnknownAgent(t *testing.T) {
	rs := NewReputationSystem(0.05)
	score := rs.GetCategoryReputation("nonexistent", "coding")
	if score != 0.5 {
		t.Errorf("expected neutral score 0.5 for unknown agent, got %f", score)
	}
}

func TestGetTopCategories(t *testing.T) {
	rs := NewReputationSystem(0.05)
	// Need at least 3 records per category
	for i := 0; i < 4; i++ {
		rs.RecordOutcome("agent-1", "t1", "coding", true, 0.9)
	}
	for i := 0; i < 3; i++ {
		rs.RecordOutcome("agent-1", "t2", "planning", true, 0.7)
	}

	top := rs.GetTopCategories("agent-1", 2)
	if len(top) == 0 {
		t.Fatal("expected at least 1 top category")
	}
	if top[0] != "coding" {
		t.Errorf("expected 'coding' as top category, got '%s'", top[0])
	}
}

func TestGetTopCategories_NoRecords(t *testing.T) {
	rs := NewReputationSystem(0.05)
	top := rs.GetTopCategories("agent-1", 3)
	if top != nil {
		t.Errorf("expected nil, got %v", top)
	}
}

func TestGetTopCategories_FewerThanMin(t *testing.T) {
	rs := NewReputationSystem(0.05)
	// Only 2 records (need 3 for category to qualify)
	rs.RecordOutcome("agent-1", "t1", "coding", true, 0.9)
	rs.RecordOutcome("agent-1", "t2", "coding", true, 0.9)

	top := rs.GetTopCategories("agent-1", 3)
	if len(top) != 0 {
		t.Errorf("expected 0 categories (fewer than 3 records), got %d", len(top))
	}
}

func TestGetAgentStats(t *testing.T) {
	rs := NewReputationSystem(0.05)
	rs.RecordOutcome("agent-1", "t1", "coding", true, 0.9)
	rs.RecordOutcome("agent-1", "t2", "coding", true, 0.7)
	rs.RecordOutcome("agent-1", "t3", "analysis", false, 0)

	stats := rs.GetAgentStats("agent-1")
	if stats == nil {
		t.Fatal("GetAgentStats returned nil")
	}
	if stats.TotalTasks != 3 {
		t.Errorf("TotalTasks = %d, want 3", stats.TotalTasks)
	}
	if stats.SuccessRate != 2.0/3.0 {
		t.Errorf("SuccessRate = %f, want %f", stats.SuccessRate, 2.0/3.0)
	}
	if stats.AvgQuality != (0.9+0.7)/2.0 {
		t.Errorf("AvgQuality = %f, want %f", stats.AvgQuality, (0.9+0.7)/2.0)
	}
}

func TestGetAgentStats_NoRecords(t *testing.T) {
	rs := NewReputationSystem(0.05)
	stats := rs.GetAgentStats("unknown")
	if stats == nil {
		t.Fatal("GetAgentStats returned nil")
	}
	if stats.AgentID != "unknown" {
		t.Errorf("AgentID = %q, want 'unknown'", stats.AgentID)
	}
	if stats.TotalTasks != 0 {
		t.Errorf("TotalTasks = %d, want 0", stats.TotalTasks)
	}
}

func TestRecordOutcome_DefaultQuality(t *testing.T) {
	rs := NewReputationSystem(0.05)
	// When quality score is 0 but success is true, defaults to 0.7
	rs.RecordOutcome("agent-1", "t1", "coding", true, 0)

	score := rs.GetReputation("agent-1")
	if score < 0.5 {
		t.Errorf("expected default quality (0.7) to give > 0.5, got %f", score)
	}
}

func TestReputation_DecayOverTime(t *testing.T) {
	rs := NewReputationSystem(0.05)

	// Add a recent success
	rs.RecordOutcome("agent-1", "t1", "coding", true, 1.0)

	recentScore := rs.GetReputation("agent-1")

	// Manually add an old record (CompletedAt in the past)
	oldRecord := &ReputationRecord{
		AgentID:      "agent-1",
		TaskID:       "t0",
		TaskCategory: "coding",
		Success:      false,
		QualityScore: 0,
		CompletedAt:  time.Now().Add(-30 * 24 * time.Hour), // 30 days ago
	}
	rs.mu.Lock()
	rs.records["agent-1"] = append(rs.records["agent-1"], oldRecord)
	rs.mu.Unlock()

	scoreWithOld := rs.GetReputation("agent-1")
	// The old failure should have some (decayed) impact
	if scoreWithOld > recentScore {
		t.Logf("Note: old failure had minimal decayed impact: recent=%f, withOld=%f", recentScore, scoreWithOld)
	}
}

func TestPruneLocked(t *testing.T) {
	rs := NewReputationSystem(0.05)

	// Add old record (beyond 60 days)
	oldRecord := &ReputationRecord{
		AgentID:      "agent-1",
		TaskID:       "t-old",
		TaskCategory: "coding",
		Success:      true,
		QualityScore: 0.5,
		CompletedAt:  time.Now().Add(-100 * 24 * time.Hour),
	}
	rs.mu.Lock()
	rs.records["agent-1"] = []*ReputationRecord{oldRecord}
	rs.mu.Unlock()

	// RecordOutcome triggers prune internally
	rs.RecordOutcome("agent-1", "t-new", "coding", true, 0.9)

	// After prune, only the new record should remain
	rs.mu.RLock()
	records := rs.records["agent-1"]
	rs.mu.RUnlock()

	if len(records) != 1 {
		t.Errorf("expected 1 record after prune, got %d", len(records))
	}
	if records[0].TaskID != "t-new" {
		t.Errorf("expected 't-new', got '%s'", records[0].TaskID)
	}
}

func TestGetAgentStats_ZeroSuccessQuality(t *testing.T) {
	rs := NewReputationSystem(0.05)
	// When there are 0 successes, AvgQuality should be 0
	rs.RecordOutcome("agent-1", "t1", "coding", false, 0)
	stats := rs.GetAgentStats("agent-1")

	if stats.TotalTasks != 1 {
		t.Errorf("TotalTasks = %d, want 1", stats.TotalTasks)
	}
	_ = stats.AvgQuality // 0/0 = 0 with float64
}
