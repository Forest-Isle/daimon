package evolution

import (
	"context"
	"math"
	"testing"
	"time"
)

const floatEps = 1e-9

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < floatEps
}

// helper builds a minimal successful ReflectionEvent.
func successEvent(tools []string, complexity string, replans int) ReflectionEvent {
	return ReflectionEvent{
		SessionID:   "sess-1",
		UserID:      "user-1",
		Goal:        "test goal",
		Complexity:  complexity,
		Succeeded:   true,
		Confidence:  0.9,
		ToolsUsed:   tools,
		ReplanCount: replans,
		Timestamp:   time.Now(),
	}
}

func defaultCfg() PreferenceConfig {
	return PreferenceConfig{
		Enabled:        true,
		MaxPreferences: 100,
		MinConfidence:  0.0, // accept everything by default in tests
	}
}

// ---------- Name ----------

func TestPreferenceLearner_Name(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())
	if got := pl.Name(); got != "preference_learner" {
		t.Fatalf("Name() = %q, want %q", got, "preference_learner")
	}
}

// ---------- Hook interface compliance ----------

func TestPreferenceLearner_ImplementsHook(t *testing.T) {
	var _ Hook = (*PreferenceLearner)(nil)
}

// ---------- Extraction from reflection events ----------

func TestPreferenceLearner_ExtractsToolPreferences(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())
	pl.OnReflectionComplete(context.Background(), successEvent(
		[]string{"bash", "file_write", "file_read"}, "medium", 0,
	))

	prefs := pl.GetPreferences("tool_preference")
	if len(prefs) != 3 {
		t.Fatalf("got %d tool prefs, want 3", len(prefs))
	}

	keys := map[string]bool{}
	for _, p := range prefs {
		keys[p.Key] = true
		if p.Count != 1 {
			t.Errorf("tool %q: Count = %d, want 1", p.Key, p.Count)
		}
		if !approxEqual(p.Confidence, 0.2) {
			t.Errorf("tool %q: Confidence = %.2f, want 0.20", p.Key, p.Confidence)
		}
	}
	for _, want := range []string{"bash", "file_write", "file_read"} {
		if !keys[want] {
			t.Errorf("missing tool preference for %q", want)
		}
	}
}

func TestPreferenceLearner_ExtractsComplexityHandling(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())
	pl.OnReflectionComplete(context.Background(), successEvent(nil, "high", 0))

	prefs := pl.GetPreferences("complexity_handling")
	if len(prefs) != 1 {
		t.Fatalf("got %d complexity prefs, want 1", len(prefs))
	}
	if prefs[0].Key != "high" {
		t.Errorf("Key = %q, want %q", prefs[0].Key, "high")
	}
	if prefs[0].Value != "handles_well" {
		t.Errorf("Value = %q, want %q", prefs[0].Value, "handles_well")
	}
}

func TestPreferenceLearner_ExtractsReplanTendency(t *testing.T) {
	tests := []struct {
		replans   int
		wantKey   string
		wantValue string
		wantCount int
	}{
		{0, "no_replans", "preferred", 1},
		{2, "uses_replans", "approved", 1},
		{5, "uses_replans", "approved", 1},
	}
	for _, tt := range tests {
		pl := NewPreferenceLearner(defaultCfg())
		pl.OnReflectionComplete(context.Background(), successEvent(nil, "", tt.replans))

		prefs := pl.GetPreferences("replan_tendency")
		if len(prefs) != tt.wantCount {
			t.Fatalf("replans=%d: got %d prefs, want %d", tt.replans, len(prefs), tt.wantCount)
		}
		if prefs[0].Key != tt.wantKey {
			t.Errorf("replans=%d: Key = %q, want %q", tt.replans, prefs[0].Key, tt.wantKey)
		}
		if prefs[0].Value != tt.wantValue {
			t.Errorf("replans=%d: Value = %q, want %q", tt.replans, prefs[0].Value, tt.wantValue)
		}
	}
}

func TestPreferenceLearner_ReplanCountOneIsSkipped(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())
	pl.OnReflectionComplete(context.Background(), successEvent(nil, "", 1))

	prefs := pl.GetPreferences("replan_tendency")
	if len(prefs) != 0 {
		t.Errorf("ReplanCount=1 should not produce replan_tendency, got %d", len(prefs))
	}
}

func TestPreferenceLearner_IgnoresEmptyTools(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())
	pl.OnReflectionComplete(context.Background(), successEvent(
		[]string{"bash", "", "file_read"}, "", 0,
	))

	prefs := pl.GetPreferences("tool_preference")
	if len(prefs) != 2 {
		t.Fatalf("got %d prefs, want 2 (empty tool should be skipped)", len(prefs))
	}
}

func TestPreferenceLearner_IgnoresFailedReflections(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())
	event := successEvent([]string{"bash"}, "medium", 0)
	event.Succeeded = false

	pl.OnReflectionComplete(context.Background(), event)

	if got := len(pl.GetTopPreferences(100)); got != 0 {
		t.Errorf("failed reflection recorded %d prefs, want 0", got)
	}
}

func TestPreferenceLearner_DisabledIsNoop(t *testing.T) {
	cfg := defaultCfg()
	cfg.Enabled = false
	pl := NewPreferenceLearner(cfg)

	pl.OnReflectionComplete(context.Background(), successEvent([]string{"bash"}, "low", 0))

	if got := len(pl.GetTopPreferences(100)); got != 0 {
		t.Errorf("disabled learner recorded %d prefs, want 0", got)
	}
}

func TestPreferenceLearner_IgnoresLowConfidenceEvents(t *testing.T) {
	cfg := defaultCfg()
	cfg.MinConfidence = 0.5
	pl := NewPreferenceLearner(cfg)

	event := successEvent([]string{"bash"}, "medium", 0)
	event.Confidence = 0.3 // below MinConfidence threshold

	pl.OnReflectionComplete(context.Background(), event)

	if got := len(pl.GetTopPreferences(100)); got != 0 {
		t.Errorf("low-confidence event recorded %d prefs, want 0", got)
	}
}

// ---------- Confidence accumulation ----------

func TestPreferenceLearner_ConfidenceAccumulation(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())

	event := successEvent([]string{"bash"}, "low", 0)
	for i := 0; i < 6; i++ {
		pl.OnReflectionComplete(context.Background(), event)
	}

	prefs := pl.GetPreferences("tool_preference")
	if len(prefs) != 1 {
		t.Fatalf("got %d prefs, want 1", len(prefs))
	}
	if prefs[0].Count != 6 {
		t.Errorf("Count = %d, want 6", prefs[0].Count)
	}
	// min(1.0, 6*0.2) = 1.0
	if !approxEqual(prefs[0].Confidence, 1.0) {
		t.Errorf("Confidence = %.2f, want 1.00", prefs[0].Confidence)
	}
}

func TestPreferenceLearner_ConfidenceNeverExceedsOne(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())
	event := successEvent([]string{"bash"}, "", 0)

	for i := 0; i < 20; i++ {
		pl.OnReflectionComplete(context.Background(), event)
	}

	prefs := pl.GetPreferences("tool_preference")
	if !approxEqual(prefs[0].Confidence, 1.0) {
		t.Errorf("Confidence = %.2f, want 1.00 (clamped)", prefs[0].Confidence)
	}
}

// ---------- Max preferences cap and eviction ----------

func TestPreferenceLearner_EvictsLowestConfidence(t *testing.T) {
	cfg := defaultCfg()
	cfg.MaxPreferences = 3
	pl := NewPreferenceLearner(cfg)

	// Record "bash" 3 times → confidence 0.6
	for i := 0; i < 3; i++ {
		pl.OnReflectionComplete(context.Background(), successEvent([]string{"bash"}, "", 0))
	}
	// Record "curl" 2 times → confidence 0.4
	for i := 0; i < 2; i++ {
		pl.OnReflectionComplete(context.Background(), successEvent([]string{"curl"}, "", 0))
	}
	// Record "file" 1 time → confidence 0.2  (3 entries total: bash, curl, replan_tendency:low)
	// Actually we also get replan_tendency entries. Let's be explicit.

	// Reset for cleaner test.
	pl = NewPreferenceLearner(cfg)

	// Directly use recordPreference to avoid replan_tendency noise.
	pl.recordPreference("tool_preference", "bash", "bash")
	pl.recordPreference("tool_preference", "bash", "bash")
	pl.recordPreference("tool_preference", "bash", "bash") // count=3, conf=0.6

	pl.recordPreference("tool_preference", "curl", "curl")
	pl.recordPreference("tool_preference", "curl", "curl") // count=2, conf=0.4

	pl.recordPreference("tool_preference", "file", "file") // count=1, conf=0.2
	// Now we have 3 entries, exactly at cap.

	// Add a 4th → triggers eviction of lowest (file, conf=0.2).
	pl.recordPreference("tool_preference", "http", "http") // count=1, conf=0.2
	// After adding http (4 entries), evict lowest. file and http both have 0.2,
	// but file was seen earlier → file is evicted.

	pl.mu.RLock()
	_, hasFile := pl.preferences["tool_preference:file"]
	_, hasHTTP := pl.preferences["tool_preference:http"]
	_, hasBash := pl.preferences["tool_preference:bash"]
	total := len(pl.preferences)
	pl.mu.RUnlock()

	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if hasFile {
		t.Error("file should have been evicted (lowest confidence, oldest)")
	}
	if !hasHTTP {
		t.Error("http should be present (just added)")
	}
	if !hasBash {
		t.Error("bash should be present (highest confidence)")
	}
}

// ---------- GetPreferences sorting ----------

func TestPreferenceLearner_GetPreferencesSortedByConfidence(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())

	// bash: 4 observations → 0.8, curl: 2 → 0.4, file: 1 → 0.2
	for i := 0; i < 4; i++ {
		pl.recordPreference("tool_preference", "bash", "bash")
	}
	for i := 0; i < 2; i++ {
		pl.recordPreference("tool_preference", "curl", "curl")
	}
	pl.recordPreference("tool_preference", "file", "file")

	prefs := pl.GetPreferences("tool_preference")
	if len(prefs) != 3 {
		t.Fatalf("got %d prefs, want 3", len(prefs))
	}
	if prefs[0].Key != "bash" || prefs[1].Key != "curl" || prefs[2].Key != "file" {
		t.Errorf("order = [%s, %s, %s], want [bash, curl, file]",
			prefs[0].Key, prefs[1].Key, prefs[2].Key)
	}
}

func TestPreferenceLearner_GetPreferencesFiltersByCategory(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())

	pl.recordPreference("tool_preference", "bash", "bash")
	pl.recordPreference("complexity_handling", "low", "low")

	toolPrefs := pl.GetPreferences("tool_preference")
	if len(toolPrefs) != 1 || toolPrefs[0].Category != "tool_preference" {
		t.Errorf("expected 1 tool_preference, got %d", len(toolPrefs))
	}

	complexPrefs := pl.GetPreferences("complexity_handling")
	if len(complexPrefs) != 1 || complexPrefs[0].Category != "complexity_handling" {
		t.Errorf("expected 1 complexity_handling, got %d", len(complexPrefs))
	}

	none := pl.GetPreferences("nonexistent")
	if len(none) != 0 {
		t.Errorf("expected 0 prefs for nonexistent category, got %d", len(none))
	}
}

func TestPreferenceLearner_GetPreferencesRespectsMinConfidence(t *testing.T) {
	cfg := defaultCfg()
	cfg.MinConfidence = 0.5
	pl := NewPreferenceLearner(cfg)

	// 1 observation → confidence 0.2 (below 0.5 threshold)
	pl.recordPreference("tool_preference", "bash", "bash")

	prefs := pl.GetPreferences("tool_preference")
	if len(prefs) != 0 {
		t.Fatalf("expected 0 prefs (below min confidence), got %d", len(prefs))
	}

	// Add 2 more → count=3, confidence=0.6 (above threshold)
	pl.recordPreference("tool_preference", "bash", "bash")
	pl.recordPreference("tool_preference", "bash", "bash")

	prefs = pl.GetPreferences("tool_preference")
	if len(prefs) != 1 {
		t.Fatalf("expected 1 pref (above min confidence), got %d", len(prefs))
	}
	if !approxEqual(prefs[0].Confidence, 0.6) {
		t.Errorf("Confidence = %.2f, want 0.60", prefs[0].Confidence)
	}
}

// ---------- GetTopPreferences ----------

func TestPreferenceLearner_GetTopPreferences(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())

	// Create entries across categories with different confidence levels.
	for i := 0; i < 5; i++ {
		pl.recordPreference("tool_preference", "bash", "bash") // conf=1.0
	}
	for i := 0; i < 3; i++ {
		pl.recordPreference("complexity_handling", "medium", "medium") // conf=0.6
	}
	pl.recordPreference("replan_tendency", "low", "low") // conf=0.2

	top2 := pl.GetTopPreferences(2)
	if len(top2) != 2 {
		t.Fatalf("got %d, want 2", len(top2))
	}
	if top2[0].Confidence < top2[1].Confidence {
		t.Errorf("not sorted: %.2f < %.2f", top2[0].Confidence, top2[1].Confidence)
	}
	if top2[0].Key != "bash" {
		t.Errorf("top pref Key = %q, want %q", top2[0].Key, "bash")
	}
}

func TestPreferenceLearner_GetTopPreferencesClamps(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())
	pl.recordPreference("tool_preference", "bash", "bash")

	all := pl.GetTopPreferences(999)
	if len(all) != 1 {
		t.Errorf("got %d, want 1 (clamped to available)", len(all))
	}
}

func TestPreferenceLearner_GetTopPreferencesEmpty(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())
	if got := pl.GetTopPreferences(5); len(got) != 0 {
		t.Errorf("got %d, want 0 on empty learner", len(got))
	}
}

// ---------- No-op methods ----------

func TestPreferenceLearner_OnEpisodeCompleteIsNoop(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())
	pl.OnEpisodeComplete(context.Background(), EpisodeEvent{})
	if got := len(pl.GetTopPreferences(100)); got != 0 {
		t.Errorf("OnEpisodeComplete should be noop, got %d prefs", got)
	}
}

func TestPreferenceLearner_OnToolExecutedIsNoop(t *testing.T) {
	pl := NewPreferenceLearner(defaultCfg())
	pl.OnToolExecuted(context.Background(), ToolExecEvent{})
	if got := len(pl.GetTopPreferences(100)); got != 0 {
		t.Errorf("OnToolExecuted should be noop, got %d prefs", got)
	}
}
