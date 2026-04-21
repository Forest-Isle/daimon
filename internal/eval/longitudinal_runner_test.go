package eval

import (
	"context"
	"strings"
	"testing"
	"time"
)

// mockInsightRunner implements both AgentRunner and InsightsTrigger for testing.
type mockInsightRunner struct {
	mockRunner
	insightsCalled int
	trajectories   int
}

func (m *mockInsightRunner) RunInsightsCycle() (bool, string) {
	m.insightsCalled++
	return true, "mock insights ran"
}

func (m *mockInsightRunner) TrajectoryCount() int { return m.trajectories }

func TestRunLongitudinal_TwoRounds(t *testing.T) {
	runner := &mockInsightRunner{
		mockRunner: mockRunner{
			results: map[string]*EvalResult{
				"t1": {TaskID: "t1", Success: true, Duration: 50 * time.Millisecond, Confidence: 0.9, AssertionPassRate: 1.0, Timestamp: time.Now()},
				"t2": {TaskID: "t2", Success: true, Duration: 60 * time.Millisecond, Confidence: 0.8, AssertionPassRate: 0.9, Timestamp: time.Now()},
			},
		},
		trajectories: 10, // above MinTrajectories threshold
	}

	tasks := []TaskCase{
		{ID: "t1", Goal: "test task 1"},
		{ID: "t2", Goal: "test task 2"},
	}

	cfg := LongitudinalConfig{Rounds: 2, MinTrajectories: 5}
	result, err := RunLongitudinal(context.Background(), runner, tasks, cfg)
	if err != nil {
		t.Fatalf("RunLongitudinal: %v", err)
	}

	if len(result.Rounds) != 2 {
		t.Fatalf("expected 2 rounds, got %d", len(result.Rounds))
	}

	// Round 0 should have suite results but no comparison.
	r0 := result.Rounds[0]
	if r0.Suite == nil {
		t.Error("round 0: expected Suite to be non-nil")
	}
	if r0.Comparison != nil {
		t.Error("round 0: Comparison should be nil (no previous round)")
	}
	// Insights should have fired after round 0 (between rounds).
	if !r0.InsightsRan {
		t.Errorf("round 0: InsightsRan should be true, reason=%q", r0.InsightsReason)
	}

	// Round 1 should have a comparison against round 0.
	r1 := result.Rounds[1]
	if r1.Suite == nil {
		t.Error("round 1: expected Suite to be non-nil")
	}
	if r1.Comparison == nil {
		t.Error("round 1: expected Comparison to be non-nil")
	}
	// No insights after last round.
	if r1.InsightsRan {
		t.Error("round 1 (last): InsightsRan should be false — no cycle runs after the last round")
	}

	// insightsCalled should be exactly 1 (only between round 0 and round 1).
	if runner.insightsCalled != 1 {
		t.Errorf("insightsCalled = %d, want 1", runner.insightsCalled)
	}

	if result.StartedAt.IsZero() {
		t.Error("StartedAt should not be zero")
	}
	if result.CompletedAt.IsZero() {
		t.Error("CompletedAt should not be zero")
	}
}

func TestRunLongitudinal_InsufficientTrajectories(t *testing.T) {
	runner := &mockInsightRunner{
		mockRunner: mockRunner{
			results: map[string]*EvalResult{
				"t1": {TaskID: "t1", Success: true, Duration: 50 * time.Millisecond, Timestamp: time.Now()},
			},
		},
		trajectories: 2, // below default threshold of 5
	}

	tasks := []TaskCase{
		{ID: "t1", Goal: "test task"},
	}

	cfg := LongitudinalConfig{Rounds: 2, MinTrajectories: 5}
	result, err := RunLongitudinal(context.Background(), runner, tasks, cfg)
	if err != nil {
		t.Fatalf("RunLongitudinal: %v", err)
	}

	r0 := result.Rounds[0]
	if r0.InsightsRan {
		t.Error("InsightsRan should be false when trajectories < MinTrajectories")
	}
	if !strings.Contains(r0.InsightsReason, "2") {
		t.Errorf("reason should mention trajectory count, got %q", r0.InsightsReason)
	}
	if !strings.Contains(r0.InsightsReason, "5") {
		t.Errorf("reason should mention required count, got %q", r0.InsightsReason)
	}
	if runner.insightsCalled != 0 {
		t.Errorf("insightsCalled = %d, want 0", runner.insightsCalled)
	}
}

func TestRunLongitudinal_NoInsightsTrigger(t *testing.T) {
	// Use a plain mockRunner that does NOT implement InsightsTrigger.
	runner := &mockRunner{
		results: map[string]*EvalResult{
			"t1": {TaskID: "t1", Success: true, Duration: 50 * time.Millisecond, Timestamp: time.Now()},
		},
	}

	tasks := []TaskCase{
		{ID: "t1", Goal: "test task"},
	}

	cfg := LongitudinalConfig{Rounds: 2}
	result, err := RunLongitudinal(context.Background(), runner, tasks, cfg)
	if err != nil {
		t.Fatalf("RunLongitudinal: %v", err)
	}

	r0 := result.Rounds[0]
	if r0.InsightsRan {
		t.Error("InsightsRan should be false when runner does not implement InsightsTrigger")
	}
	if r0.InsightsReason != "runner does not implement InsightsTrigger" {
		t.Errorf("unexpected reason: %q", r0.InsightsReason)
	}
}

func TestRunLongitudinal_DefaultConfig(t *testing.T) {
	runner := &mockInsightRunner{
		mockRunner:   mockRunner{},
		trajectories: 10,
	}

	tasks := []TaskCase{
		{ID: "t1", Goal: "task 1"},
	}

	// Zero-value config should default Rounds=2, MinTrajectories=5.
	result, err := RunLongitudinal(context.Background(), runner, tasks, LongitudinalConfig{})
	if err != nil {
		t.Fatalf("RunLongitudinal: %v", err)
	}

	if result.Config.Rounds != 2 {
		t.Errorf("expected default Rounds=2, got %d", result.Config.Rounds)
	}
	if result.Config.MinTrajectories != 5 {
		t.Errorf("expected default MinTrajectories=5, got %d", result.Config.MinTrajectories)
	}
	if len(result.Rounds) != 2 {
		t.Errorf("expected 2 rounds, got %d", len(result.Rounds))
	}
}

func TestLongitudinalResult_FormatMarkdown(t *testing.T) {
	runner := &mockInsightRunner{
		mockRunner: mockRunner{
			results: map[string]*EvalResult{
				"t1": {TaskID: "t1", Success: true, Duration: 50 * time.Millisecond, AssertionPassRate: 1.0, Confidence: 0.9, Timestamp: time.Now()},
			},
		},
		trajectories: 10,
	}

	tasks := []TaskCase{{ID: "t1", Goal: "test"}}
	result, err := RunLongitudinal(context.Background(), runner, tasks, LongitudinalConfig{Rounds: 2})
	if err != nil {
		t.Fatalf("RunLongitudinal: %v", err)
	}

	md := result.FormatMarkdown()
	if md == "" {
		t.Fatal("FormatMarkdown returned empty string")
	}
	if !strings.Contains(md, "# Longitudinal Evaluation Report") {
		t.Error("markdown should contain header")
	}
	if !strings.Contains(md, "## Round 0") {
		t.Error("markdown should contain Round 0 section")
	}
	if !strings.Contains(md, "## Round 1") {
		t.Error("markdown should contain Round 1 section")
	}
	if !strings.Contains(md, "Insights cycle ran") {
		t.Error("markdown should note that insights ran")
	}
}
