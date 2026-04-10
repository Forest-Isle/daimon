package evolution

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func defaultTestCfg() OptimizerConfig {
	return OptimizerConfig{
		Enabled:              true,
		UpdateInterval:       10,
		MaxAdjustmentPercent: 10,
		RevertThreshold:      0.15,
		StrategyFile:         "",
	}
}

func makeEvent(succeeded bool, replanCount int, tools []string) EpisodeEvent {
	return EpisodeEvent{
		SessionID:    "s1",
		EpisodeID:    "e1",
		Goal:         "test",
		Complexity:   "medium",
		Succeeded:    succeeded,
		TotalReward:  1.0,
		ToolSequence: tools,
		ReplanCount:  replanCount,
		DurationMs:   500,
		Timestamp:    time.Now(),
	}
}

// feedEpisodes pushes n episodes into the optimizer and returns it.
func feedEpisodes(t *testing.T, cfg OptimizerConfig, events []EpisodeEvent) *StrategyOptimizer {
	t.Helper()
	opt := NewStrategyOptimizer(cfg)
	ctx := context.Background()
	for _, ev := range events {
		opt.OnEpisodeComplete(ctx, ev)
	}
	return opt
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestOptimizer_Name(t *testing.T) {
	opt := NewStrategyOptimizer(defaultTestCfg())
	if got := opt.Name(); got != "strategy_optimizer" {
		t.Fatalf("Name() = %q, want %q", got, "strategy_optimizer")
	}
}

func TestOptimizer_NoOps(t *testing.T) {
	opt := NewStrategyOptimizer(defaultTestCfg())
	ctx := context.Background()

	// Must not panic.
	opt.OnReflectionComplete(ctx, ReflectionEvent{})
	opt.OnToolExecuted(ctx, ToolExecEvent{})
}

func TestOptimizer_StatsAccumulation(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.UpdateInterval = 100 // avoid triggering optimize mid-test

	events := make([]EpisodeEvent, 15)
	for i := range events {
		events[i] = makeEvent(i%2 == 0, i%3, []string{"toolA"})
	}

	opt := feedEpisodes(t, cfg, events)

	opt.mu.Lock()
	defer opt.mu.Unlock()

	if got := len(opt.episodes); got != 15 {
		t.Fatalf("episodes = %d, want 15", got)
	}
	if opt.episodeCount != 15 {
		t.Fatalf("episodeCount = %d, want 15", opt.episodeCount)
	}
}

func TestOptimizer_RollingWindowCap(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.UpdateInterval = 9999

	events := make([]EpisodeEvent, 130)
	for i := range events {
		events[i] = makeEvent(true, 0, []string{"t"})
		events[i].Complexity = "high"
	}

	opt := feedEpisodes(t, cfg, events)

	opt.mu.Lock()
	defer opt.mu.Unlock()

	if got := len(opt.episodes); got != maxEpisodeWindow {
		t.Fatalf("rolling window = %d, want %d", got, maxEpisodeWindow)
	}
}

func TestOptimizer_ThresholdLowered(t *testing.T) {
	// When replans are highly effective (>70%), threshold should decrease.
	cfg := defaultTestCfg()
	cfg.UpdateInterval = 10

	var events []EpisodeEvent
	// 8 episodes WITH replans, 7 succeed → 87.5% effectiveness
	for i := 0; i < 8; i++ {
		events = append(events, makeEvent(i < 7, 1, []string{"t1"}))
	}
	// 2 episodes WITHOUT replans, 0 succeed → 0% no-replan rate
	for i := 0; i < 2; i++ {
		events = append(events, makeEvent(false, 0, []string{"t1"}))
	}

	opt := feedEpisodes(t, cfg, events)

	opt.mu.Lock()
	defer opt.mu.Unlock()

	if opt.strategy.ReplanThreshold.Value >= defaultReplanThreshold {
		t.Fatalf("threshold should have decreased: got %.4f, initial was %.4f",
			opt.strategy.ReplanThreshold.Value, defaultReplanThreshold)
	}
	if opt.strategy.ReplanThreshold.Previous != defaultReplanThreshold {
		t.Fatalf("Previous not recorded: got %.4f", opt.strategy.ReplanThreshold.Previous)
	}
}

func TestOptimizer_ThresholdRaised(t *testing.T) {
	// When replans are ineffective (<30%), threshold should increase.
	cfg := defaultTestCfg()
	cfg.UpdateInterval = 10

	var events []EpisodeEvent
	// 8 episodes WITH replans, only 1 succeeds → 12.5% effectiveness
	for i := 0; i < 8; i++ {
		events = append(events, makeEvent(i == 0, 1, []string{"t1"}))
	}
	// 2 episodes WITHOUT replans, 2 succeed → 100% no-replan rate
	for i := 0; i < 2; i++ {
		events = append(events, makeEvent(true, 0, []string{"t1"}))
	}

	opt := feedEpisodes(t, cfg, events)

	opt.mu.Lock()
	defer opt.mu.Unlock()

	if opt.strategy.ReplanThreshold.Value <= defaultReplanThreshold {
		t.Fatalf("threshold should have increased: got %.4f, initial was %.4f",
			opt.strategy.ReplanThreshold.Value, defaultReplanThreshold)
	}
}

func TestOptimizer_BoundsEnforcement(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.MaxAdjustmentPercent = 200 // extreme to force clamping
	cfg.UpdateInterval = 10
	cfg.RevertThreshold = 1.0 // disable revert for this test

	t.Run("lower_bound", func(t *testing.T) {
		opt := NewStrategyOptimizer(cfg)
		opt.strategy.ReplanThreshold.Value = 0.005 // already near minimum
		ctx := context.Background()

		// All episodes have effective replans → want to lower further.
		for i := 0; i < 10; i++ {
			succeeded := i < 8
			replan := 1
			if i == 9 {
				replan = 0
			}
			opt.OnEpisodeComplete(ctx, makeEvent(succeeded, replan, []string{"t"}))
		}

		opt.mu.Lock()
		defer opt.mu.Unlock()

		if opt.strategy.ReplanThreshold.Value < minReplanThreshold {
			t.Fatalf("below lower bound: %.6f", opt.strategy.ReplanThreshold.Value)
		}
	})

	t.Run("upper_bound", func(t *testing.T) {
		opt := NewStrategyOptimizer(cfg)
		opt.strategy.ReplanThreshold.Value = 0.98 // already near maximum
		ctx := context.Background()

		// All episodes have ineffective replans → want to raise further.
		for i := 0; i < 10; i++ {
			replan := 1
			if i == 9 {
				replan = 0 // need at least one without replan
			}
			opt.OnEpisodeComplete(ctx, makeEvent(i == 9, replan, []string{"t"}))
		}

		opt.mu.Lock()
		defer opt.mu.Unlock()

		if opt.strategy.ReplanThreshold.Value > maxReplanThreshold {
			t.Fatalf("above upper bound: %.6f", opt.strategy.ReplanThreshold.Value)
		}
	})

	t.Run("tool_priority_bounds", func(t *testing.T) {
		opt := NewStrategyOptimizer(cfg)
		opt.strategy.ToolPriorities["x"] = StrategyParam{Value: 0.99}
		ctx := context.Background()

		// Tool "x" used in all episodes, all succeed → boost.
		for i := 0; i < 10; i++ {
			replan := 0
			opt.OnEpisodeComplete(ctx, makeEvent(true, replan, []string{"x"}))
		}

		opt.mu.Lock()
		defer opt.mu.Unlock()

		if v := opt.strategy.ToolPriorities["x"].Value; v > maxToolPriority {
			t.Fatalf("tool priority exceeds upper bound: %.6f", v)
		}
	})
}

func TestOptimizer_ToolPriorityAdjustment(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.UpdateInterval = 10

	var events []EpisodeEvent
	// 5 episodes using "good_tool", all succeed (100% rate → boost).
	for i := 0; i < 5; i++ {
		events = append(events, makeEvent(true, 0, []string{"good_tool"}))
	}
	// 5 episodes using "bad_tool", all fail (0% rate → reduce).
	for i := 0; i < 5; i++ {
		events = append(events, makeEvent(false, 0, []string{"bad_tool"}))
	}

	opt := feedEpisodes(t, cfg, events)

	opt.mu.Lock()
	defer opt.mu.Unlock()

	if p, ok := opt.strategy.ToolPriorities["good_tool"]; !ok {
		t.Fatal("good_tool missing from priorities")
	} else if p.Value <= defaultToolPriority {
		t.Fatalf("good_tool should be boosted: got %.4f", p.Value)
	}

	if p, ok := opt.strategy.ToolPriorities["bad_tool"]; !ok {
		t.Fatal("bad_tool missing from priorities")
	} else if p.Value >= defaultToolPriority {
		t.Fatalf("bad_tool should be reduced: got %.4f", p.Value)
	}
}

func TestOptimizer_RevertOnSuccessDrop(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.UpdateInterval = 10
	cfg.RevertThreshold = 0.05 // 5 %

	opt := NewStrategyOptimizer(cfg)
	ctx := context.Background()

	// Cycle 1: 90% success rate to establish baseline.
	for i := 0; i < 10; i++ {
		opt.OnEpisodeComplete(ctx, makeEvent(i < 9, 0, []string{"t"}))
	}

	opt.mu.Lock()
	firstRate := opt.strategy.Metrics.OverallSuccessRate
	savedThreshold := opt.strategy.ReplanThreshold.Value
	opt.mu.Unlock()

	if firstRate < 0.85 {
		t.Fatalf("first cycle success rate unexpectedly low: %.2f", firstRate)
	}

	// Cycle 2: feed mostly failures to trigger a big drop.
	for i := 0; i < 10; i++ {
		opt.OnEpisodeComplete(ctx, makeEvent(i < 3, 0, []string{"t"}))
	}

	opt.mu.Lock()
	defer opt.mu.Unlock()

	// The revert should restore the previous success rate.
	if opt.strategy.Metrics.OverallSuccessRate != firstRate {
		t.Fatalf("expected reverted success rate %.2f, got %.2f",
			firstRate, opt.strategy.Metrics.OverallSuccessRate)
	}
	// The threshold should have been restored.
	if opt.strategy.ReplanThreshold.Value != savedThreshold {
		t.Fatalf("expected reverted threshold %.4f, got %.4f",
			savedThreshold, opt.strategy.ReplanThreshold.Value)
	}
}

func TestOptimizer_YAMLRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "strategy.yaml")

	opt := NewStrategyOptimizer(defaultTestCfg())
	opt.strategy = &Strategy{
		Version:   7,
		UpdatedAt: time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		ReplanThreshold: StrategyParam{
			Value:    0.42,
			Previous: 0.50,
			Reason:   "replan effective",
		},
		ToolPriorities: map[string]StrategyParam{
			"browser": {Value: 0.85, Previous: 0.75, Reason: "highly successful"},
			"calc":    {Value: 0.30, Previous: 0.40, Reason: "underperforming"},
		},
		Metrics: MetricsSnapshot{
			OverallSuccessRate: 0.78,
			EpisodesAnalyzed:   60,
		},
	}

	if err := opt.SaveStrategy(path); err != nil {
		t.Fatalf("SaveStrategy: %v", err)
	}

	loaded := NewStrategyOptimizer(defaultTestCfg())
	if err := loaded.LoadStrategy(path); err != nil {
		t.Fatalf("LoadStrategy: %v", err)
	}

	s := loaded.strategy
	assertEqual(t, "Version", s.Version, 7)
	assertFloat(t, "ReplanThreshold.Value", s.ReplanThreshold.Value, 0.42)
	assertFloat(t, "ReplanThreshold.Previous", s.ReplanThreshold.Previous, 0.50)
	assertEqual(t, "ReplanThreshold.Reason", s.ReplanThreshold.Reason, "replan effective")

	if len(s.ToolPriorities) != 2 {
		t.Fatalf("ToolPriorities len = %d, want 2", len(s.ToolPriorities))
	}
	assertFloat(t, "browser.Value", s.ToolPriorities["browser"].Value, 0.85)
	assertFloat(t, "calc.Value", s.ToolPriorities["calc"].Value, 0.30)

	assertFloat(t, "OverallSuccessRate", s.Metrics.OverallSuccessRate, 0.78)
	assertEqual(t, "EpisodesAnalyzed", s.Metrics.EpisodesAnalyzed, 60)
}

func TestOptimizer_SaveCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	nested := filepath.Join(tmpDir, "a", "b", "strategy.yaml")

	opt := NewStrategyOptimizer(defaultTestCfg())
	if err := opt.SaveStrategy(nested); err != nil {
		t.Fatalf("SaveStrategy with nested dir: %v", err)
	}
	if err := opt.LoadStrategy(nested); err != nil {
		t.Fatalf("LoadStrategy after nested save: %v", err)
	}
}

func TestOptimizer_OptimizeTriggersOnInterval(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.UpdateInterval = 5
	cfg.RevertThreshold = 1.0

	opt := NewStrategyOptimizer(cfg)
	ctx := context.Background()

	// Feed 4 episodes — no optimisation yet.
	for i := 0; i < 4; i++ {
		opt.OnEpisodeComplete(ctx, makeEvent(true, 0, []string{"t"}))
	}

	opt.mu.Lock()
	v1 := opt.strategy.Version
	opt.mu.Unlock()

	if v1 != 1 {
		t.Fatalf("version should still be 1 after 4 episodes, got %d", v1)
	}

	// 5th episode should trigger optimisation → version bumps.
	opt.OnEpisodeComplete(ctx, makeEvent(true, 0, []string{"t"}))

	opt.mu.Lock()
	v2 := opt.strategy.Version
	opt.mu.Unlock()

	if v2 <= v1 {
		t.Fatalf("version should have bumped after interval, got %d", v2)
	}
}

// ---------- BuildPromptSection ----------

func TestOptimizer_BuildPromptSectionEmpty(t *testing.T) {
	opt := NewStrategyOptimizer(defaultTestCfg())
	if s := opt.BuildPromptSection(); s != "" {
		t.Errorf("expected empty section for fresh optimizer (version=1), got %q", s)
	}
}

func TestOptimizer_BuildPromptSectionAfterOptimization(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.UpdateInterval = 2
	opt := NewStrategyOptimizer(cfg)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		opt.OnEpisodeComplete(ctx, EpisodeEvent{
			Succeeded:    true,
			TotalReward:  1.0,
			ToolSequence: []string{"bash"},
			ReplanCount:  1,
		})
	}

	section := opt.BuildPromptSection()
	if section == "" {
		t.Fatal("expected non-empty section after optimization")
	}
	if !strings.Contains(section, "STRATEGY HINTS") {
		t.Error("missing header")
	}
	if !strings.Contains(section, "Replan threshold") {
		t.Error("missing replan threshold info")
	}
	if !strings.Contains(section, "success rate") {
		t.Error("missing success rate info")
	}
}

// ---------- GetStrategy ----------

func TestOptimizer_GetStrategyReturnsCopy(t *testing.T) {
	opt := NewStrategyOptimizer(defaultTestCfg())
	s := opt.GetStrategy()
	s.ReplanThreshold.Value = 999.0
	s.ToolPriorities["test"] = StrategyParam{Value: 1.0}

	original := opt.GetStrategy()
	if original.ReplanThreshold.Value == 999.0 {
		t.Error("GetStrategy should return a copy, not a reference")
	}
	if _, ok := original.ToolPriorities["test"]; ok {
		t.Error("GetStrategy ToolPriorities should be a copy")
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func assertEqual[T comparable](t *testing.T, field string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

func assertFloat(t *testing.T, field string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %f, want %f", field, got, want)
	}
}
