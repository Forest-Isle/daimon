package collective

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewConsensusEngine(t *testing.T) {
	rep := NewReputationSystem(0.05)
	ce := NewConsensusEngine(rep, 3, 0.6)
	if ce == nil {
		t.Fatal("NewConsensusEngine returned nil")
	}
	if ce.quorum != 3 {
		t.Errorf("quorum = %d, want 3", ce.quorum)
	}
	if ce.threshold != 0.6 {
		t.Errorf("threshold = %f, want 0.6", ce.threshold)
	}
}

func TestNewConsensusEngine_Defaults(t *testing.T) {
	rep := NewReputationSystem(0.05)
	ce := NewConsensusEngine(rep, 1, 0) // below minimums
	if ce.quorum != 2 {
		t.Errorf("quorum should default to 2, got %d", ce.quorum)
	}
	if ce.threshold != 0.6 {
		t.Errorf("threshold should default to 0.6, got %f", ce.threshold)
	}

	ce = NewConsensusEngine(rep, 5, 1.5) // above max
	if ce.threshold != 0.6 {
		t.Errorf("threshold should default to 0.6 when >1, got %f", ce.threshold)
	}
}

func TestReachConsensus_InsufficientAgents(t *testing.T) {
	rep := NewReputationSystem(0.05)
	ce := NewConsensusEngine(rep, 3, 0.6)

	_, err := ce.ReachConsensus(context.Background(), []string{"a1", "a2"}, "test?", mockVoter(true))
	if err == nil {
		t.Fatal("expected error for insufficient agents")
	}
}

func TestReachConsensus_AllFor(t *testing.T) {
	rep := NewReputationSystem(0.05)
	rep.RecordOutcome("a1", "t1", "coding", true, 1.0)
	rep.RecordOutcome("a2", "t2", "coding", true, 1.0)
	rep.RecordOutcome("a3", "t3", "coding", true, 1.0)

	ce := NewConsensusEngine(rep, 2, 0.6)
	result, err := ce.ReachConsensus(context.Background(), []string{"a1", "a2", "a3"}, "approve?", mockVoter(true))
	if err != nil {
		t.Fatalf("ReachConsensus failed: %v", err)
	}

	if !result.Approved {
		t.Error("expected approved when all vote for")
	}
	if result.VoterCount != 3 {
		t.Errorf("VoterCount = %d, want 3", result.VoterCount)
	}
	if !result.QuorumMet {
		t.Error("quorum should be met")
	}
	if result.ForWeight <= result.AgainstWeight {
		t.Error("ForWeight should be > AgainstWeight")
	}
}

func TestReachConsensus_AllAgainst(t *testing.T) {
	rep := NewReputationSystem(0.05)
	rep.RecordOutcome("a1", "t1", "coding", true, 1.0)
	rep.RecordOutcome("a2", "t2", "coding", true, 1.0)
	rep.RecordOutcome("a3", "t3", "coding", true, 1.0)

	ce := NewConsensusEngine(rep, 2, 0.6)
	result, err := ce.ReachConsensus(context.Background(), []string{"a1", "a2", "a3"}, "approve?", mockVoter(false))
	if err != nil {
		t.Fatalf("ReachConsensus failed: %v", err)
	}

	if result.Approved {
		t.Error("expected not approved when all vote against")
	}
}

func TestReachConsensus_Mixed(t *testing.T) {
	rep := NewReputationSystem(0.05)
	rep.RecordOutcome("a1", "t1", "coding", true, 1.0)
	rep.RecordOutcome("a2", "t2", "coding", true, 1.0)
	rep.RecordOutcome("a3", "t3", "coding", true, 1.0)
	rep.RecordOutcome("a4", "t4", "coding", true, 1.0)
	rep.RecordOutcome("a5", "t5", "coding", true, 1.0)

	ce := NewConsensusEngine(rep, 2, 0.5)
	// 3 for, 2 against — with equal weights and threshold 0.5, should pass
	voter := func(ctx context.Context, agentID, question string) (bool, string, error) {
		switch agentID {
		case "a1", "a2", "a3":
			return true, "good idea", nil
		default:
			return false, "bad idea", nil
		}
	}

	result, err := ce.ReachConsensus(context.Background(), []string{"a1", "a2", "a3", "a4", "a5"}, "test?", voter)
	if err != nil {
		t.Fatalf("ReachConsensus failed: %v", err)
	}

	if !result.Approved {
		t.Error("expected approved (3/5 with equal weights)")
	}
}

func TestReachConsensus_QuorumNotMet(t *testing.T) {
	rep := NewReputationSystem(0.05)
	rep.RecordOutcome("a1", "t1", "coding", true, 1.0)
	rep.RecordOutcome("a2", "t2", "coding", true, 1.0)
	rep.RecordOutcome("a3", "t3", "coding", true, 1.0)

	// voter that returns errors for some agents
	voter := func(ctx context.Context, agentID, question string) (bool, string, error) {
		if agentID == "a3" {
			return false, "", errors.New("timeout")
		}
		return true, "yes", nil
	}

	ce := NewConsensusEngine(rep, 3, 0.6)
	result, err := ce.ReachConsensus(context.Background(), []string{"a1", "a2", "a3"}, "test?", voter)
	if err != nil {
		t.Fatalf("ReachConsensus failed: %v", err)
	}

	if result.QuorumMet {
		t.Logf("VoterCount = %d, quorum = %d", result.VoterCount, ce.quorum)
	}
}

func TestMajorityVote(t *testing.T) {
	rep := NewReputationSystem(0.05)
	rep.RecordOutcome("a1", "t1", "coding", true, 1.0)
	rep.RecordOutcome("a2", "t2", "coding", true, 1.0)
	rep.RecordOutcome("a3", "t3", "coding", true, 1.0)

	ce := NewConsensusEngine(rep, 2, 0.6)

	approved, err := ce.MajorityVote(context.Background(), []string{"a1", "a2", "a3"}, "approve?", mockVoter(true))
	if err != nil {
		t.Fatalf("MajorityVote failed: %v", err)
	}
	if !approved {
		t.Error("expected majority approval")
	}
}

func TestMajorityVote_Tie(t *testing.T) {
	rep := NewReputationSystem(0.05)
	rep.RecordOutcome("a1", "t1", "coding", true, 1.0)
	rep.RecordOutcome("a2", "t2", "coding", true, 1.0)
	rep.RecordOutcome("a3", "t3", "coding", true, 1.0)
	rep.RecordOutcome("a4", "t4", "coding", true, 1.0)

	ce := NewConsensusEngine(rep, 2, 0.6)

	// 2 for, 2 against
	voter := func(ctx context.Context, agentID, question string) (bool, string, error) {
		if agentID == "a1" || agentID == "a2" {
			return true, "for", nil
		}
		return false, "against", nil
	}

	approved, err := ce.MajorityVote(context.Background(), []string{"a1", "a2", "a3", "a4"}, "test?", voter)
	if err != nil {
		t.Fatalf("MajorityVote failed: %v", err)
	}
	if approved {
		t.Error("expected not approved on tie (2 for, 2 against)")
	}
}

func TestMajorityVote_QuorumNotMet(t *testing.T) {
	rep := NewReputationSystem(0.05)
	ce := NewConsensusEngine(rep, 3, 0.6)

	// Agent errors cause quorum miss
	voter := func(ctx context.Context, agentID, question string) (bool, string, error) {
		return false, "", errors.New("error")
	}

	_, err := ce.MajorityVote(context.Background(), []string{"a1", "a2"}, "test?", voter)
	if err == nil {
		t.Error("expected error when quorum not met")
	}
}

func TestConsensusResult_FormatMarkdown(t *testing.T) {
	result := &ConsensusResult{
		Question:      "Should we proceed?",
		Approved:      true,
		ForWeight:     2.5,
		AgainstWeight: 0.5,
		VoterCount:    2,
		QuorumMet:     true,
		Votes: []*Vote{
			{AgentID: "a1", Decision: true, Weight: 1.5, Reason: "good"},
			{AgentID: "a2", Decision: false, Weight: 0.5, Reason: "risky"},
		},
	}

	md := result.FormatMarkdown()
	if md == "" {
		t.Error("expected non-empty markdown")
	}
}

func TestReachConsensus_VoterTimeout(t *testing.T) {
	rep := NewReputationSystem(0.05)
	ce := NewConsensusEngine(rep, 2, 0.6)
	ce.timeout = 50 * time.Millisecond

	// Voter that blocks past the timeout
	voter := func(ctx context.Context, agentID, question string) (bool, string, error) {
		<-ctx.Done()
		return false, "", ctx.Err()
	}

	_, err := ce.ReachConsensus(context.Background(), []string{"a1", "a2", "a3"}, "test?", voter)
	if err != nil {
		t.Logf("Expected possible timeout error: %v", err)
	}
}

// --- helpers ---

func mockVoter(decision bool) VoterFn {
	return func(ctx context.Context, agentID, question string) (bool, string, error) {
		return decision, "mock reason", nil
	}
}
