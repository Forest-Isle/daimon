package evolution

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrajectoryRecorder_WritesJSONL(t *testing.T) {
	dir := t.TempDir()
	tr := NewTrajectoryRecorder(dir)
	defer func() { _ = tr.Close() }()
	ctx := context.Background()

	tr.OnToolExecuted(ctx, ToolExecEvent{
		SessionID:  "s1",
		ToolName:   "bash",
		Succeeded:  true,
		DurationMs: 100,
	})
	tr.OnToolExecuted(ctx, ToolExecEvent{
		SessionID:  "s1",
		ToolName:   "file",
		Succeeded:  false,
		DurationMs: 50,
	})

	now := time.Now()
	tr.OnEpisodeComplete(ctx, EpisodeEvent{
		SessionID:    "s1",
		EpisodeID:    "ep1",
		Goal:         "test goal",
		Complexity:   "moderate",
		Succeeded:    true,
		TotalReward:  0.85,
		ToolSequence: []string{"bash", "file"},
		ReplanCount:  1,
		DurationMs:   500,
		UserFeedback: 0.5,
		Timestamp:    now,
	})

	// Verify file exists
	dayFile := filepath.Join(dir, now.Format("2006-01-02")+".jsonl")
	data, err := os.ReadFile(dayFile)
	if err != nil {
		t.Fatalf("read trajectory file: %v", err)
	}

	var rec TrajectoryRecord
	if err := json.Unmarshal(data[:len(data)-1], &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if rec.SessionID != "s1" {
		t.Errorf("session_id = %q, want s1", rec.SessionID)
	}
	if rec.Goal != "test goal" {
		t.Errorf("goal = %q, want 'test goal'", rec.Goal)
	}
	if len(rec.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(rec.Tools))
	}
	if rec.Tools[0].Name != "bash" || !rec.Tools[0].Succeeded {
		t.Errorf("tool[0] = %+v, want bash/true", rec.Tools[0])
	}
	if rec.Tools[1].Name != "file" || rec.Tools[1].Succeeded {
		t.Errorf("tool[1] = %+v, want file/false", rec.Tools[1])
	}
}

func TestTrajectoryRecorder_FallbackToToolSequence(t *testing.T) {
	dir := t.TempDir()
	tr := NewTrajectoryRecorder(dir)
	defer func() { _ = tr.Close() }()

	now := time.Now()
	tr.OnEpisodeComplete(context.Background(), EpisodeEvent{
		SessionID:    "s2",
		Succeeded:    true,
		ToolSequence: []string{"http", "bash"},
		Timestamp:    now,
	})

	recs, err := ReadTrajectories(dir, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if len(recs[0].Tools) != 2 {
		t.Errorf("tools = %d, want 2 (from ToolSequence fallback)", len(recs[0].Tools))
	}
}

func TestTrajectoryRecorder_DailyRotation(t *testing.T) {
	dir := t.TempDir()
	tr := NewTrajectoryRecorder(dir)
	defer func() { _ = tr.Close() }()

	day1 := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC)

	tr.OnEpisodeComplete(context.Background(), EpisodeEvent{
		SessionID: "s1", Succeeded: true, Timestamp: day1,
	})
	tr.OnEpisodeComplete(context.Background(), EpisodeEvent{
		SessionID: "s2", Succeeded: true, Timestamp: day2,
	})

	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("expected 2 daily files, got %d", len(entries))
	}
}

func TestReadTrajectories_TimeFiltering(t *testing.T) {
	dir := t.TempDir()
	tr := NewTrajectoryRecorder(dir)

	base := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		tr.OnEpisodeComplete(context.Background(), EpisodeEvent{
			SessionID: "s" + string(rune('0'+i)),
			Succeeded: true,
			Timestamp: base.Add(time.Duration(i) * 24 * time.Hour),
		})
	}
	_ = tr.Close()

	since := time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 4, 11, 23, 59, 59, 0, time.UTC)
	recs, err := ReadTrajectories(dir, since, until)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Errorf("expected 3 records in range, got %d", len(recs))
	}
}

func TestReadTrajectories_EmptyDir(t *testing.T) {
	recs, err := ReadTrajectories("/nonexistent/path", time.Time{}, time.Now())
	if err != nil {
		t.Fatalf("should not error on missing dir: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected 0, got %d", len(recs))
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"a\n", 1},
		{"a\nb\n", 2},
		{"a\nb", 2},
		{"single", 1},
	}
	for _, tc := range tests {
		lines := splitLines([]byte(tc.input))
		if len(lines) != tc.expected {
			t.Errorf("splitLines(%q) = %d lines, want %d", tc.input, len(lines), tc.expected)
		}
	}
}
