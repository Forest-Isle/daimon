package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TrajectoryRecord is one complete cognitive cycle serialized as a JSONL line.
type TrajectoryRecord struct {
	SessionID    string          `json:"session_id"`
	Goal         string          `json:"goal"`
	Complexity   string          `json:"complexity"`
	Tools        []ToolRecord    `json:"tools"`
	Reflection   ReflectionBrief `json:"reflection"`
	UserFeedback float64         `json:"user_feedback"`
	ReplanCount  int             `json:"replan_count"`
	DurationMs   int64           `json:"duration_ms"`
	Timestamp    time.Time       `json:"timestamp"`
}

// ToolRecord captures a single tool invocation within a trajectory.
type ToolRecord struct {
	Name       string `json:"name"`
	Succeeded  bool   `json:"succeeded"`
	DurationMs int64  `json:"duration_ms"`
}

// ReflectionBrief is a compact view of the reflection outcome.
type ReflectionBrief struct {
	Confidence float64  `json:"confidence"`
	Reward     float64  `json:"reward"`
	Succeeded  bool     `json:"succeeded"`
	Lessons    []string `json:"lessons,omitempty"`
}

// TrajectoryRecorder implements Hook. It persists every completed cognitive
// cycle as a JSONL line under the configured trajectories directory. Files
// are rotated daily (one file per calendar day).
//
// Concurrent writes are serialized via a mutex; file handles are kept open
// for the lifetime of the current day's file and rotated lazily.
type TrajectoryRecorder struct {
	dir     string // absolute path to trajectories directory
	mu      sync.Mutex
	file    *os.File
	fileDay string // "2006-01-02" of the currently open file

	// accumulated tool events for the current session (keyed by session_id)
	toolBuf   map[string][]ToolRecord
	toolBufMu sync.Mutex
}

var _ Hook = (*TrajectoryRecorder)(nil)

// NewTrajectoryRecorder creates a recorder that writes to dir.
// The directory is created lazily on first write.
func NewTrajectoryRecorder(dir string) *TrajectoryRecorder {
	return &TrajectoryRecorder{
		dir:     dir,
		toolBuf: make(map[string][]ToolRecord),
	}
}

func (tr *TrajectoryRecorder) Name() string { return "trajectory_recorder" }

// OnToolExecuted buffers individual tool results per session.
func (tr *TrajectoryRecorder) OnToolExecuted(_ context.Context, event ToolExecEvent) {
	rec := ToolRecord{
		Name:       event.ToolName,
		Succeeded:  event.Succeeded,
		DurationMs: event.DurationMs,
	}
	tr.toolBufMu.Lock()
	tr.toolBuf[event.SessionID] = append(tr.toolBuf[event.SessionID], rec)
	tr.toolBufMu.Unlock()
}

// OnReflectionComplete is a no-op; episode events carry the full picture.
func (tr *TrajectoryRecorder) OnReflectionComplete(_ context.Context, _ ReflectionEvent) {}

// OnEpisodeComplete flushes a complete trajectory line to the JSONL file.
func (tr *TrajectoryRecorder) OnEpisodeComplete(_ context.Context, event EpisodeEvent) {
	tr.toolBufMu.Lock()
	tools := tr.toolBuf[event.SessionID]
	delete(tr.toolBuf, event.SessionID)
	tr.toolBufMu.Unlock()

	// Fall back to ToolSequence names if no buffered tool events.
	if len(tools) == 0 && len(event.ToolSequence) > 0 {
		for _, name := range event.ToolSequence {
			tools = append(tools, ToolRecord{Name: name, Succeeded: true})
		}
	}

	rec := TrajectoryRecord{
		SessionID:  event.SessionID,
		Goal:       event.Goal,
		Complexity: event.Complexity,
		Tools:      tools,
		Reflection: ReflectionBrief{
			Reward:    event.TotalReward,
			Succeeded: event.Succeeded,
		},
		UserFeedback: event.UserFeedback,
		ReplanCount:  event.ReplanCount,
		DurationMs:   event.DurationMs,
		Timestamp:    event.Timestamp,
	}

	if err := tr.append(rec); err != nil {
		slog.Warn("trajectory_recorder: write failed", "err", err)
	}
}

// append serializes rec as JSON and writes it as one line to the daily file.
func (tr *TrajectoryRecorder) append(rec TrajectoryRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal trajectory: %w", err)
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()

	day := rec.Timestamp.Format("2006-01-02")
	if err := tr.ensureFileLocked(day); err != nil {
		return err
	}

	data = append(data, '\n')
	_, err = tr.file.Write(data)
	return err
}

// ensureFileLocked opens (or rotates to) the file for the given day. Caller must hold mu.
func (tr *TrajectoryRecorder) ensureFileLocked(day string) error {
	if tr.file != nil && tr.fileDay == day {
		return nil
	}
	if tr.file != nil {
		_ = tr.file.Close()
		tr.file = nil
	}

	if err := os.MkdirAll(tr.dir, 0o755); err != nil {
		return fmt.Errorf("create trajectories dir: %w", err)
	}

	path := filepath.Join(tr.dir, day+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open trajectory file: %w", err)
	}

	tr.file = f
	tr.fileDay = day
	return nil
}

// Close releases the open file handle.
func (tr *TrajectoryRecorder) Close() error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.file != nil {
		err := tr.file.Close()
		tr.file = nil
		return err
	}
	return nil
}

// ReadTrajectories reads all trajectory records from JSONL files in the
// configured directory, filtered to the given time range. Useful for the
// InsightsEngine and CLI exports.
func ReadTrajectories(dir string, since, until time.Time) ([]TrajectoryRecord, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	since = since.UTC()
	until = until.UTC()

	// Day-level pre-filter with ±1 day buffer: file naming uses the timestamp's
	// local timezone but time.Parse gives UTC midnight, so tz offsets can push
	// the parsed day outside the range. The per-record timestamp check below
	// ensures exact correctness.
	dayLo := since.Truncate(24 * time.Hour).Add(-24 * time.Hour)
	dayHi := until.Truncate(24 * time.Hour).Add(24 * time.Hour)

	var results []TrajectoryRecord
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		dayStr := entry.Name()[:len(entry.Name())-len(".jsonl")]
		day, err := time.Parse("2006-01-02", dayStr)
		if err != nil {
			continue
		}
		if day.Before(dayLo) || day.After(dayHi) {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			slog.Warn("trajectory: read file failed", "file", entry.Name(), "err", err)
			continue
		}

		for _, line := range splitLines(data) {
			if len(line) == 0 {
				continue
			}
			var rec TrajectoryRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				slog.Debug("trajectory: skip malformed line", "err", err)
				continue
			}
			if !rec.Timestamp.Before(since) && !rec.Timestamp.After(until) {
				results = append(results, rec)
			}
		}
	}
	return results, nil
}

// splitLines splits data by newline without allocating empty trailing entries.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	for len(data) > 0 {
		idx := -1
		for i, b := range data {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			if len(data) > 0 {
				lines = append(lines, data)
			}
			break
		}
		if idx > 0 {
			lines = append(lines, data[:idx])
		}
		data = data[idx+1:]
	}
	return lines
}
