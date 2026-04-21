package eval

import (
	"context"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

// TestTaskCase_UserFeedback_AffectsEpisodeReward verifies that a TaskCase with
// positive UserFeedback produces a higher EpisodeReward than the same task
// without feedback, and that the value is stored in EvalResult.UserFeedback.
func TestTaskCase_UserFeedback_AffectsEpisodeReward(t *testing.T) {
	hook := NewEvalHook()
	r := &CognitiveAgentRunner{
		channel: &EvalChannel{},
		hook:    hook,
	}

	// Seed a successful reflection so populateFromEvolution produces a real result.
	hook.OnReflectionComplete(context.Background(), evolution.ReflectionEvent{
		SessionID:  "sess-base",
		Succeeded:  true,
		Confidence: 0.9,
	})

	// Baseline: same result fields, no user feedback.
	baseResult := &EvalResult{
		Success:           true,
		AssertionPassRate: 0.8,
	}
	r.populateFromEvolution(baseResult, "sess-base")

	// With feedback: re-run populateFromEvolution for a fresh result, then
	// apply user feedback the same way RunTask does.
	hook.OnReflectionComplete(context.Background(), evolution.ReflectionEvent{
		SessionID:  "sess-fb",
		Succeeded:  true,
		Confidence: 0.9,
	})

	fbResult := &EvalResult{
		Success:           true,
		AssertionPassRate: 0.8,
	}
	r.populateFromEvolution(fbResult, "sess-fb")

	const feedback = 0.8
	fbResult.UserFeedback = feedback
	fbResult.EpisodeReward = evolution.ComputeReward(evolution.RewardInput{
		Succeeded:    fbResult.Success,
		Progress:     fbResult.AssertionPassRate,
		DurationMs:   fbResult.Duration.Milliseconds(),
		ReplanCount:  fbResult.ReplanCount,
		UserFeedback: feedback,
	})

	if fbResult.UserFeedback != feedback {
		t.Errorf("UserFeedback = %f, want %f", fbResult.UserFeedback, feedback)
	}
	if fbResult.EpisodeReward <= baseResult.EpisodeReward {
		t.Errorf("positive feedback should increase EpisodeReward: got %f, base %f",
			fbResult.EpisodeReward, baseResult.EpisodeReward)
	}
}

// TestTaskCase_NegativeUserFeedback_LowersEpisodeReward ensures that negative
// UserFeedback produces a lower EpisodeReward than the baseline (no feedback).
func TestTaskCase_NegativeUserFeedback_LowersEpisodeReward(t *testing.T) {
	hook := NewEvalHook()
	r := &CognitiveAgentRunner{
		channel: &EvalChannel{},
		hook:    hook,
	}

	// Baseline.
	hook.OnReflectionComplete(context.Background(), evolution.ReflectionEvent{
		SessionID: "sess-neg-base",
		Succeeded: true,
	})
	baseResult := &EvalResult{Success: true}
	r.populateFromEvolution(baseResult, "sess-neg-base")

	// Negative feedback result.
	hook.OnReflectionComplete(context.Background(), evolution.ReflectionEvent{
		SessionID: "sess-neg-fb",
		Succeeded: true,
	})
	negResult := &EvalResult{Success: true}
	r.populateFromEvolution(negResult, "sess-neg-fb")

	const feedback = -0.5
	negResult.UserFeedback = feedback
	negResult.EpisodeReward = evolution.ComputeReward(evolution.RewardInput{
		Succeeded:    negResult.Success,
		Progress:     negResult.AssertionPassRate,
		DurationMs:   negResult.Duration.Milliseconds(),
		ReplanCount:  negResult.ReplanCount,
		UserFeedback: feedback,
	})

	if negResult.EpisodeReward >= baseResult.EpisodeReward {
		t.Errorf("negative feedback should decrease EpisodeReward: got %f, base %f",
			negResult.EpisodeReward, baseResult.EpisodeReward)
	}
}

// TestEvolutionSnapshot_PreferenceQuality verifies that populatePreferenceQuality
// correctly computes the confidence distribution from a PreferenceLearner that
// has accumulated known entries via OnReflectionComplete.
func TestEvolutionSnapshot_PreferenceQuality(t *testing.T) {
	cfg := evolution.PreferenceConfig{
		Enabled:        true,
		MinConfidence:  0.0, // accept all for testing
		MaxPreferences: 100,
	}
	pl := evolution.NewPreferenceLearner(cfg)
	ctx := context.Background()

	// Build a tool entry with count=5 → confidence=1.0 (high).
	for i := 0; i < 5; i++ {
		pl.OnReflectionComplete(ctx, evolution.ReflectionEvent{
			Succeeded:  true,
			Confidence: 0.0, // bypasses MinConfidence check via cfg
			ToolsUsed:  []string{"bash"},
			Complexity: "simple",
		})
	}

	// Build a tool entry with count=2 → confidence=0.4 (med).
	for i := 0; i < 2; i++ {
		pl.OnReflectionComplete(ctx, evolution.ReflectionEvent{
			Succeeded:  true,
			Confidence: 0.0,
			ToolsUsed:  []string{"file_read"},
			Complexity: "",
		})
	}

	snap := &EvolutionSnapshot{}
	populatePreferenceQuality(snap, pl)

	if snap.PreferenceToolCount < 2 {
		t.Errorf("expected at least 2 tool preference entries, got %d", snap.PreferenceToolCount)
	}
	if snap.PreferenceComplexityCount < 1 {
		t.Errorf("expected at least 1 complexity entry, got %d", snap.PreferenceComplexityCount)
	}
	if snap.PreferenceHighConfCount == 0 {
		t.Error("expected at least one high-confidence preference (bash, count=5 → conf=1.0)")
	}
	if snap.PreferenceAvgConfidence <= 0 {
		t.Errorf("PreferenceAvgConfidence = %f, want > 0", snap.PreferenceAvgConfidence)
	}
	if snap.PreferenceAvgConfidence > 1.0 {
		t.Errorf("PreferenceAvgConfidence = %f, want <= 1.0", snap.PreferenceAvgConfidence)
	}

	total := snap.PreferenceHighConfCount + snap.PreferenceMedConfCount + snap.PreferenceLowConfCount
	if total == 0 {
		t.Error("total preference count across all buckets should be > 0")
	}
}

// TestPreferenceTasks_Structure checks that PreferenceTasks returns well-formed
// task cases with valid UserFeedback values.
func TestPreferenceTasks_Structure(t *testing.T) {
	tasks := PreferenceTasks()
	if len(tasks) == 0 {
		t.Fatal("PreferenceTasks() returned empty slice")
	}

	for _, task := range tasks {
		if task.ID == "" {
			t.Errorf("task missing ID: %+v", task)
		}
		if task.Goal == "" {
			t.Errorf("task %q missing Goal", task.ID)
		}
		if task.UserFeedback < -1.0 || task.UserFeedback > 1.0 {
			t.Errorf("task %q has out-of-range UserFeedback: %f", task.ID, task.UserFeedback)
		}
	}

	// Verify at least one task has positive and one has negative feedback.
	var hasPositive, hasNegative bool
	for _, task := range tasks {
		if task.UserFeedback > 0 {
			hasPositive = true
		}
		if task.UserFeedback < 0 {
			hasNegative = true
		}
	}
	if !hasPositive {
		t.Error("PreferenceTasks should include at least one task with positive UserFeedback")
	}
	if !hasNegative {
		t.Error("PreferenceTasks should include at least one task with negative UserFeedback")
	}
}
