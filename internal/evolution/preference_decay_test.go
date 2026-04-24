package evolution

import (
	"testing"
	"time"
)

func TestDecayPreferences_ExponentialDecay(t *testing.T) {
	pl := NewPreferenceLearner(PreferenceConfig{
		Enabled:        true,
		MaxPreferences: 100,
		MinConfidence:  0.0,
	})

	// Manually insert a preference with known confidence and LastSeen.
	now := time.Now()
	past := now.Add(-48 * time.Hour) // 2 days ago

	pl.mu.Lock()
	pl.preferences["tool_preference:bash"] = &PreferenceEntry{
		Category:   "tool_preference",
		Key:        "bash",
		Value:      "preferred",
		Confidence: 1.0,
		Count:      5,
		LastSeen:   past,
	}
	pl.mu.Unlock()

	halfLife := 48 * time.Hour // exactly one half-life has passed
	removed := pl.DecayPreferences(now, halfLife)

	if removed != 0 {
		t.Errorf("expected 0 removed (confidence should be ~0.5), got %d", removed)
	}

	pl.mu.RLock()
	entry := pl.preferences["tool_preference:bash"]
	pl.mu.RUnlock()

	if entry == nil {
		t.Fatal("entry should still exist")
	}

	// After exactly one half-life, confidence should be ~0.5.
	if entry.Confidence < 0.45 || entry.Confidence > 0.55 {
		t.Errorf("expected confidence ~0.5, got %f", entry.Confidence)
	}
}

func TestDecayPreferences_RemoveBelowThreshold(t *testing.T) {
	pl := NewPreferenceLearner(PreferenceConfig{
		Enabled:        true,
		MaxPreferences: 100,
		MinConfidence:  0.0,
	})

	now := time.Now()
	veryOld := now.Add(-30 * 24 * time.Hour) // 30 days ago

	pl.mu.Lock()
	pl.preferences["tool_preference:old_tool"] = &PreferenceEntry{
		Category:   "tool_preference",
		Key:        "old_tool",
		Value:      "preferred",
		Confidence: 0.1, // low confidence + very old → will drop below 0.05
		Count:      1,
		LastSeen:   veryOld,
	}
	pl.mu.Unlock()

	halfLife := 24 * time.Hour // 30 half-lives → factor ≈ 0
	removed := pl.DecayPreferences(now, halfLife)

	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	pl.mu.RLock()
	_, exists := pl.preferences["tool_preference:old_tool"]
	pl.mu.RUnlock()

	if exists {
		t.Error("entry should have been removed")
	}
}

func TestDecayPreferences_NilLearner(t *testing.T) {
	var pl *PreferenceLearner
	removed := pl.DecayPreferences(time.Now(), 24*time.Hour)
	if removed != 0 {
		t.Errorf("expected 0, got %d", removed)
	}
}

func TestDecayPreferences_ZeroHalfLife(t *testing.T) {
	pl := NewPreferenceLearner(PreferenceConfig{Enabled: true, MaxPreferences: 100})
	removed := pl.DecayPreferences(time.Now(), 0)
	if removed != 0 {
		t.Errorf("expected 0 with zero halfLife, got %d", removed)
	}
}

func TestDecayPreferences_FutureLastSeen(t *testing.T) {
	pl := NewPreferenceLearner(PreferenceConfig{
		Enabled:        true,
		MaxPreferences: 100,
		MinConfidence:  0.0,
	})

	now := time.Now()
	future := now.Add(1 * time.Hour)

	pl.mu.Lock()
	pl.preferences["tool_preference:future"] = &PreferenceEntry{
		Category:   "tool_preference",
		Key:        "future",
		Value:      "preferred",
		Confidence: 0.8,
		Count:      3,
		LastSeen:   future,
	}
	pl.mu.Unlock()

	removed := pl.DecayPreferences(now, 24*time.Hour)
	if removed != 0 {
		t.Errorf("expected 0 removed for future entry, got %d", removed)
	}

	// Confidence should be unchanged.
	pl.mu.RLock()
	entry := pl.preferences["tool_preference:future"]
	pl.mu.RUnlock()
	if entry.Confidence != 0.8 {
		t.Errorf("confidence should be unchanged at 0.8, got %f", entry.Confidence)
	}
}
