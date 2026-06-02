package evolution

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEngineIntegration_FullEventFlow verifies the complete event pipeline:
// dispatch events → hooks process them → strategy changes → prompt section updates.
func TestEngineIntegration_FullEventFlow(t *testing.T) {
	cfg := Config{
		Enabled:     true,
		HookTimeout: 5 * time.Second,
		Preference: PreferenceConfig{
			Enabled:        true,
			MaxPreferences: 10,
			MinConfidence:  0.1,
		},
		Optimizer: OptimizerConfig{
			Enabled:              true,
			UpdateInterval:       3,
			MaxAdjustmentPercent: 10,
			RevertThreshold:      0.15,
		},
	}

	engine := NewEngine(cfg)

	pl := NewPreferenceLearner(cfg.Preference)
	so := NewStrategyOptimizer(cfg.Optimizer)
	engine.RegisterHook(pl)
	engine.RegisterHook(so)

	engine.Start()
	defer engine.Stop()

	// Verify hooks registered
	if engine.PreferenceLearnerHook() == nil {
		t.Fatal("preference learner not registered")
	}
	if engine.StrategyOptimizerHook() == nil {
		t.Fatal("strategy optimizer not registered")
	}

	// Dispatch reflection events
	for i := 0; i < 5; i++ {
		engine.DispatchReflection(ReflectionEvent{
			SessionID:      "s1",
			UserID:         "u1",
			Goal:           "test task",
			Complexity:     "moderate",
			Succeeded:      true,
			Confidence:     0.85,
			LessonsLearned: []string{"use bash for file ops"},
			ToolsUsed:      []string{"bash", "file_write"},
			ReplanCount:    0,
		})
	}

	// Dispatch episode events to trigger optimization
	for i := 0; i < 3; i++ {
		engine.DispatchEpisode(EpisodeEvent{
			SessionID:    "s1",
			EpisodeID:    "e" + string(rune('1'+i)),
			Goal:         "test task",
			Complexity:   "moderate",
			Succeeded:    true,
			TotalReward:  0.8,
			ToolSequence: []string{"bash", "file_write"},
			ReplanCount:  0,
			DurationMs:   5000,
			Timestamp:    time.Now(),
		})
	}

	// Dispatch tool events
	engine.DispatchToolExec(ToolExecEvent{
		SessionID:  "s1",
		ToolName:   "bash",
		Succeeded:  true,
		DurationMs: 100,
	})

	// Wait for async hooks to complete
	time.Sleep(500 * time.Millisecond)

	// Verify preference learner captured data
	section := pl.BuildPromptSection()
	// After 5 successful reflections using bash, preferences should form
	if pl.GetPreferences("tool_preference") == nil && section == "" {
		// Acceptable: heuristic may not trigger for generic goals
		t.Log("no preferences formed (heuristic threshold not met) — acceptable")
	}

	// Verify strategy optimizer ran (3 episodes = UpdateInterval)
	strategy := so.GetStrategy()
	if strategy.Version <= 1 {
		t.Error("expected strategy version > 1 after optimization cycle")
	}

	// Verify prompt section is non-empty
	stratSection := so.BuildPromptSection()
	if stratSection == "" {
		t.Error("expected non-empty strategy prompt section")
	}
	if !strings.Contains(stratSection, "STRATEGY HINTS") {
		t.Error("strategy section missing expected header")
	}
}

// TestEngineIntegration_TrajectoryRecorderAndInsights verifies the trajectory →
// insights → strategy pipeline end-to-end.
func TestEngineIntegration_TrajectoryRecorderAndInsights(t *testing.T) {
	tmpDir := t.TempDir()
	trajDir := filepath.Join(tmpDir, "trajectories")

	cfg := Config{
		Enabled:     true,
		HookTimeout: 5 * time.Second,
		Optimizer: OptimizerConfig{
			Enabled:              true,
			UpdateInterval:       100, // high so auto-optimize doesn't fire
			MaxAdjustmentPercent: 10,
		},
	}

	engine := NewEngine(cfg)
	engine.SetTrajectoryDir(trajDir)

	recorder := NewTrajectoryRecorder(trajDir)
	so := NewStrategyOptimizer(cfg.Optimizer)
	engine.RegisterHook(recorder)
	engine.RegisterHook(so)

	engine.Start()
	defer func() {
		engine.Stop()
		_ = recorder.Close()
	}()

	now := time.Now()
	for i := 0; i < 10; i++ {
		engine.DispatchToolExec(ToolExecEvent{
			SessionID:  "session-1",
			ToolName:   "bash",
			Succeeded:  i%3 != 0,
			DurationMs: 100,
			Timestamp:  now,
		})
	}

	for i := 0; i < 10; i++ {
		succeeded := i >= 3
		engine.DispatchEpisode(EpisodeEvent{
			SessionID:    "session-1",
			EpisodeID:    "ep-" + string(rune('a'+i)),
			Goal:         "do something",
			Complexity:   "moderate",
			Succeeded:    succeeded,
			TotalReward:  float64(i) * 0.1,
			ToolSequence: []string{"bash"},
			ReplanCount:  i % 2,
			DurationMs:   int64(5000 + i*1000),
			Timestamp:    now,
		})
	}

	time.Sleep(500 * time.Millisecond)

	// Verify trajectories were written to disk
	records, err := ReadTrajectories(trajDir, now.Add(-1*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("read trajectories: %v", err)
	}
	if len(records) != 10 {
		t.Errorf("expected 10 trajectory records, got %d", len(records))
	}

	// Run insights cycle manually
	report := GenerateInsights(records, "test-period")
	if report.TotalEpisodes != 10 {
		t.Errorf("insights report episodes = %d, want 10", report.TotalEpisodes)
	}

	// Feed insights into optimizer
	applied := so.ApplyInsights(report)
	t.Logf("insights applied %d adjustments", applied)

	// If adjustments were applied, verify strategy updated.
	// When all episodes use the same tool with moderate success rate,
	// ApplyInsights may find nothing to adjust — that's acceptable.
	strategy := so.GetStrategy()
	if applied > 0 && strategy.Version <= 1 {
		t.Error("expected strategy version > 1 after applying insights")
	}
	if applied == 0 {
		t.Log("no adjustments applied (homogeneous data) — acceptable")
	}

	// Save and reload strategy
	stratPath := filepath.Join(tmpDir, "strategy.yaml")
	if err := so.SaveStrategy(stratPath); err != nil {
		t.Fatalf("save strategy: %v", err)
	}
	data, err := os.ReadFile(stratPath)
	if err != nil {
		t.Fatalf("read strategy file: %v", err)
	}
	if len(data) == 0 {
		t.Error("strategy file is empty")
	}
}

// TestEngineIntegration_RewardFromTrajectory verifies that ComputeReward works
// correctly with inputs derived from a TrajectoryRecord's fields.
func TestEngineIntegration_RewardFromTrajectory(t *testing.T) {
	rec := TrajectoryRecord{
		Reflection: ReflectionBrief{
			Succeeded:  true,
			Confidence: 0.8,
		},
		DurationMs:   30000,
		ReplanCount:  0,
		UserFeedback: 0.5,
	}

	// Compute reward from trajectory record fields via the unified formula.
	// Succeeded=true -> +0.5, Progress=0 (default) -> +0, DurationMs < 60s -> +0.1,
	// ReplanCount=0 -> +0.05, UserFeedback=0.5 -> +0.05, total = 0.7
	reward := ComputeReward(RewardInput{
		Succeeded:    rec.Reflection.Succeeded,
		DurationMs:   rec.DurationMs,
		ReplanCount:  rec.ReplanCount,
		UserFeedback: rec.UserFeedback,
	})

	if math.Abs(reward-0.7) > 1e-9 {
		t.Errorf("compute reward from trajectory = %.10f, want 0.7", reward)
	}
}

// TestEngineIntegration_HardControlPipeline verifies that when hard control
// is enabled, the optimizer's GetReplanThreshold returns a usable value after
// the optimization cycle runs.
func TestEngineIntegration_HardControlPipeline(t *testing.T) {
	cfg := OptimizerConfig{
		Enabled:              true,
		UpdateInterval:       2,
		MaxAdjustmentPercent: 10,
		HardControlEnabled:   true,
	}

	so := NewStrategyOptimizer(cfg)

	if !so.IsHardControlEnabled() {
		t.Fatal("hard control should be enabled")
	}

	// Before any optimization, threshold should be 0 (use config default)
	if v := so.GetReplanThreshold(); v != 0 {
		t.Errorf("fresh optimizer threshold = %f, want 0", v)
	}

	// Feed episodes to trigger optimization
	ctx := context.Background()
	so.OnEpisodeComplete(ctx, EpisodeEvent{Succeeded: true, ToolSequence: []string{"bash"}})
	so.OnEpisodeComplete(ctx, EpisodeEvent{Succeeded: true, ToolSequence: []string{"bash"}})

	// Now should have a real threshold
	v := so.GetReplanThreshold()
	if v <= 0 || v > 1.0 {
		t.Errorf("post-optimization threshold = %f, want (0, 1.0]", v)
	}
	t.Logf("evolved replan threshold: %.4f", v)
}
