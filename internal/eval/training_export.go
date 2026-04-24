package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TrainingFormat defines the export format for training data.
type TrainingFormat string

const (
	FormatRLHF TrainingFormat = "rlhf" // Reward-labeled pairs
	FormatDPO  TrainingFormat = "dpo"  // Direct Preference Optimization pairs
	FormatSFT  TrainingFormat = "sft"  // Supervised Fine-Tuning (successful only)
)

// TrajectoryRecord mirrors evolution.TrajectoryRecord for decoupled import.
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

// RLHFSample is a single training sample in RLHF format.
type RLHFSample struct {
	Prompt   string       `json:"prompt"`
	Response string       `json:"response"`
	Reward   float64      `json:"reward"`
	Metadata RLHFMetadata `json:"metadata"`
}

// RLHFMetadata holds provenance information for an RLHF sample.
type RLHFMetadata struct {
	SessionID  string  `json:"session_id"`
	Complexity string  `json:"complexity"`
	Confidence float64 `json:"confidence"`
	Duration   int64   `json:"duration_ms"`
}

// DPOPair is a preference pair for Direct Preference Optimization.
type DPOPair struct {
	Prompt   string      `json:"prompt"`
	Chosen   string      `json:"chosen"`
	Rejected string      `json:"rejected"`
	Metadata DPOMetadata `json:"metadata"`
}

// DPOMetadata holds provenance information for a DPO pair.
type DPOMetadata struct {
	ChosenReward    float64 `json:"chosen_reward"`
	RejectedReward  float64 `json:"rejected_reward"`
	ChosenSession   string  `json:"chosen_session"`
	RejectedSession string  `json:"rejected_session"`
}

// SFTSample is a supervised fine-tuning sample (successful trajectories only).
type SFTSample struct {
	Instruction string      `json:"instruction"`
	Output      string      `json:"output"`
	Metadata    SFTMetadata `json:"metadata"`
}

// SFTMetadata holds provenance information for an SFT sample.
type SFTMetadata struct {
	SessionID  string  `json:"session_id"`
	Confidence float64 `json:"confidence"`
}

// ExportConfig configures the training data export.
type ExportConfig struct {
	TrajectoryDir string         // Source directory with JSONL files
	OutputDir     string         // Destination directory
	Format        TrainingFormat // rlhf, dpo, or sft
	MinReward     float64        // Minimum reward threshold for inclusion
	MinConfidence float64        // Minimum confidence threshold
	Since         time.Time      // Only include records after this time
}

// ExportResult summarizes what was exported.
type ExportResult struct {
	Format     TrainingFormat `json:"format"`
	Samples    int            `json:"samples,omitempty"`
	Pairs      int            `json:"pairs,omitempty"`
	SFTSamples int            `json:"sft_samples,omitempty"`
	OutputPath string         `json:"output_path"`
}

// ExportTrainingData reads trajectories and exports in the specified format.
func ExportTrainingData(cfg ExportConfig) (*ExportResult, error) {
	records, err := readTrajectories(cfg.TrajectoryDir, cfg.Since)
	if err != nil {
		return nil, fmt.Errorf("read trajectories: %w", err)
	}

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	outputPath := filepath.Join(cfg.OutputDir, fmt.Sprintf("training_%s.jsonl", cfg.Format))
	result := &ExportResult{Format: cfg.Format, OutputPath: outputPath}

	switch cfg.Format {
	case FormatRLHF:
		samples, exportErr := exportRLHF(records, cfg)
		if exportErr != nil {
			return nil, exportErr
		}
		result.Samples = len(samples)
		if err := writeJSONL(outputPath, samples); err != nil {
			return nil, err
		}
	case FormatDPO:
		pairs, exportErr := exportDPO(records, cfg)
		if exportErr != nil {
			return nil, exportErr
		}
		result.Pairs = len(pairs)
		if err := writeJSONL(outputPath, pairs); err != nil {
			return nil, err
		}
	case FormatSFT:
		samples, exportErr := exportSFT(records, cfg)
		if exportErr != nil {
			return nil, exportErr
		}
		result.SFTSamples = len(samples)
		if err := writeJSONL(outputPath, samples); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported format: %s", cfg.Format)
	}

	return result, nil
}

// readTrajectories reads all JSONL files in dir, returning records with
// timestamps on or after since. Files are expected to be named YYYY-MM-DD.jsonl.
func readTrajectories(dir string, since time.Time) ([]TrajectoryRecord, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dir: %w", err)
	}

	since = since.UTC()
	// Day-level pre-filter with 1-day buffer for timezone safety.
	dayLo := since.Truncate(24 * time.Hour).Add(-24 * time.Hour)

	var results []TrajectoryRecord
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		dayStr := strings.TrimSuffix(entry.Name(), ".jsonl")
		day, parseErr := time.Parse("2006-01-02", dayStr)
		if parseErr != nil {
			continue
		}
		if day.Before(dayLo) {
			continue
		}

		f, openErr := os.Open(filepath.Join(dir, entry.Name()))
		if openErr != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var rec TrajectoryRecord
			if json.Unmarshal(line, &rec) != nil {
				continue
			}
			if !rec.Timestamp.UTC().Before(since) {
				results = append(results, rec)
			}
		}
		_ = f.Close()
	}
	return results, nil
}

// exportRLHF converts each record passing thresholds into an RLHFSample.
func exportRLHF(records []TrajectoryRecord, cfg ExportConfig) ([]RLHFSample, error) {
	// Find max reward for normalization.
	var maxReward float64
	for _, r := range records {
		if r.Reflection.Reward > maxReward {
			maxReward = r.Reflection.Reward
		}
	}
	if maxReward == 0 {
		maxReward = 1 // avoid division by zero
	}

	var samples []RLHFSample
	for _, r := range records {
		if r.Reflection.Reward < cfg.MinReward {
			continue
		}
		if r.Reflection.Confidence < cfg.MinConfidence {
			continue
		}
		samples = append(samples, RLHFSample{
			Prompt:   r.Goal,
			Response: formatToolSequence(r.Tools),
			Reward:   r.Reflection.Reward / maxReward,
			Metadata: RLHFMetadata{
				SessionID:  r.SessionID,
				Complexity: r.Complexity,
				Confidence: r.Reflection.Confidence,
				Duration:   r.DurationMs,
			},
		})
	}
	return samples, nil
}

// exportDPO groups records by goal, then pairs the best (chosen) with the
// worst (rejected) trajectory for each goal that has at least two records.
func exportDPO(records []TrajectoryRecord, cfg ExportConfig) ([]DPOPair, error) {
	// Group by goal.
	byGoal := make(map[string][]TrajectoryRecord)
	for _, r := range records {
		if r.Reflection.Confidence < cfg.MinConfidence {
			continue
		}
		byGoal[r.Goal] = append(byGoal[r.Goal], r)
	}

	var pairs []DPOPair
	for goal, group := range byGoal {
		if len(group) < 2 {
			continue
		}
		// Sort descending by reward.
		sort.Slice(group, func(i, j int) bool {
			return group[i].Reflection.Reward > group[j].Reflection.Reward
		})
		chosen := group[0]
		rejected := group[len(group)-1]

		// Only pair if there is a meaningful reward difference.
		if chosen.Reflection.Reward <= rejected.Reflection.Reward {
			continue
		}

		pairs = append(pairs, DPOPair{
			Prompt:   goal,
			Chosen:   formatToolSequence(chosen.Tools),
			Rejected: formatToolSequence(rejected.Tools),
			Metadata: DPOMetadata{
				ChosenReward:    chosen.Reflection.Reward,
				RejectedReward:  rejected.Reflection.Reward,
				ChosenSession:   chosen.SessionID,
				RejectedSession: rejected.SessionID,
			},
		})
	}

	// Sort for deterministic output.
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].Prompt < pairs[j].Prompt
	})
	return pairs, nil
}

// exportSFT exports only successful, high-confidence trajectories.
func exportSFT(records []TrajectoryRecord, cfg ExportConfig) ([]SFTSample, error) {
	var samples []SFTSample
	for _, r := range records {
		if !r.Reflection.Succeeded {
			continue
		}
		if r.Reflection.Confidence < cfg.MinConfidence {
			continue
		}
		if r.Reflection.Reward < cfg.MinReward {
			continue
		}
		samples = append(samples, SFTSample{
			Instruction: r.Goal,
			Output:      formatToolSequence(r.Tools),
			Metadata: SFTMetadata{
				SessionID:  r.SessionID,
				Confidence: r.Reflection.Confidence,
			},
		})
	}
	return samples, nil
}

// formatToolSequence renders a tool chain as a human-readable arrow-delimited string.
// Example: "bash(succeed, 250ms) → file_read(succeed, 100ms) → bash(failed, 50ms)"
func formatToolSequence(tools []ToolRecord) string {
	if len(tools) == 0 {
		return "(no tools)"
	}
	parts := make([]string, len(tools))
	for i, t := range tools {
		status := "succeed"
		if !t.Succeeded {
			status = "failed"
		}
		parts[i] = fmt.Sprintf("%s(%s, %dms)", t.Name, status, t.DurationMs)
	}
	return strings.Join(parts, " → ")
}

// writeJSONL writes a slice of JSON-serializable values as newline-delimited JSON.
func writeJSONL[T any](path string, items []T) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, cerr)
		}
	}()

	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, item := range items {
		if err := enc.Encode(item); err != nil {
			return fmt.Errorf("encode: %w", err)
		}
	}
	return w.Flush()
}
