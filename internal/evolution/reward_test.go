package evolution

import (
	"math"
	"testing"
)

func TestComputeReward_Success(t *testing.T) {
	r := ComputeReward(RewardInput{
		Succeeded:  true,
		Progress:   1.0,
		DurationMs: 5000,
	})
	if r <= 0 {
		t.Errorf("successful fast task should have positive reward, got %f", r)
	}
}

func TestComputeReward_Failure(t *testing.T) {
	r := ComputeReward(RewardInput{
		Succeeded:  false,
		Progress:   0.0,
		DurationMs: 120000,
	})
	if r >= 0 {
		t.Errorf("failed slow task should have negative reward, got %f", r)
	}
}

func TestComputeReward_SpeedBonus(t *testing.T) {
	fast := ComputeReward(RewardInput{Succeeded: true, DurationMs: 10000})
	slow := ComputeReward(RewardInput{Succeeded: true, DurationMs: 120000})
	if fast <= slow {
		t.Errorf("fast task (%f) should score higher than slow (%f)", fast, slow)
	}
}

func TestComputeReward_ReplanPenalty(t *testing.T) {
	noReplan := ComputeReward(RewardInput{Succeeded: true, ReplanCount: 0})
	withReplan := ComputeReward(RewardInput{Succeeded: true, ReplanCount: 2})
	if noReplan <= withReplan {
		t.Errorf("no-replan (%f) should score higher than replanned (%f)", noReplan, withReplan)
	}
}

func TestComputeReward_UserFeedback(t *testing.T) {
	positive := ComputeReward(RewardInput{Succeeded: true, UserFeedback: 1.0})
	negative := ComputeReward(RewardInput{Succeeded: true, UserFeedback: -1.0})
	if positive <= negative {
		t.Errorf("positive feedback (%f) should score higher than negative (%f)", positive, negative)
	}
}

func TestComputeReward_Clamped(t *testing.T) {
	worst := ComputeReward(RewardInput{
		Succeeded:    false,
		Progress:     0,
		DurationMs:   999999,
		ReplanCount:  10,
		UserFeedback: -1.0,
	})
	if worst < -1.5 {
		t.Errorf("reward should be clamped to >= -1.5, got %f", worst)
	}

	best := ComputeReward(RewardInput{
		Succeeded:    true,
		Progress:     1.0,
		DurationMs:   1000,
		ReplanCount:  0,
		UserFeedback: 1.0,
	})
	if best > 1.0 {
		t.Errorf("reward should be clamped to <= 1.0, got %f", best)
	}
}

func TestComputeReward_ProgressContribution(t *testing.T) {
	noProgress := ComputeReward(RewardInput{Succeeded: false, Progress: 0.0})
	halfProgress := ComputeReward(RewardInput{Succeeded: false, Progress: 0.5})
	if halfProgress <= noProgress {
		t.Errorf("partial progress (%f) should score higher than none (%f)", halfProgress, noProgress)
	}
	diff := halfProgress - noProgress
	expected := 0.5 * 0.25
	if math.Abs(diff-expected) > 1e-9 {
		t.Errorf("progress delta = %f, want %f", diff, expected)
	}
}
