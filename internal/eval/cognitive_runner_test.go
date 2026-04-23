package eval

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
	"github.com/Forest-Isle/IronClaw/internal/memory"
)

func TestRunTask_SkillEvolutionDraftQuality(t *testing.T) {
	ctx := context.Background()
	r := &CognitiveAgentRunner{
		agent:   &agent.CognitiveAgent{},
		channel: &EvalChannel{},
	}
	res, err := r.RunTask(ctx, TaskCase{
		ID:         TaskIDSkillEvolutionDraftQuality,
		Goal:       "[offline] skill draft check",
		Complexity: "simple",
		Dimension:  DimSkillEvolution,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.SkillEvolution == nil {
		t.Fatal("expected SkillEvolution metrics")
	}
	if res.SkillEvolution.Score < 0.65 {
		t.Fatalf("expected score >= 0.65, got %f fails=%v", res.SkillEvolution.Score, res.SkillEvolution.ChecksFailed)
	}
	if !res.Success {
		t.Fatalf("expected success, err=%q score=%f", res.Error, res.FinalScore)
	}
	if res.Dimension != DimSkillEvolution {
		t.Errorf("dimension = %s", res.Dimension)
	}
}

func TestEvalChannel_AutoApproves(t *testing.T) {
	ch := &EvalChannel{}
	approved, err := ch.SendApprovalRequest(context.Background(), channel.MessageTarget{}, "bash", "rm -rf /")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approved {
		t.Error("EvalChannel should auto-approve all tool calls")
	}
}

func TestEvalChannel_CapturesMessages(t *testing.T) {
	ch := &EvalChannel{}
	ctx := context.Background()

	_ = ch.Send(ctx, outMsg("hello"))
	_ = ch.Send(ctx, outMsg("world"))

	msgs := ch.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Text != "hello" || msgs[1].Text != "world" {
		t.Errorf("messages = %v, want [hello, world]", msgs)
	}

	if ch.LastMessage() != "world" {
		t.Errorf("LastMessage() = %q, want %q", ch.LastMessage(), "world")
	}
}

func TestEvalChannel_Reset(t *testing.T) {
	ch := &EvalChannel{}
	_ = ch.Send(context.Background(), outMsg("test"))
	ch.Reset()

	if len(ch.Messages()) != 0 {
		t.Error("expected no messages after reset")
	}
	if ch.LastMessage() != "" {
		t.Error("expected empty last message after reset")
	}
}

func TestEvalChannel_StreamUpdater(t *testing.T) {
	ch := &EvalChannel{}
	updater, err := ch.SendStreaming(context.Background(), channel.MessageTarget{})
	if err != nil {
		t.Fatal(err)
	}

	_ = updater.Update("partial")
	_ = updater.Finish("complete message")

	if ch.LastMessage() != "complete message" {
		t.Errorf("expected Finish to capture message, got %q", ch.LastMessage())
	}
}

func TestEvalHook_CapturesEvents(t *testing.T) {
	hook := NewEvalHook()

	ref := evolution.ReflectionEvent{
		SessionID:  "sess1",
		Succeeded:  true,
		Confidence: 0.85,
		ToolsUsed:  []string{"bash", "file_write"},
		ReplanCount: 1,
	}
	ep := evolution.EpisodeEvent{
		SessionID:  "sess1",
		Succeeded:  true,
		DurationMs: 5000,
		ReplanCount: 1,
	}
	tool := evolution.ToolExecEvent{
		SessionID: "sess1",
		ToolName:  "bash",
		Succeeded: true,
	}

	hook.OnReflectionComplete(context.Background(), ref)
	hook.OnEpisodeComplete(context.Background(), ep)
	hook.OnToolExecuted(context.Background(), tool)

	gotRef := hook.GetReflection("sess1")
	if gotRef == nil {
		t.Fatal("expected reflection event")
	}
	if gotRef.Confidence != 0.85 {
		t.Errorf("confidence = %f, want 0.85", gotRef.Confidence)
	}

	gotEp := hook.GetEpisode("sess1")
	if gotEp == nil {
		t.Fatal("expected episode event")
	}
	if gotEp.DurationMs != 5000 {
		t.Errorf("duration = %d, want 5000", gotEp.DurationMs)
	}

	execs := hook.GetToolExecs("sess1")
	if len(execs) != 1 || execs[0].ToolName != "bash" {
		t.Errorf("tool execs = %v, want [{bash}]", execs)
	}
}

func TestEvalHook_ClearSession(t *testing.T) {
	hook := NewEvalHook()
	hook.OnReflectionComplete(context.Background(), evolution.ReflectionEvent{SessionID: "s1"})
	hook.OnEpisodeComplete(context.Background(), evolution.EpisodeEvent{SessionID: "s1"})
	hook.OnToolExecuted(context.Background(), evolution.ToolExecEvent{SessionID: "s1"})

	hook.ClearSession("s1")

	if hook.GetReflection("s1") != nil {
		t.Error("expected nil reflection after clear")
	}
	if hook.GetEpisode("s1") != nil {
		t.Error("expected nil episode after clear")
	}
	if len(hook.GetToolExecs("s1")) != 0 {
		t.Error("expected no tool execs after clear")
	}
}

func TestEvalHook_IsolatesSessions(t *testing.T) {
	hook := NewEvalHook()
	hook.OnReflectionComplete(context.Background(), evolution.ReflectionEvent{SessionID: "s1", Confidence: 0.9})
	hook.OnReflectionComplete(context.Background(), evolution.ReflectionEvent{SessionID: "s2", Confidence: 0.4})

	if hook.GetReflection("s1").Confidence != 0.9 {
		t.Error("s1 confidence should be 0.9")
	}
	if hook.GetReflection("s2").Confidence != 0.4 {
		t.Error("s2 confidence should be 0.4")
	}
	if hook.GetReflection("s3") != nil {
		t.Error("non-existent session should return nil")
	}
}

func outMsg(text string) channel.OutboundMessage {
	return channel.OutboundMessage{Text: text}
}

func TestPopulateFromObservation(t *testing.T) {
	r := &CognitiveAgentRunner{
		channel: &EvalChannel{},
	}

	obs := &agent.ObservationResult{
		Assertions: []agent.AssertionResult{
			{Check: "exit_code == 0", Passed: true},
			{Check: "no stderr", Passed: true},
			{Check: "file exists", Passed: false, Actual: "file not found"},
		},
		Observations: []agent.Observation{
			{ToolName: "bash"},
			{ToolName: "file_write"},
			{ToolName: "bash"},
		},
	}

	r.mu.Lock()
	r.lastObservation = obs
	r.mu.Unlock()

	result := &EvalResult{}
	r.populateFromObservation(result)

	if result.AssertionTotal != 3 {
		t.Errorf("AssertionTotal = %d, want 3", result.AssertionTotal)
	}
	if result.AssertionPassed != 2 {
		t.Errorf("AssertionPassed = %d, want 2", result.AssertionPassed)
	}
	wantRate := 2.0 / 3.0
	if diff := result.AssertionPassRate - wantRate; diff > 0.001 || diff < -0.001 {
		t.Errorf("AssertionPassRate = %f, want ~%f", result.AssertionPassRate, wantRate)
	}
	if len(result.ToolsUsed) != 2 {
		t.Errorf("ToolsUsed = %v, want 2 unique tools", result.ToolsUsed)
	}
}

func TestPopulateFromObservation_NilObservation(t *testing.T) {
	r := &CognitiveAgentRunner{
		channel: &EvalChannel{},
	}

	result := &EvalResult{}
	r.populateFromObservation(result)

	if result.AssertionTotal != 0 {
		t.Errorf("AssertionTotal should be 0 when no observation, got %d", result.AssertionTotal)
	}
	if result.ToolsUsed != nil {
		t.Errorf("ToolsUsed should be nil when no observation, got %v", result.ToolsUsed)
	}
}

func TestPopulateFromEvolution(t *testing.T) {
	hook := NewEvalHook()
	r := &CognitiveAgentRunner{
		channel: &EvalChannel{},
		hook:    hook,
	}

	hook.OnReflectionComplete(context.Background(), evolution.ReflectionEvent{
		SessionID:   "test-sess",
		Succeeded:   true,
		Confidence:  0.92,
		ReplanCount: 2,
		ToolsUsed:   []string{"bash"},
	})
	hook.OnEpisodeComplete(context.Background(), evolution.EpisodeEvent{
		SessionID:   "test-sess",
		Succeeded:   true,
		ReplanCount: 2,
	})

	result := &EvalResult{}
	r.populateFromEvolution(result, "test-sess")

	if !result.Success {
		t.Error("Success should be true from reflection event")
	}
	if result.Confidence != 0.92 {
		t.Errorf("Confidence = %f, want 0.92", result.Confidence)
	}
	if result.ReplanCount != 2 {
		t.Errorf("ReplanCount = %d, want 2", result.ReplanCount)
	}
}

func TestPopulateFromEvolution_NoHook(t *testing.T) {
	r := &CognitiveAgentRunner{
		channel: &EvalChannel{},
		hook:    nil,
	}

	result := &EvalResult{Success: false, Confidence: 0}
	r.populateFromEvolution(result, "any-session")

	if result.Success {
		t.Error("Success should remain false when hook is nil")
	}
	if result.Confidence != 0 {
		t.Error("Confidence should remain 0 when hook is nil")
	}
}

// mockMemoryStore is a minimal in-memory implementation of memory.Store
// used to verify InjectMemory / CleanupMemory without file I/O.
type mockMemoryStore struct {
	entries map[string]memory.Entry
}

func newMockMemoryStore() *mockMemoryStore {
	return &mockMemoryStore{entries: make(map[string]memory.Entry)}
}

func (m *mockMemoryStore) Save(_ context.Context, e memory.Entry) error {
	m.entries[e.ID] = e
	return nil
}

func (m *mockMemoryStore) Search(_ context.Context, _ memory.SearchQuery) ([]memory.SearchResult, error) {
	return nil, nil
}

func (m *mockMemoryStore) ListByScope(_ context.Context, _ memory.MemoryScope, _ string) ([]memory.Entry, error) {
	return nil, nil
}

func (m *mockMemoryStore) Update(_ context.Context, id, content string, _ int) error {
	if e, ok := m.entries[id]; ok {
		e.Content = content
		m.entries[id] = e
	}
	return nil
}

func (m *mockMemoryStore) Delete(_ context.Context, id string) error {
	delete(m.entries, id)
	return nil
}

func TestMemoryAwareRunner_InjectAndCleanup(t *testing.T) {
	store := newMockMemoryStore()

	// Wire the mock store directly; no need for a real CognitiveAgent.
	r := &CognitiveAgentRunner{
		agent:    &agent.CognitiveAgent{},
		channel:  &EvalChannel{},
		memStore: store,
	}

	ctx := context.Background()

	// Inject two entries.
	entries := []memory.Entry{
		{ID: "e1", Scope: memory.ScopeUser, UserID: "eval_user", Content: "cat is Muffin", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "e2", Scope: memory.ScopeUser, UserID: "eval_user", Content: "dog is Rex", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := r.InjectMemory(ctx, entries...); err != nil {
		t.Fatalf("InjectMemory: %v", err)
	}
	if len(store.entries) != 2 {
		t.Errorf("expected 2 entries after inject, got %d", len(store.entries))
	}

	// Cleanup one entry.
	if err := r.CleanupMemory(ctx, "e1"); err != nil {
		t.Fatalf("CleanupMemory: %v", err)
	}
	if len(store.entries) != 1 {
		t.Errorf("expected 1 entry after cleanup, got %d", len(store.entries))
	}
	if _, ok := store.entries["e2"]; !ok {
		t.Error("expected e2 to still be present")
	}
}

func TestMemoryAwareRunner_NoStore(t *testing.T) {
	// memStore is nil — should return error from InjectMemory.
	r := &CognitiveAgentRunner{
		agent:   &agent.CognitiveAgent{},
		channel: &EvalChannel{},
	}
	ctx := context.Background()
	err := r.InjectMemory(ctx, memory.Entry{ID: "x"})
	if err == nil {
		t.Error("expected error when memory store is nil")
	}
}

func TestRunSuite_SetupWithRunner(t *testing.T) {
	injected := false
	cleaned := false

	task := TaskCase{
		ID:   "mem-test",
		Goal: "test",
		SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
			injected = true
			_, ok := runner.(MemoryAwareRunner)
			if !ok {
				t.Error("runner should implement MemoryAwareRunner")
			}
			return nil
		},
		CleanupWithRunner: func(ctx context.Context, runner AgentRunner) error {
			cleaned = true
			return nil
		},
	}

	store := newMockMemoryStore()
	r := &CognitiveAgentRunner{
		agent:    &agent.CognitiveAgent{},
		channel:  &EvalChannel{},
		memStore: store,
	}

	runner := &mockRunnerWithMemory{r: r}
	_, _ = RunSuite(context.Background(), "test", []TaskCase{task}, runner)

	if !injected {
		t.Error("SetupWithRunner was not called")
	}
	if !cleaned {
		t.Error("CleanupWithRunner was not called")
	}
}

// mockRunnerWithMemory wraps CognitiveAgentRunner so RunTask doesn't need a real agent.
type mockRunnerWithMemory struct {
	r *CognitiveAgentRunner
}

func (m *mockRunnerWithMemory) RunTask(_ context.Context, task TaskCase) (*EvalResult, error) {
	return &EvalResult{TaskID: task.ID, Success: true}, nil
}

func (m *mockRunnerWithMemory) InjectMemory(ctx context.Context, entries ...memory.Entry) error {
	return m.r.InjectMemory(ctx, entries...)
}

func (m *mockRunnerWithMemory) CleanupMemory(ctx context.Context, ids ...string) error {
	return m.r.CleanupMemory(ctx, ids...)
}

func TestPopulateFromEvolution_EpisodeFallback(t *testing.T) {
	hook := NewEvalHook()
	r := &CognitiveAgentRunner{
		channel: &EvalChannel{},
		hook:    hook,
	}

	hook.OnEpisodeComplete(context.Background(), evolution.EpisodeEvent{
		SessionID:   "test-sess",
		Succeeded:   true,
		ReplanCount: 1,
	})

	result := &EvalResult{}
	r.populateFromEvolution(result, "test-sess")

	if !result.Success {
		t.Error("Success should be true from episode fallback")
	}
	if result.ReplanCount != 1 {
		t.Errorf("ReplanCount = %d, want 1", result.ReplanCount)
	}
}

// TestCaptureSnapshot_StrategyParams verifies that CaptureSnapshot correctly
// reads strategy parameter values from a StrategyOptimizer.
func TestCaptureSnapshot_StrategyParams(t *testing.T) {
	so := evolution.NewStrategyOptimizer(evolution.OptimizerConfig{
		UpdateInterval:      10,
		MaxAdjustmentPercent: 10,
	})

	// Manually drive an optimization cycle by feeding episodes.
	// We need enough episodes with replans to trigger threshold adjustment.
	for i := 0; i < 10; i++ {
		so.OnEpisodeComplete(context.Background(), evolution.EpisodeEvent{
			SessionID:   "s1",
			Succeeded:   i%2 == 0,
			ReplanCount: 2, // all have replans
			ToolSequence: []string{"bash"},
			TotalReward: 0.5,
			Timestamp:   time.Now(),
		})
	}

	strategy := so.GetStrategy()

	// Verify the fields we'll capture.
	if strategy.ReplanThreshold.Value <= 0 {
		t.Errorf("expected positive ReplanThreshold.Value, got %f", strategy.ReplanThreshold.Value)
	}

	// Simulate what CaptureSnapshot does when extracting tool priorities.
	snap := &EvolutionSnapshot{}
	snap.StrategyVersion = strategy.Version
	snap.ReplanThreshold = strategy.ReplanThreshold.Value
	snap.ReplanThresholdPrev = strategy.ReplanThreshold.Previous
	snap.ReplanThresholdReason = strategy.ReplanThreshold.Reason

	if len(strategy.ToolPriorities) > 0 {
		tp := make(map[string]float64, len(strategy.ToolPriorities))
		for tool, param := range strategy.ToolPriorities {
			tp[tool] = param.Value
		}
		snap.ToolPriorities = tp
	}

	if snap.StrategyVersion < 1 {
		t.Errorf("StrategyVersion should be >= 1, got %d", snap.StrategyVersion)
	}
	if snap.ReplanThreshold <= 0 {
		t.Errorf("ReplanThreshold should be > 0, got %f", snap.ReplanThreshold)
	}
}

// TestCaptureSnapshot_RLStats verifies computeRLStats correctly aggregates
// RL experience statistics from trajectory records.
func TestCaptureSnapshot_RLStats(t *testing.T) {
	records := []evolution.TrajectoryRecord{
		{
			SessionID:  "s1",
			Complexity: "simple",
			Tools:      []evolution.ToolRecord{{Name: "bash", Succeeded: true}},
			Reflection: evolution.ReflectionBrief{Succeeded: true, Confidence: 0.9, Reward: 0.8},
			DurationMs: 1000,
		},
		{
			SessionID:  "s2",
			Complexity: "moderate",
			Tools:      []evolution.ToolRecord{{Name: "bash", Succeeded: false}},
			Reflection: evolution.ReflectionBrief{Succeeded: false, Confidence: 0.4, Reward: -0.2},
			DurationMs: 2000,
		},
		{
			SessionID:  "s3",
			Complexity: "simple",
			Tools:      []evolution.ToolRecord{{Name: "file_write", Succeeded: true}},
			Reflection: evolution.ReflectionBrief{Succeeded: true, Confidence: 0.85, Reward: 0.7},
			DurationMs: 1500,
		},
	}

	snap := &EvolutionSnapshot{}
	snap = computeRLStats(snap, records)

	if snap.RLEpisodeCount != 3 {
		t.Errorf("RLEpisodeCount = %d, want 3", snap.RLEpisodeCount)
	}
	// 2 out of 3 succeeded (progress = 1.0)
	wantSuccessRate := 2.0 / 3.0
	if diff := snap.RLSuccessRate - wantSuccessRate; diff > 0.001 || diff < -0.001 {
		t.Errorf("RLSuccessRate = %f, want ~%f", snap.RLSuccessRate, wantSuccessRate)
	}
	// RLAvgReward should be the mean of the computed rewards.
	if snap.RLAvgReward == 0 {
		t.Error("RLAvgReward should be non-zero")
	}
	if snap.RLAvgProgress <= 0 {
		t.Error("RLAvgProgress should be positive (some tasks succeeded)")
	}
}

// TestCaptureSnapshot_RLStats_Empty verifies computeRLStats handles empty input gracefully.
func TestCaptureSnapshot_RLStats_Empty(t *testing.T) {
	snap := &EvolutionSnapshot{}
	snap = computeRLStats(snap, nil)

	if snap.RLEpisodeCount != 0 {
		t.Errorf("RLEpisodeCount should be 0 for empty input, got %d", snap.RLEpisodeCount)
	}
	if snap.RLAvgReward != 0 {
		t.Errorf("RLAvgReward should be 0 for empty input, got %f", snap.RLAvgReward)
	}
}

// TestEvoSnapshotDiff_StrategyDelta verifies Compare correctly computes
// strategy parameter deltas between two suite results.
func TestEvoSnapshotDiff_StrategyDelta(t *testing.T) {
	before := &SuiteResult{
		RunID: "run-before",
		EvoAfter: &EvolutionSnapshot{
			PreferenceCount:  5,
			StrategyVersion:  2,
			SkillDraftCount:  1,
			TrajectoryCount:  10,
			ReplanThreshold:  0.30,
			RLAvgReward:      0.50,
			RLSuccessRate:    0.60,
			ToolPriorities:   map[string]float64{"bash": 0.5, "file_write": 0.6},
		},
	}
	after := &SuiteResult{
		RunID: "run-after",
		EvoAfter: &EvolutionSnapshot{
			PreferenceCount:  8,
			StrategyVersion:  3,
			SkillDraftCount:  2,
			TrajectoryCount:  15,
			ReplanThreshold:  0.27,
			RLAvgReward:      0.65,
			RLSuccessRate:    0.75,
			ToolPriorities:   map[string]float64{"bash": 0.55, "file_write": 0.6, "http": 0.4},
		},
	}

	report := Compare(before, after)

	if report.EvoSnapshot == nil {
		t.Fatal("EvoSnapshot diff should not be nil")
	}
	diff := report.EvoSnapshot

	if diff.StrategyVersionDelta != 1 {
		t.Errorf("StrategyVersionDelta = %d, want 1", diff.StrategyVersionDelta)
	}
	if diff.TrajectoryCountDelta != 5 {
		t.Errorf("TrajectoryCountDelta = %d, want 5", diff.TrajectoryCountDelta)
	}

	wantReplanDelta := 0.27 - 0.30
	if d := diff.ReplanThresholdDelta - wantReplanDelta; d > 0.001 || d < -0.001 {
		t.Errorf("ReplanThresholdDelta = %f, want ~%f", diff.ReplanThresholdDelta, wantReplanDelta)
	}

	wantRewardDelta := 0.65 - 0.50
	if d := diff.RLAvgRewardDelta - wantRewardDelta; d > 0.001 || d < -0.001 {
		t.Errorf("RLAvgRewardDelta = %f, want ~%f", diff.RLAvgRewardDelta, wantRewardDelta)
	}

	wantSuccessDelta := 0.75 - 0.60
	if d := diff.RLSuccessRateDelta - wantSuccessDelta; d > 0.001 || d < -0.001 {
		t.Errorf("RLSuccessRateDelta = %f, want ~%f", diff.RLSuccessRateDelta, wantSuccessDelta)
	}

	// Tool priority deltas — only tools present in both snapshots.
	// "bash" and "file_write" are in both; "http" is only in after.
	if diff.ToolPriorityDeltas == nil {
		t.Fatal("ToolPriorityDeltas should not be nil")
	}
	if _, ok := diff.ToolPriorityDeltas["http"]; ok {
		t.Error("'http' should not appear in ToolPriorityDeltas (not in before)")
	}
	wantBashDelta := 0.55 - 0.5
	if d := diff.ToolPriorityDeltas["bash"] - wantBashDelta; d > 0.001 || d < -0.001 {
		t.Errorf("ToolPriorityDeltas[bash] = %f, want ~%f", diff.ToolPriorityDeltas["bash"], wantBashDelta)
	}
	// file_write delta is 0.0 — tool still present in map with zero delta.
	if _, ok := diff.ToolPriorityDeltas["file_write"]; !ok {
		t.Error("'file_write' should appear in ToolPriorityDeltas (present in both)")
	}
}

// TestFormatMarkdown_EvoSnapshotDelta verifies FormatMarkdown renders the new fields.
func TestFormatMarkdown_EvoSnapshotDelta(t *testing.T) {
	before := &SuiteResult{
		RunID: "run-a",
		EvoAfter: &EvolutionSnapshot{
			ReplanThreshold: 0.30,
			RLAvgReward:     0.50,
			RLSuccessRate:   0.60,
			ToolPriorities:  map[string]float64{"bash": 0.5},
		},
	}
	after := &SuiteResult{
		RunID: "run-b",
		EvoAfter: &EvolutionSnapshot{
			ReplanThreshold: 0.27,
			RLAvgReward:     0.65,
			RLSuccessRate:   0.75,
			ToolPriorities:  map[string]float64{"bash": 0.55},
		},
	}

	report := Compare(before, after)
	md := report.FormatMarkdown()

	if !strings.Contains(md, "Replan Threshold") {
		t.Error("FormatMarkdown should include 'Replan Threshold' row")
	}
	if !strings.Contains(md, "RL Avg Reward") {
		t.Error("FormatMarkdown should include 'RL Avg Reward' row")
	}
	if !strings.Contains(md, "RL Success Rate") {
		t.Error("FormatMarkdown should include 'RL Success Rate' row")
	}
	if !strings.Contains(md, "Tool Priority Deltas") {
		t.Error("FormatMarkdown should include 'Tool Priority Deltas' section")
	}
	if !strings.Contains(md, "bash") {
		t.Error("FormatMarkdown should include 'bash' in tool priority deltas")
	}
}
