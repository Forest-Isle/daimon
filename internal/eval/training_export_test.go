package eval

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeTestRecords returns a set of trajectory records for testing.
func makeTestRecords() []TrajectoryRecord {
	ts := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	return []TrajectoryRecord{
		{
			SessionID:  "s1",
			Goal:       "fix bug in parser",
			Complexity: "medium",
			Tools: []ToolRecord{
				{Name: "bash", Succeeded: true, DurationMs: 250},
				{Name: "file_read", Succeeded: true, DurationMs: 100},
			},
			Reflection:   ReflectionBrief{Confidence: 0.95, Reward: 15.0, Succeeded: true, Lessons: []string{"check edge cases"}},
			UserFeedback: 0.8,
			ReplanCount:  0,
			DurationMs:   5000,
			Timestamp:    ts,
		},
		{
			SessionID:  "s2",
			Goal:       "fix bug in parser",
			Complexity: "medium",
			Tools: []ToolRecord{
				{Name: "bash", Succeeded: false, DurationMs: 300},
			},
			Reflection:   ReflectionBrief{Confidence: 0.4, Reward: 2.0, Succeeded: false},
			UserFeedback: 0.2,
			ReplanCount:  3,
			DurationMs:   8000,
			Timestamp:    ts.Add(time.Hour),
		},
		{
			SessionID:  "s3",
			Goal:       "add logging",
			Complexity: "low",
			Tools: []ToolRecord{
				{Name: "file_write", Succeeded: true, DurationMs: 50},
			},
			Reflection:   ReflectionBrief{Confidence: 0.99, Reward: 18.0, Succeeded: true},
			UserFeedback: 0.9,
			ReplanCount:  0,
			DurationMs:   2000,
			Timestamp:    ts.Add(2 * time.Hour),
		},
		{
			SessionID:  "s4",
			Goal:       "refactor auth",
			Complexity: "high",
			Tools: []ToolRecord{
				{Name: "bash", Succeeded: true, DurationMs: 500},
				{Name: "file_read", Succeeded: true, DurationMs: 200},
				{Name: "file_write", Succeeded: true, DurationMs: 150},
			},
			Reflection:   ReflectionBrief{Confidence: 0.7, Reward: 5.0, Succeeded: true},
			UserFeedback: 0.5,
			ReplanCount:  1,
			DurationMs:   10000,
			Timestamp:    ts.Add(3 * time.Hour),
		},
	}
}

// writeTestJSONL writes records to a dated JSONL file in dir.
func writeTestJSONL(t *testing.T, dir string, records []TrajectoryRecord) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Group by day and write separate files.
	byDay := make(map[string][]TrajectoryRecord)
	for _, r := range records {
		day := r.Timestamp.UTC().Format("2006-01-02")
		byDay[day] = append(byDay[day], r)
	}
	for day, recs := range byDay {
		path := filepath.Join(dir, day+".jsonl")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		enc := json.NewEncoder(f)
		for _, r := range recs {
			if err := enc.Encode(r); err != nil {
				_ = f.Close()
				t.Fatal(err)
			}
		}
		_ = f.Close()
	}
}

func TestReadTrajectories(t *testing.T) {
	dir := t.TempDir()
	records := makeTestRecords()
	writeTestJSONL(t, dir, records)

	// Read all records (since = zero time).
	got, err := readTrajectories(dir, time.Time{})
	if err != nil {
		t.Fatalf("readTrajectories: %v", err)
	}
	if len(got) != len(records) {
		t.Fatalf("expected %d records, got %d", len(records), len(got))
	}

	// Read with since filter — should exclude first record.
	since := records[0].Timestamp.Add(time.Minute)
	got, err = readTrajectories(dir, since)
	if err != nil {
		t.Fatalf("readTrajectories with since: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 records after since filter, got %d", len(got))
	}
}

func TestReadTrajectories_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got, err := readTrajectories(dir, time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 records, got %d", len(got))
	}
}

func TestReadTrajectories_NonExistentDir(t *testing.T) {
	got, err := readTrajectories("/tmp/nonexistent-ironclaw-test-dir", time.Time{})
	if err != nil {
		t.Fatalf("unexpected error for missing dir: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestExportRLHF(t *testing.T) {
	dir := t.TempDir()
	outDir := t.TempDir()
	records := makeTestRecords()
	writeTestJSONL(t, dir, records)

	result, err := ExportTrainingData(ExportConfig{
		TrajectoryDir: dir,
		OutputDir:     outDir,
		Format:        FormatReward,
		MinReward:     0,
		MinConfidence: 0,
		Since:         time.Time{},
	})
	if err != nil {
		t.Fatalf("ExportTrainingData RLHF: %v", err)
	}
	if result.Samples != 4 {
		t.Fatalf("expected 4 RLHF samples, got %d", result.Samples)
	}

	// Verify output file is valid JSONL.
	samples := readJSONLFile[RLHFSample](t, result.OutputPath)
	if len(samples) != 4 {
		t.Fatalf("expected 4 lines in output, got %d", len(samples))
	}

	// Rewards should be normalized to [0, 1].
	for _, s := range samples {
		if s.Reward < 0 || s.Reward > 1.0 {
			t.Errorf("reward %f out of [0,1] range", s.Reward)
		}
	}
}

func TestExportDPO(t *testing.T) {
	dir := t.TempDir()
	outDir := t.TempDir()
	records := makeTestRecords()
	writeTestJSONL(t, dir, records)

	result, err := ExportTrainingData(ExportConfig{
		TrajectoryDir: dir,
		OutputDir:     outDir,
		Format:        FormatDPO,
		MinReward:     0,
		MinConfidence: 0,
		Since:         time.Time{},
	})
	if err != nil {
		t.Fatalf("ExportTrainingData DPO: %v", err)
	}

	// Only "fix bug in parser" has 2 records with different rewards → 1 pair.
	if result.Pairs != 1 {
		t.Fatalf("expected 1 DPO pair, got %d", result.Pairs)
	}

	pairs := readJSONLFile[DPOPair](t, result.OutputPath)
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair in file, got %d", len(pairs))
	}
	pair := pairs[0]
	if pair.Prompt != "fix bug in parser" {
		t.Errorf("unexpected prompt: %s", pair.Prompt)
	}
	if pair.Metadata.ChosenReward <= pair.Metadata.RejectedReward {
		t.Error("chosen reward should be greater than rejected reward")
	}
	if pair.Metadata.ChosenSession != "s1" {
		t.Errorf("expected chosen session s1, got %s", pair.Metadata.ChosenSession)
	}
	if pair.Metadata.RejectedSession != "s2" {
		t.Errorf("expected rejected session s2, got %s", pair.Metadata.RejectedSession)
	}
}

func TestExportSFT(t *testing.T) {
	dir := t.TempDir()
	outDir := t.TempDir()
	records := makeTestRecords()
	writeTestJSONL(t, dir, records)

	result, err := ExportTrainingData(ExportConfig{
		TrajectoryDir: dir,
		OutputDir:     outDir,
		Format:        FormatSFT,
		MinReward:     0,
		MinConfidence: 0.5,
		Since:         time.Time{},
	})
	if err != nil {
		t.Fatalf("ExportTrainingData SFT: %v", err)
	}

	// s1 (succeeded, conf=0.95), s3 (succeeded, conf=0.99), s4 (succeeded, conf=0.7) pass.
	// s2 (failed) excluded.
	if result.SFTSamples != 3 {
		t.Fatalf("expected 3 SFT samples, got %d", result.SFTSamples)
	}

	samples := readJSONLFile[SFTSample](t, result.OutputPath)
	for _, s := range samples {
		if s.Instruction == "" || s.Output == "" {
			t.Error("SFT sample has empty instruction or output")
		}
	}
}

func TestExportConfig_MinReward(t *testing.T) {
	dir := t.TempDir()
	outDir := t.TempDir()
	records := makeTestRecords()
	writeTestJSONL(t, dir, records)

	// MinReward=10 should exclude s2 (reward=2) and s4 (reward=5).
	result, err := ExportTrainingData(ExportConfig{
		TrajectoryDir: dir,
		OutputDir:     outDir,
		Format:        FormatReward,
		MinReward:     10.0,
		MinConfidence: 0,
		Since:         time.Time{},
	})
	if err != nil {
		t.Fatalf("ExportTrainingData with MinReward: %v", err)
	}
	if result.Samples != 2 {
		t.Fatalf("expected 2 samples with MinReward=10, got %d", result.Samples)
	}
}

func TestExportUnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	outDir := t.TempDir()

	_, err := ExportTrainingData(ExportConfig{
		TrajectoryDir: dir,
		OutputDir:     outDir,
		Format:        TrainingFormat("unknown"),
	})
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func TestFormatToolSequence(t *testing.T) {
	tests := []struct {
		name  string
		tools []ToolRecord
		want  string
	}{
		{
			name:  "empty",
			tools: nil,
			want:  "(no tools)",
		},
		{
			name: "single_success",
			tools: []ToolRecord{
				{Name: "bash", Succeeded: true, DurationMs: 250},
			},
			want: "bash(succeed, 250ms)",
		},
		{
			name: "multiple_mixed",
			tools: []ToolRecord{
				{Name: "bash", Succeeded: true, DurationMs: 250},
				{Name: "file_read", Succeeded: true, DurationMs: 100},
				{Name: "bash", Succeeded: false, DurationMs: 50},
			},
			want: "bash(succeed, 250ms) → file_read(succeed, 100ms) → bash(failed, 50ms)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatToolSequence(tt.tools)
			if got != tt.want {
				t.Errorf("formatToolSequence = %q, want %q", got, tt.want)
			}
		})
	}
}

// readJSONLFile is a test helper that reads a JSONL file into a slice.
func readJSONLFile[T any](t *testing.T, path string) []T {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	var items []T
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var item T
		if err := json.Unmarshal(line, &item); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		items = append(items, item)
	}
	return items
}
