package finetune

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"
)

// TrainingSample is a single instruction-tuning example.
type TrainingSample struct {
	ID        string         `json:"id"`
	System    string         `json:"system"`
	User      string         `json:"user"`
	Assistant string         `json:"assistant"`
	Metadata  SampleMetadata `json:"metadata"`
}

// SampleMetadata carries quality and categorization info.
type SampleMetadata struct {
	SessionID     string   `json:"session_id"`
	QualityScore  float64  `json:"quality_score"`
	Complexity    string   `json:"complexity"`
	ToolsUsed     []string `json:"tools_used"`
	Category      string   `json:"category"`
}

// Dataset is a collection of training samples.
type Dataset struct {
	Samples   []*TrainingSample `json:"samples"`
	CreatedAt time.Time         `json:"created_at"`
	Stats     DatasetStats      `json:"stats"`
}

// DatasetStats summarizes a dataset.
type DatasetStats struct {
	TotalSamples    int                `json:"total_samples"`
	AvgQuality      float64            `json:"avg_quality"`
	ByCategory      map[string]int     `json:"by_category"`
	ByComplexity    map[string]int     `json:"by_complexity"`
}

// DatasetBuilder constructs fine-tuning datasets from cortex memory.
type DatasetBuilder struct {
	minQuality float64
	maxSamples int
}

// NewDatasetBuilder creates a new dataset builder.
func NewDatasetBuilder(minQuality float64, maxSamples int) *DatasetBuilder {
	if minQuality <= 0 {
		minQuality = 0.7
	}
	if maxSamples <= 0 {
		maxSamples = 10000
	}
	return &DatasetBuilder{minQuality: minQuality, maxSamples: maxSamples}
}

// CollectSample adds a training sample if it meets quality thresholds.
func (db *DatasetBuilder) BuildDataset(samples []*TrainingSample) *Dataset {
	filtered := make([]*TrainingSample, 0)
	byCategory := make(map[string]int)
	byComplexity := make(map[string]int)
	var totalQuality float64

	for _, s := range samples {
		if s.Metadata.QualityScore < db.minQuality {
			continue
		}
		filtered = append(filtered, s)
		byCategory[s.Metadata.Category]++
		byComplexity[s.Metadata.Complexity]++
		totalQuality += s.Metadata.QualityScore
		if len(filtered) >= db.maxSamples {
			break
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Metadata.QualityScore > filtered[j].Metadata.QualityScore
	})

	avgQuality := 0.0
	if len(filtered) > 0 {
		avgQuality = totalQuality / float64(len(filtered))
	}

	return &Dataset{
		Samples:   filtered,
		CreatedAt: time.Now(),
		Stats: DatasetStats{
			TotalSamples: len(filtered),
			AvgQuality:   avgQuality,
			ByCategory:   byCategory,
			ByComplexity: byComplexity,
		},
	}
}

// --- Export Formats ---

// ExportOpenAI exports in OpenAI Chat format (for fine-tuning API).
func ExportOpenAI(dataset *Dataset, w *os.File) error {
	for _, s := range dataset.Samples {
		record := map[string]interface{}{
			"messages": []map[string]string{
				{"role": "system", "content": s.System},
				{"role": "user", "content": s.User},
				{"role": "assistant", "content": s.Assistant},
			},
		}
		if err := json.NewEncoder(w).Encode(record); err != nil {
			return fmt.Errorf("encode openai: %w", err)
		}
	}
	return nil
}

// ExportAlpaca exports in Alpaca format (for LLaMA-Factory / unsloth).
func ExportAlpaca(dataset *Dataset, w *os.File) error {
	for _, s := range dataset.Samples {
		record := map[string]string{
			"instruction": s.User,
			"input":       "",
			"output":      s.Assistant,
			"system":      s.System,
		}
		if err := json.NewEncoder(w).Encode(record); err != nil {
			return fmt.Errorf("encode alpaca: %w", err)
		}
	}
	return nil
}

// ExportShareGPT exports in ShareGPT format (for Axolotl).
func ExportShareGPT(dataset *Dataset, w *os.File) error {
	for _, s := range dataset.Samples {
		record := map[string]interface{}{
			"conversations": []map[string]string{
				{"from": "system", "value": s.System},
				{"from": "human", "value": s.User},
				{"from": "gpt", "value": s.Assistant},
			},
		}
		if err := json.NewEncoder(w).Encode(record); err != nil {
			return fmt.Errorf("encode sharegpt: %w", err)
		}
	}
	return nil
}

// ExportToFile exports a dataset to a file in the given format.
func ExportToFile(dataset *Dataset, path string, format string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	switch format {
	case "openai", "openai-chat":
		return ExportOpenAI(dataset, f)
	case "alpaca":
		return ExportAlpaca(dataset, f)
	case "sharegpt":
		return ExportShareGPT(dataset, f)
	default:
		return fmt.Errorf("unknown format: %s (supported: openai, alpaca, sharegpt)", format)
	}
}

// --- Train Config ---

// TrainConfig specifies fine-tuning parameters.
type TrainConfig struct {
	BaseModel    string  `yaml:"base_model" json:"base_model"`
	Method       string  `yaml:"method" json:"method"` // lora, qlora, full
	LoRARank     int     `yaml:"lora_rank" json:"lora_rank"`
	LoRAAlpha    int     `yaml:"lora_alpha" json:"lora_alpha"`
	LearningRate float64 `yaml:"learning_rate" json:"learning_rate"`
	NumEpochs    int     `yaml:"num_epochs" json:"num_epochs"`
	BatchSize    int     `yaml:"batch_size" json:"batch_size"`
	MaxSeqLength int     `yaml:"max_seq_length" json:"max_seq_length"`
	OutputDir    string  `yaml:"output_dir" json:"output_dir"`
}

// DefaultTrainConfig returns sensible defaults for QLoRA fine-tuning.
func DefaultTrainConfig() TrainConfig {
	return TrainConfig{
		Method:       "qlora",
		LoRARank:     16,
		LoRAAlpha:    32,
		LearningRate: 2e-4,
		NumEpochs:    3,
		BatchSize:    4,
		MaxSeqLength: 4096,
		OutputDir:    "./finetune_output",
	}
}

// --- A/B Testing ---

// ABTestResult compares old vs new model performance.
type ABTestResult struct {
	OldModel       string  `json:"old_model"`
	NewModel       string  `json:"new_model"`
	OldSuccessRate float64 `json:"old_success_rate"`
	NewSuccessRate float64 `json:"new_success_rate"`
	DeltaSuccess   float64 `json:"delta_success"`
	OldConfidence  float64 `json:"old_confidence"`
	NewConfidence  float64 `json:"new_confidence"`
	DeltaConfidence float64 `json:"delta_confidence"`
	Decision       DeployDecision `json:"decision"`
	TestedAt       time.Time      `json:"tested_at"`
}

// DeployDecision is the outcome of A/B testing.
type DeployDecision string

const (
	DeployPromote DeployDecision = "promote" // full rollout
	DeployShadow  DeployDecision = "shadow"  // shadow mode (parallel, no user impact)
	DeployReject  DeployDecision = "reject"  // keep old model
)

// ABTester evaluates a new model against the current one.
type ABTester struct {
	oldModel string
	minImprovement float64
}

// NewABTester creates an A/B tester.
func NewABTester(oldModel string) *ABTester {
	return &ABTester{
		oldModel:       oldModel,
		minImprovement: 0.05,
	}
}

// Decide determines whether to promote, shadow, or reject the new model.
func (ab *ABTester) Decide(oldSR, newSR, oldConf, newConf float64) *ABTestResult {
	delta := newSR - oldSR
	result := &ABTestResult{
		OldModel:        ab.oldModel,
		OldSuccessRate:  oldSR,
		NewSuccessRate:  newSR,
		DeltaSuccess:    delta,
		OldConfidence:   oldConf,
		NewConfidence:   newConf,
		DeltaConfidence: newConf - oldConf,
		TestedAt:        time.Now(),
	}

	switch {
	case delta > ab.minImprovement:
		result.Decision = DeployPromote
		slog.Info("finetune: promoting new model", "delta", fmt.Sprintf("%+.1f%%", delta*100))
	case delta > 0:
		result.Decision = DeployShadow
		slog.Info("finetune: shadow deploying new model", "delta", fmt.Sprintf("%+.1f%%", delta*100))
	default:
		result.Decision = DeployReject
		slog.Info("finetune: rejecting new model (no improvement)", "delta", fmt.Sprintf("%+.1f%%", delta*100))
	}

	return result
}

// --- Finetune Trigger ---

// TriggerConfig defines when automatic fine-tuning should fire.
type TriggerConfig struct {
	MinNewSamples      int           `yaml:"min_new_samples" json:"min_new_samples"`
	MinQualityThreshold float64      `yaml:"min_quality_threshold" json:"min_quality_threshold"`
	CooldownPeriod     time.Duration `yaml:"cooldown_period" json:"cooldown_period"`
	AutoDeployEnabled  bool          `yaml:"auto_deploy_enabled" json:"auto_deploy_enabled"`
}

// DefaultTriggerConfig returns sensible defaults.
func DefaultTriggerConfig() TriggerConfig {
	return TriggerConfig{
		MinNewSamples:      500,
		MinQualityThreshold: 0.75,
		CooldownPeriod:     7 * 24 * time.Hour,
		AutoDeployEnabled:  false,
	}
}

// ShouldTrigger checks whether fine-tuning conditions are met.
func (tc TriggerConfig) ShouldTrigger(newSamples int, avgQuality float64, lastRun time.Time) bool {
	if newSamples < tc.MinNewSamples {
		return false
	}
	if avgQuality < tc.MinQualityThreshold {
		return false
	}
	if time.Since(lastRun) < tc.CooldownPeriod {
		return false
	}
	return true
}

// --- Pipeline ---

// Pipeline orchestrates the full fine-tuning workflow.
type Pipeline struct {
	builder  *DatasetBuilder
	trigger  TriggerConfig
	lastRun  time.Time
}

// NewPipeline creates a new fine-tuning pipeline.
func NewPipeline(minQuality float64, maxSamples int, trigger TriggerConfig) *Pipeline {
	return &Pipeline{
		builder: NewDatasetBuilder(minQuality, maxSamples),
		trigger: trigger,
	}
}

// Run executes the full pipeline: build → export → trigger check.
func (p *Pipeline) Run(ctx context.Context, samples []*TrainingSample, exportPath, format string) (*Dataset, error) {
	dataset := p.builder.BuildDataset(samples)

	if exportPath != "" {
		if err := ExportToFile(dataset, exportPath, format); err != nil {
			return dataset, fmt.Errorf("export: %w", err)
		}
		slog.Info("finetune: dataset exported",
			"path", exportPath,
			"format", format,
			"samples", dataset.Stats.TotalSamples,
			"avg_quality", fmt.Sprintf("%.2f", dataset.Stats.AvgQuality),
		)
	}

	p.lastRun = time.Now()
	return dataset, nil
}

// LastRun returns the timestamp of the last pipeline execution.
func (p *Pipeline) LastRun() time.Time {
	return p.lastRun
}

// ShouldTrigger checks if enough new data has accumulated.
func (p *Pipeline) ShouldTrigger(newSamples int, avgQuality float64) bool {
	return p.trigger.ShouldTrigger(newSamples, avgQuality, p.lastRun)
}
