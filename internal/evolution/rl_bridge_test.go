package evolution

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConvertTrajectories_Empty(t *testing.T) {
	exps := ConvertTrajectories(nil)
	if len(exps) != 0 {
		t.Errorf("expected 0, got %d", len(exps))
	}
}

func TestConvertTrajectories_Basic(t *testing.T) {
	records := []TrajectoryRecord{
		{
			Complexity: "simple",
			Tools: []ToolRecord{
				{Name: "bash", Succeeded: true, DurationMs: 50},
			},
			Reflection:   ReflectionBrief{Confidence: 0.9, Succeeded: true},
			UserFeedback: 1.0,
			DurationMs:   500,
			ReplanCount:  0,
		},
		{
			Complexity: "complex",
			Tools: []ToolRecord{
				{Name: "http", Succeeded: false, DurationMs: 100},
			},
			Reflection:   ReflectionBrief{Confidence: 0.3, Succeeded: false},
			UserFeedback: -1.0,
			DurationMs:   120000,
			ReplanCount:  3,
		},
	}

	exps := ConvertTrajectories(records)
	if len(exps) != 2 {
		t.Fatalf("expected 2, got %d", len(exps))
	}

	if exps[0].ComplexitySimple != 1.0 {
		t.Error("first should be simple")
	}
	if exps[0].Reward <= 0 {
		t.Errorf("successful episode should have positive reward, got %.2f", exps[0].Reward)
	}

	if exps[1].ComplexityComplex != 1.0 {
		t.Error("second should be complex")
	}
	if exps[1].Reward >= 0 {
		t.Errorf("failed episode with negative feedback should have negative reward, got %.2f", exps[1].Reward)
	}
}

func TestConvertFromDir(t *testing.T) {
	dir := t.TempDir()
	tr := NewTrajectoryRecorder(dir)

	now := time.Now()
	tr.OnEpisodeComplete(nil, EpisodeEvent{
		SessionID:    "s1",
		Succeeded:    true,
		Complexity:   "moderate",
		ToolSequence: []string{"bash"},
		Timestamp:    now,
	})
	tr.Close()

	exps, err := ConvertFromDir(dir, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(exps) != 1 {
		t.Fatalf("expected 1, got %d", len(exps))
	}
	if exps[0].ComplexityModerate != 1.0 {
		t.Error("should be moderate")
	}
}

func TestConvertFromDir_MissingDir(t *testing.T) {
	exps, err := ConvertFromDir(filepath.Join(os.TempDir(), "nonexistent_dir_test"), time.Time{})
	if err != nil {
		t.Fatalf("should not error on missing dir: %v", err)
	}
	if len(exps) != 0 {
		t.Errorf("expected 0, got %d", len(exps))
	}
}
