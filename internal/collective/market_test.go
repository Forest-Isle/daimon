package collective

import (
	"context"
	"testing"
	"time"
)

func TestNewAgentMarket(t *testing.T) {
	rep := NewReputationSystem(0.05)
	m := NewAgentMarket(rep)
	if m == nil {
		t.Fatal("NewAgentMarket returned nil")
	}
	if m.board == nil {
		t.Error("board should not be nil")
	}
	if m.registry == nil {
		t.Error("registry should not be nil")
	}
	if m.settlement == nil {
		t.Error("settlement should not be nil")
	}
}

func TestRegisterAgent(t *testing.T) {
	rep := NewReputationSystem(0.05)
	m := NewAgentMarket(rep)
	m.RegisterAgent("a1", "Agent One", []string{"go", "docker"})

	agents := m.registry.All()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].Name != "Agent One" {
		t.Errorf("Name = %q, want 'Agent One'", agents[0].Name)
	}
}

func TestPostTask(t *testing.T) {
	rep := NewReputationSystem(0.05)
	m := NewAgentMarket(rep)

	task := m.PostTask("build service", []string{"go", "docker"}, "complex")
	if task == nil {
		t.Fatal("PostTask returned nil")
	}
	if task.Status != TaskOpen {
		t.Errorf("Status = %s, want TaskOpen", task.Status)
	}
	if len(task.RequiredSkills) != 2 {
		t.Errorf("expected 2 required skills, got %d", len(task.RequiredSkills))
	}
}

func TestSubmitBid(t *testing.T) {
	rep := NewReputationSystem(0.05)
	m := NewAgentMarket(rep)
	m.RegisterAgent("a1", "Agent One", []string{"go"})

	task := m.PostTask("build", []string{"go"}, "simple")

	bid, err := m.SubmitBid(task.ID, "a1", "Agent One", 0.9, 5*time.Minute, "I can do this")
	if err != nil {
		t.Fatalf("SubmitBid failed: %v", err)
	}
	if bid == nil {
		t.Fatal("bid should not be nil")
	}
	if bid.AgentID != "a1" {
		t.Errorf("AgentID = %q, want 'a1'", bid.AgentID)
	}
	if bid.Confidence != 0.9 {
		t.Errorf("Confidence = %f, want 0.9", bid.Confidence)
	}
}

func TestSubmitBid_TaskNotFound(t *testing.T) {
	rep := NewReputationSystem(0.05)
	m := NewAgentMarket(rep)

	_, err := m.SubmitBid("nonexistent", "a1", "Agent One", 0.9, time.Minute, "")
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestSubmitBid_TaskNotOpen(t *testing.T) {
	rep := NewReputationSystem(0.05)
	m := NewAgentMarket(rep)
	m.RegisterAgent("a1", "Agent One", []string{"go"})

	task := m.PostTask("build", []string{"go"}, "simple")
	task.Status = TaskAwarded // close the task

	_, err := m.SubmitBid(task.ID, "a1", "Agent One", 0.9, time.Minute, "")
	if err == nil {
		t.Error("expected error for non-open task")
	}
}

func TestRunAuction_NoBids(t *testing.T) {
	rep := NewReputationSystem(0.05)
	m := NewAgentMarket(rep)
	m.auctionTimeout = 10 * time.Millisecond

	task := m.PostTask("build", []string{"go"}, "simple")

	_, err := m.RunAuction(context.Background(), task.ID)
	if err == nil {
		t.Error("expected error for no bids")
	}
}

func TestRunAuction_TaskNotFound(t *testing.T) {
	rep := NewReputationSystem(0.05)
	m := NewAgentMarket(rep)

	_, err := m.RunAuction(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestRunAuction_SelectsWinner(t *testing.T) {
	rep := NewReputationSystem(0.05)
	rep.RecordOutcome("a1", "t1", "coding", true, 1.0)
	rep.RecordOutcome("a1", "t2", "coding", true, 1.0)
	rep.RecordOutcome("a2", "t3", "coding", true, 0.5)
	rep.RecordOutcome("a2", "t4", "coding", true, 0.5)

	m := NewAgentMarket(rep)
	m.auctionTimeout = 10 * time.Millisecond

	m.RegisterAgent("a1", "Agent One", []string{"go"})
	m.RegisterAgent("a2", "Agent Two", []string{"go"})

	task := m.PostTask("build", []string{"go"}, "simple")

	// Submit bids before auction starts
	_, _ = m.SubmitBid(task.ID, "a1", "Agent One", 0.9, 10*time.Minute, "")
	_, _ = m.SubmitBid(task.ID, "a2", "Agent Two", 0.7, 15*time.Minute, "")

	winner, err := m.RunAuction(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("RunAuction failed: %v", err)
	}
	if winner == nil {
		t.Fatal("expected a winner")
	}
	if task.Status != TaskAwarded {
		t.Errorf("task Status = %s, want TaskAwarded", task.Status)
	}
	if task.AwardedTo != winner.AgentID {
		t.Errorf("AwardedTo = %q, want %q", task.AwardedTo, winner.AgentID)
	}
}

func TestCompleteTask(t *testing.T) {
	rep := NewReputationSystem(0.05)
	m := NewAgentMarket(rep)
	m.auctionTimeout = 10 * time.Millisecond

	m.RegisterAgent("a1", "Agent One", []string{"go"})
	task := m.PostTask("build", []string{"go"}, "simple")

	_, _ = m.SubmitBid(task.ID, "a1", "Agent One", 0.9, time.Minute, "")
	_, _ = m.RunAuction(context.Background(), task.ID)

	// Complete successfully
	m.CompleteTask(task.ID, true, 0.95)
	if task.Status != TaskComplete {
		t.Errorf("Status = %s, want TaskComplete", task.Status)
	}

	// Verify reputation was updated
	score := rep.GetReputation("a1")
	if score <= 0 {
		t.Errorf("expected positive reputation after success, got %f", score)
	}

	// Verify settlement
	balance := m.settlement.Balance("a1")
	if balance != 0 {
		t.Errorf("expected balance 0 (no budget set), got %f", balance)
	}
}

func TestCompleteTask_Failure(t *testing.T) {
	rep := NewReputationSystem(0.05)
	m := NewAgentMarket(rep)
	m.auctionTimeout = 10 * time.Millisecond

	m.RegisterAgent("a1", "Agent One", []string{"go"})
	task := m.PostTask("build", []string{"go"}, "simple")
	_, _ = m.SubmitBid(task.ID, "a1", "Agent One", 0.9, time.Minute, "")
	_, _ = m.RunAuction(context.Background(), task.ID)

	m.CompleteTask(task.ID, false, 0)
	if task.Status != TaskFailed {
		t.Errorf("Status = %s, want TaskFailed", task.Status)
	}

	// Reputation should be lower after failure
	score := rep.GetReputation("a1")
	if score != 0 {
		t.Errorf("expected 0 reputation after failure, got %f", score)
	}
}

func TestCompleteTask_NoAwardee(t *testing.T) {
	rep := NewReputationSystem(0.05)
	m := NewAgentMarket(rep)

	task := m.PostTask("build", []string{"go"}, "simple")
	// Complete without awarding
	m.CompleteTask(task.ID, true, 0.9)
	if task.Status != TaskComplete {
		t.Errorf("Status = %s, want TaskComplete", task.Status)
	}
}

func TestTaskBoard_AddGet(t *testing.T) {
	tb := NewTaskBoard()
	task := &MarketTask{ID: "t1", Description: "test"}
	tb.Add(task)

	got := tb.Get("t1")
	if got == nil {
		t.Fatal("expected task 't1'")
	}
	if got.ID != "t1" {
		t.Errorf("ID = %q, want 't1'", got.ID)
	}

	none := tb.Get("nonexistent")
	if none != nil {
		t.Errorf("expected nil for nonexistent task")
	}
}

func TestTaskBoard_ListByStatus(t *testing.T) {
	tb := NewTaskBoard()
	tb.Add(&MarketTask{ID: "t1", Description: "open", Status: TaskOpen})
	tb.Add(&MarketTask{ID: "t2", Description: "open", Status: TaskOpen})
	tb.Add(&MarketTask{ID: "t3", Description: "complete", Status: TaskComplete})

	openTasks := tb.ListByStatus(TaskOpen)
	if len(openTasks) != 2 {
		t.Errorf("expected 2 open tasks, got %d", len(openTasks))
	}

	completeTasks := tb.ListByStatus(TaskComplete)
	if len(completeTasks) != 1 {
		t.Errorf("expected 1 complete task, got %d", len(completeTasks))
	}
}

func TestSettlementEngine(t *testing.T) {
	se := NewSettlementEngine()

	// Initial balance should be 0
	if b := se.Balance("a1"); b != 0 {
		t.Errorf("initial balance = %f, want 0", b)
	}

	// Settle success
	se.Settle(&MarketTask{AwardedTo: "a1", Budget: 100}, true)
	if b := se.Balance("a1"); b != 100 {
		t.Errorf("after settle: balance = %f, want 100", b)
	}

	// Settle failure should not add balance
	se.Settle(&MarketTask{AwardedTo: "a1", Budget: 50}, false)
	if b := se.Balance("a1"); b != 100 {
		t.Errorf("after failed settle: balance = %f, want 100", b)
	}
}

func TestAgentRegistry(t *testing.T) {
	ar := NewAgentRegistry()

	// Register agents
	ar.Register("a1", "Agent One", []string{"go", "python"})
	ar.Register("a2", "Agent Two", []string{"java"})
	ar.Register("a3", "Agent Three", []string{"python", "rust"})

	// All agents
	all := ar.All()
	if len(all) != 3 {
		t.Errorf("expected 3 agents, got %d", len(all))
	}

	// Filter by skill
	pythonAgents := ar.FilterBySkill("python")
	if len(pythonAgents) != 2 {
		t.Errorf("expected 2 python agents, got %d", len(pythonAgents))
	}

	rustAgents := ar.FilterBySkill("rust")
	if len(rustAgents) != 1 {
		t.Errorf("expected 1 rust agent, got %d", len(rustAgents))
	}

	// Unknown skill
	unknown := ar.FilterBySkill("nonexistent")
	if len(unknown) != 0 {
		t.Errorf("expected 0 agents for unknown skill, got %d", len(unknown))
	}
}

func TestBidScore(t *testing.T) {
	rep := NewReputationSystem(0.05)
	rep.RecordOutcome("a1", "t1", "coding", true, 1.0)
	rep.RecordOutcome("a1", "t2", "coding", true, 1.0)

	m := NewAgentMarket(rep)

	// High reputation, high confidence, fast, cheap
	bid1 := &Bid{AgentID: "a1", Confidence: 0.9, EstimatedDuration: time.Minute, Price: 10, Reputation: rep.GetReputation("a1")}
	score1 := m.bidScore(bid1)

	// Low reputation, low confidence, slow, expensive
	bid2 := &Bid{AgentID: "a2", Confidence: 0.3, EstimatedDuration: time.Hour, Price: 1000, Reputation: 0.3}
	score2 := m.bidScore(bid2)

	if score1 <= score2 {
		t.Errorf("expected bid1 score (%f) > bid2 score (%f)", score1, score2)
	}
}

func TestSelectWinner(t *testing.T) {
	rep := NewReputationSystem(0.05)
	m := NewAgentMarket(rep)

	bids := []*Bid{
		{AgentID: "a1", Confidence: 0.5, EstimatedDuration: time.Minute, Price: 50, Reputation: 0.5},
		{AgentID: "a2", Confidence: 0.9, EstimatedDuration: time.Minute, Price: 50, Reputation: 0.9},
		{AgentID: "a3", Confidence: 0.3, EstimatedDuration: time.Hour, Price: 100, Reputation: 0.3},
	}

	winner := m.selectWinner(bids)
	if winner == nil {
		t.Fatal("expected a winner")
	}
	if winner.AgentID != "a2" {
		t.Errorf("expected 'a2' as winner, got '%s'", winner.AgentID)
	}
}

func TestRunAuction_CancelledContext(t *testing.T) {
	rep := NewReputationSystem(0.05)
	m := NewAgentMarket(rep)
	m.auctionTimeout = 10 * time.Second // long timeout

	task := m.PostTask("build", []string{"go"}, "simple")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := m.RunAuction(ctx, task.ID)
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}
