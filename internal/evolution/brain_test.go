package evolution

import (
	"context"
	"testing"
	"time"
)

func testEpisodeEvent(tools []string, succeeded bool) EpisodeEvent {
	return EpisodeEvent{
		SessionID:    "sess-test",
		EpisodeID:    "ep-test",
		Goal:         "test goal",
		Complexity:   "medium",
		Succeeded:    succeeded,
		TotalReward:  0.8,
		ToolSequence: tools,
		ReplanCount:  1,
		DurationMs:   1000,
		Timestamp:    time.Now(),
	}
}

func TestBrain_OnEpisodeComplete(t *testing.T) {
	pref := NewPreferenceLearner(PreferenceConfig{Enabled: true, MaxPreferences: 100})
	opt := NewStrategyOptimizer(OptimizerConfig{Enabled: true, UpdateInterval: 100})
	synth := NewSkillSynthesizer(SynthesizerConfig{Enabled: false}) // disabled to avoid side-effects

	brain := NewBrain(pref, opt, synth)

	event := testEpisodeEvent([]string{"bash", "read_file"}, true)
	brain.OnEpisodeComplete(context.Background(), event)

	m := brain.GetMetrics()
	if m.TotalEpisodes != 1 {
		t.Errorf("TotalEpisodes = %d, want 1", m.TotalEpisodes)
	}
	if m.PreferenceUpdates != 1 {
		t.Errorf("PreferenceUpdates = %d, want 1", m.PreferenceUpdates)
	}
}

func TestBrain_GetMetrics(t *testing.T) {
	brain := NewBrain(nil, nil, nil)

	m := brain.GetMetrics()
	if m.TotalEpisodes != 0 {
		t.Errorf("TotalEpisodes = %d, want 0", m.TotalEpisodes)
	}
	if m.HealthScore != 1.0 {
		t.Errorf("HealthScore = %f, want 1.0", m.HealthScore)
	}
}

func TestBrain_ApplyInsights(t *testing.T) {
	pref := NewPreferenceLearner(PreferenceConfig{Enabled: true, MaxPreferences: 100})
	opt := NewStrategyOptimizer(OptimizerConfig{Enabled: true, UpdateInterval: 100, MaxAdjustmentPercent: 10})

	brain := NewBrain(pref, opt, nil)

	report := &InsightsReport{
		TotalEpisodes: 10,
		SuccessRate:   0.7,
	}
	brain.ApplyInsights(report)

	m := brain.GetMetrics()
	if m.InsightCycles != 1 {
		t.Errorf("InsightCycles = %d, want 1", m.InsightCycles)
	}
	if m.LastInsightAt.IsZero() {
		t.Error("LastInsightAt should not be zero")
	}
}

func TestBrain_DrainFeedback_SkillToPreference(t *testing.T) {
	pref := NewPreferenceLearner(PreferenceConfig{Enabled: true, MaxPreferences: 100, MinConfidence: 0.0})
	brain := NewBrain(pref, nil, nil)

	// Manually send feedback through the channel.
	brain.skillToPreference <- SkillFeedback{
		SkillName: "test_skill",
		ToolsUsed: []string{"bash"},
		Activated: true,
		AvgReward: 0.9,
	}

	brain.DrainFeedback()

	// The tool "bash" should now have a preference entry from BoostTool.
	entries := pref.GetPreferences("tool_preference")
	found := false
	for _, e := range entries {
		if e.Key == "bash" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'bash' tool_preference entry after skill feedback drain")
	}
}

func TestBrain_DrainFeedback_StrategyToSkill(t *testing.T) {
	synth := NewSkillSynthesizer(SynthesizerConfig{Enabled: false})
	brain := NewBrain(nil, nil, synth)

	brain.strategyToSkill <- StrategyFeedback{
		ToolPriorities:  map[string]float64{"bash": 0.8},
		ReplanThreshold: 0.25,
	}

	brain.DrainFeedback()

	// Verify tool priorities were set on the synthesizer.
	synth.mu.Lock()
	val, ok := synth.toolPriorities["bash"]
	synth.mu.Unlock()
	if !ok || val != 0.8 {
		t.Errorf("expected toolPriorities[bash]=0.8, got ok=%v val=%f", ok, val)
	}
}

func TestBrain_NilLoops(t *testing.T) {
	// Brain should handle nil loops gracefully.
	brain := NewBrain(nil, nil, nil)
	event := testEpisodeEvent([]string{"bash"}, true)
	brain.OnEpisodeComplete(context.Background(), event)
	brain.ApplyInsights(&InsightsReport{TotalEpisodes: 5, SuccessRate: 0.5})
	brain.DrainFeedback()

	m := brain.GetMetrics()
	if m.TotalEpisodes != 1 {
		t.Errorf("TotalEpisodes = %d, want 1", m.TotalEpisodes)
	}
}

func TestBrain_Accessors(t *testing.T) {
	pref := NewPreferenceLearner(PreferenceConfig{Enabled: true})
	opt := NewStrategyOptimizer(OptimizerConfig{Enabled: true})
	synth := NewSkillSynthesizer(SynthesizerConfig{Enabled: false})

	brain := NewBrain(pref, opt, synth)
	if brain.Preference() != pref {
		t.Error("Preference() returned wrong value")
	}
	if brain.Optimizer() != opt {
		t.Error("Optimizer() returned wrong value")
	}
	if brain.Synthesizer() != synth {
		t.Error("Synthesizer() returned wrong value")
	}
}
