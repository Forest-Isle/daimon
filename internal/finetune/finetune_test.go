package finetune

import (
		"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewDatasetBuilder(t *testing.T) {
	db := NewDatasetBuilder(0, 0)
	if db == nil {
		t.Fatal("expected non-nil DatasetBuilder")
	}
	if db.minQuality != 0.7 {
		t.Errorf("expected default minQuality 0.7, got %f", db.minQuality)
	}
	if db.maxSamples != 10000 {
		t.Errorf("expected default maxSamples 10000, got %d", db.maxSamples)
	}
}

func TestDatasetBuilder_BuildDataset(t *testing.T) {
	db := NewDatasetBuilder(0.5, 10)
	samples := []*TrainingSample{
		{ID: "1", User: "hello", Assistant: "hi", Metadata: SampleMetadata{QualityScore: 0.9, Category: "greeting", Complexity: "simple"}},
		{ID: "2", User: "bye", Assistant: "goodbye", Metadata: SampleMetadata{QualityScore: 0.3, Category: "farewell", Complexity: "simple"}},
		{ID: "3", User: "help", Assistant: "sure", Metadata: SampleMetadata{QualityScore: 0.7, Category: "support", Complexity: "medium"}},
	}

	dataset := db.BuildDataset(samples)
	if dataset == nil {
		t.Fatal("expected non-nil dataset")
	}

	// Only samples 1 and 3 have quality >= 0.5
	if dataset.Stats.TotalSamples != 2 {
		t.Fatalf("expected 2 samples after filtering, got %d", dataset.Stats.TotalSamples)
	}

	// Should be sorted by quality descending
	if dataset.Samples[0].ID != "1" {
		t.Errorf("expected first sample ID '1' (highest quality), got %q", dataset.Samples[0].ID)
	}
}

func TestDatasetBuilder_MaxSamples(t *testing.T) {
	db := NewDatasetBuilder(0, 3)
	samples := make([]*TrainingSample, 10)
	for i := range samples {
		samples[i] = &TrainingSample{
			ID:       itoa(i),
			User:     "q",
			Assistant: "a",
			Metadata: SampleMetadata{QualityScore: 0.9},
		}
	}

	dataset := db.BuildDataset(samples)
	if dataset.Stats.TotalSamples > 3 {
		t.Errorf("expected at most 3 samples, got %d", dataset.Stats.TotalSamples)
	}
}

func TestDatasetBuilder_EmptyInput(t *testing.T) {
	db := NewDatasetBuilder(0.5, 100)
	dataset := db.BuildDataset(nil)
	if dataset == nil {
		t.Fatal("expected non-nil dataset")
	}
	if dataset.Stats.TotalSamples != 0 {
		t.Errorf("expected 0 samples, got %d", dataset.Stats.TotalSamples)
	}
}

func TestDataset_Stats(t *testing.T) {
	db := NewDatasetBuilder(0, 100)
	samples := []*TrainingSample{
		{ID: "1", Metadata: SampleMetadata{QualityScore: 0.9, Category: "code", Complexity: "hard"}},
		{ID: "2", Metadata: SampleMetadata{QualityScore: 0.8, Category: "code", Complexity: "medium"}},
		{ID: "3", Metadata: SampleMetadata{QualityScore: 0.7, Category: "chat", Complexity: "simple"}},
	}
	dataset := db.BuildDataset(samples)

	if dataset.Stats.ByCategory["code"] != 2 {
		t.Errorf("expected 2 code samples, got %d", dataset.Stats.ByCategory["code"])
	}
	if dataset.Stats.ByCategory["chat"] != 1 {
		t.Errorf("expected 1 chat sample, got %d", dataset.Stats.ByCategory["chat"])
	}
}

func TestExportOpenAI(t *testing.T) {
	dataset := &Dataset{
		Samples: []*TrainingSample{
			{System: "You are a bot", User: "Hello", Assistant: "Hi there", Metadata: SampleMetadata{QualityScore: 0.9}},
		},
	}

	dir := t.TempDir()
	f, err := os.Create(dir + "/openai.jsonl")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	if err := ExportOpenAI(dataset, f); err != nil {
		t.Fatalf("ExportOpenAI: %v", err)
	}

	f.Seek(0, 0)
	var record map[string]interface{}
	if err := json.NewDecoder(f).Decode(&record); err != nil {
		t.Fatalf("decode: %v", err)
	}

	messages, ok := record["messages"].([]interface{})
	if !ok || len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %v", messages)
	}
}

func TestExportAlpaca(t *testing.T) {
	dataset := &Dataset{
		Samples: []*TrainingSample{
			{System: "You are a bot", User: "Hello", Assistant: "Hi", Metadata: SampleMetadata{QualityScore: 0.9}},
		},
	}

	dir := t.TempDir()
	f, err := os.Create(dir + "/alpaca.jsonl")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	if err := ExportAlpaca(dataset, f); err != nil {
		t.Fatalf("ExportAlpaca: %v", err)
	}

	f.Seek(0, 0)
	var record map[string]string
	if err := json.NewDecoder(f).Decode(&record); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if record["instruction"] != "Hello" {
		t.Errorf("expected instruction 'Hello', got %q", record["instruction"])
	}
	if record["output"] != "Hi" {
		t.Errorf("expected output 'Hi', got %q", record["output"])
	}
}

func TestExportShareGPT(t *testing.T) {
	dataset := &Dataset{
		Samples: []*TrainingSample{
			{System: "system prompt", User: "Hello", Assistant: "Hi", Metadata: SampleMetadata{QualityScore: 0.9}},
		},
	}

	dir := t.TempDir()
	f, err := os.Create(dir + "/sharegpt.jsonl")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	if err := ExportShareGPT(dataset, f); err != nil {
		t.Fatalf("ExportShareGPT: %v", err)
	}

	f.Seek(0, 0)
	var record map[string]interface{}
	if err := json.NewDecoder(f).Decode(&record); err != nil {
		t.Fatalf("decode: %v", err)
	}
	convs, ok := record["conversations"].([]interface{})
	if !ok || len(convs) != 3 {
		t.Fatalf("expected 3 conversations, got %v", convs)
	}
}

func TestExportToFile(t *testing.T) {
	dataset := &Dataset{
		Samples: []*TrainingSample{
			{System: "system", User: "user", Assistant: "assistant", Metadata: SampleMetadata{QualityScore: 0.9}},
		},
	}

	dir := t.TempDir()
	path := dir + "/dataset.jsonl"

	if err := ExportToFile(dataset, path, "openai"); err != nil {
		t.Fatalf("ExportToFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	if !strings.Contains(string(data), "user") {
		t.Error("exported file should contain user message")
	}
}

func TestExportToFile_UnknownFormat(t *testing.T) {
	dataset := &Dataset{Samples: []*TrainingSample{{System: "s", User: "u", Assistant: "a"}}}
	err := ExportToFile(dataset, "out.jsonl", "unknown_format")
	if err == nil {
		t.Error("expected error for unknown format")
	}
}

func TestDefaultTrainConfig(t *testing.T) {
	cfg := DefaultTrainConfig()
	if cfg.Method != "qlora" {
		t.Errorf("expected Method 'qlora', got %q", cfg.Method)
	}
	if cfg.LoRARank != 16 {
		t.Errorf("expected LoRARank 16, got %d", cfg.LoRARank)
	}
	if cfg.OutputDir != "./finetune_output" {
		t.Errorf("expected OutputDir './finetune_output', got %q", cfg.OutputDir)
	}
}

func TestABTester_New(t *testing.T) {
	ab := NewABTester("gpt-4")
	if ab == nil {
		t.Fatal("expected non-nil ABTester")
	}
	if ab.oldModel != "gpt-4" {
		t.Errorf("expected oldModel 'gpt-4', got %q", ab.oldModel)
	}
}

func TestABTester_Decide(t *testing.T) {
	ab := NewABTester("old-model")

	tests := []struct {
		name   string
		oldSR  float64
		newSR  float64
		oldC   float64
		newC   float64
		expect DeployDecision
	}{
		{"promote: significant improvement", 0.7, 0.8, 0.7, 0.8, DeployPromote},
		{"shadow: small improvement", 0.7, 0.72, 0.7, 0.72, DeployShadow},
		{"reject: no improvement", 0.7, 0.69, 0.7, 0.69, DeployReject},
		{"reject: same performance", 0.7, 0.7, 0.7, 0.7, DeployReject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ab.Decide(tt.oldSR, tt.newSR, tt.oldC, tt.newC)
			if result.Decision != tt.expect {
				t.Errorf("expected decision %s, got %s (delta=%f)", tt.expect, result.Decision, result.DeltaSuccess)
			}
		})
	}
}

func TestABTestResult_Fields(t *testing.T) {
	ab := NewABTester("gpt-4")
	result := ab.Decide(0.7, 0.8, 0.7, 0.8)
	if result.OldModel != "gpt-4" {
		t.Errorf("unexpected OldModel: %q", result.OldModel)
	}
	if result.NewModel != "" {
		t.Errorf("unexpected NewModel: %q", result.NewModel)
	}
	if result.DeltaSuccess <= 0 {
		t.Errorf("expected positive DeltaSuccess, got %f", result.DeltaSuccess)
	}
}

func TestDefaultTriggerConfig(t *testing.T) {
	tc := DefaultTriggerConfig()
	if tc.MinNewSamples != 500 {
		t.Errorf("expected MinNewSamples 500, got %d", tc.MinNewSamples)
	}
	if tc.MinQualityThreshold != 0.75 {
		t.Errorf("expected MinQualityThreshold 0.75, got %f", tc.MinQualityThreshold)
	}
	if tc.AutoDeployEnabled {
		t.Error("expected AutoDeployEnabled false")
	}
}

func TestTriggerConfig_ShouldTrigger(t *testing.T) {
	tc := DefaultTriggerConfig()

	tests := []struct {
		name        string
		newSamples  int
		avgQuality  float64
		lastRun     time.Time
		expect      bool
	}{
		{
			name:        "all conditions met",
			newSamples:  500,
			avgQuality:  0.8,
			lastRun:     time.Now().Add(-30 * 24 * time.Hour),
			expect:      true,
		},
		{
			name:        "not enough samples",
			newSamples:  100,
			avgQuality:  0.8,
			lastRun:     time.Now().Add(-30 * 24 * time.Hour),
			expect:      false,
		},
		{
			name:        "quality too low",
			newSamples:  500,
			avgQuality:  0.5,
			lastRun:     time.Now().Add(-30 * 24 * time.Hour),
			expect:      false,
		},
		{
			name:        "cooldown period not elapsed",
			newSamples:  500,
			avgQuality:  0.8,
			lastRun:     time.Now(),
			expect:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tc.ShouldTrigger(tt.newSamples, tt.avgQuality, tt.lastRun)
			if got != tt.expect {
				t.Errorf("ShouldTrigger = %v, want %v", got, tt.expect)
			}
		})
	}
}

func TestNewPipeline(t *testing.T) {
	p := NewPipeline(0.5, 100, DefaultTriggerConfig())
	if p == nil {
		t.Fatal("expected non-nil pipeline")
	}
	if p.builder.minQuality != 0.5 {
		t.Errorf("expected minQuality 0.5, got %f", p.builder.minQuality)
	}
}

func TestPipeline_Run(t *testing.T) {
	p := NewPipeline(0, 100, DefaultTriggerConfig())
	samples := []*TrainingSample{
		{System: "s", User: "u", Assistant: "a", Metadata: SampleMetadata{QualityScore: 0.9}},
	}
	dir := t.TempDir()
	path := dir + "/output.jsonl"

	dataset, err := p.Run(nil, samples, path, "openai")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if dataset == nil {
		t.Fatal("expected non-nil dataset")
	}
	if dataset.Stats.TotalSamples != 1 {
		t.Errorf("expected 1 sample, got %d", dataset.Stats.TotalSamples)
	}
}

func TestPipeline_Run_NoExport(t *testing.T) {
	p := NewPipeline(0, 100, DefaultTriggerConfig())
	samples := []*TrainingSample{
		{System: "s", User: "u", Assistant: "a", Metadata: SampleMetadata{QualityScore: 0.9}},
	}
	dataset, err := p.Run(nil, samples, "", "")
	if err != nil {
		t.Fatalf("Run (no export): %v", err)
	}
	if dataset.Stats.TotalSamples != 1 {
		t.Errorf("expected 1 sample, got %d", dataset.Stats.TotalSamples)
	}
}

func TestPipeline_ShouldTrigger(t *testing.T) {
	p := NewPipeline(0, 100, DefaultTriggerConfig())
	p.lastRun = time.Now().Add(-30 * 24 * time.Hour)

	if !p.ShouldTrigger(500, 0.8) {
		t.Error("expected ShouldTrigger true")
	}
	if p.ShouldTrigger(100, 0.8) {
		t.Error("expected ShouldTrigger false with few samples")
	}
}

func TestPipeline_LastRun(t *testing.T) {
	p := NewPipeline(0, 100, DefaultTriggerConfig())
	if !p.LastRun().IsZero() {
		t.Error("expected zero LastRun initially")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
