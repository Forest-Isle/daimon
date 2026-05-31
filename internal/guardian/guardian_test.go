package guardian

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"
)

func TestNewDriftDetector(t *testing.T) {
	dd := NewDriftDetector(DefaultDriftConfig())
	if dd == nil {
		t.Fatal("expected non-nil DriftDetector")
	}
	if dd.window == nil {
		t.Error("expected window to be initialized")
	}
	if dd.config.WindowSize != 100 {
		t.Errorf("expected WindowSize 100, got %d", dd.config.WindowSize)
	}
}

func TestDefaultDriftConfig(t *testing.T) {
	cfg := DefaultDriftConfig()
	if cfg.WindowSize != 100 {
		t.Errorf("expected WindowSize 100, got %d", cfg.WindowSize)
	}
	if cfg.DriftThreshold != 2.0 {
		t.Errorf("expected DriftThreshold 2.0, got %f", cfg.DriftThreshold)
	}
	if cfg.CheckInterval != time.Hour {
		t.Errorf("expected CheckInterval 1h, got %v", cfg.CheckInterval)
	}
}

func TestSlidingWindow_Add(t *testing.T) {
	sw := NewSlidingWindow(5)
	if sw == nil {
		t.Fatal("expected non-nil SlidingWindow")
	}
	for i := 0; i < 3; i++ {
		sw.Add(QualitySample{Success: true, Confidence: 0.8})
	}
	samples := sw.Samples()
	if len(samples) != 3 {
		t.Errorf("expected 3 samples, got %d", len(samples))
	}
}

func TestSlidingWindow_Wraparound(t *testing.T) {
	sw := NewSlidingWindow(3)
	for i := 0; i < 5; i++ {
		sw.Add(QualitySample{Success: i%2 == 0, Confidence: 0.5})
	}
	samples := sw.Samples()
	if len(samples) != 3 {
		t.Errorf("expected 3 samples (ring buffer size), got %d", len(samples))
	}
}

func TestSlidingWindow_ConcurrentAccess(t *testing.T) {
	sw := NewSlidingWindow(100)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sw.Add(QualitySample{Success: true, Confidence: 0.9})
			sw.Samples()
		}()
	}
	wg.Wait()
	samples := sw.Samples()
	if len(samples) != 50 {
		t.Errorf("expected 50 samples, got %d", len(samples))
	}
}

func TestRecordSample(t *testing.T) {
	dd := NewDriftDetector(DefaultDriftConfig())
	dd.RecordSample(QualitySample{Success: true, Confidence: 0.9, DurationMs: 100})
	dd.RecordSample(QualitySample{Success: false, Confidence: 0.3, DurationMs: 500})

	samples := dd.window.Samples()
	if len(samples) != 2 {
		t.Errorf("expected 2 samples, got %d", len(samples))
	}
}

func TestSetBaseline(t *testing.T) {
	dd := NewDriftDetector(DefaultDriftConfig())
	samples := make([]QualitySample, 50)
	for i := range samples {
		samples[i] = QualitySample{
			Success:    i < 40, // 80% success rate
			Confidence: 0.7 + float64(i)/100.0,
		}
	}
	dd.SetBaseline(samples)

	dd.mu.RLock()
	baseline := dd.baseline
	dd.mu.RUnlock()

	if baseline == nil {
		t.Fatal("expected non-nil baseline")
	}
	if baseline.SampleCount != 50 {
		t.Errorf("expected SampleCount 50, got %d", baseline.SampleCount)
	}
	if math.Abs(baseline.SuccessRate-0.8) > 0.01 {
		t.Errorf("expected SuccessRate ~0.8, got %f", baseline.SuccessRate)
	}
}

func TestCheckDrift_InsufficientData(t *testing.T) {
	dd := NewDriftDetector(DefaultDriftConfig())
	// Fewer than 10 samples
	for i := 0; i < 5; i++ {
		dd.RecordSample(QualitySample{Success: true, Confidence: 0.8})
	}
	report := dd.CheckDrift()
	if report.Status != DriftStatusInsufficientData {
		t.Errorf("expected insufficient_data, got %s", report.Status)
	}
}

func TestCheckDrift_OK(t *testing.T) {
	dd := NewDriftDetector(DriftConfig{WindowSize: 50, DriftThreshold: 2.0, CheckInterval: time.Hour})

	// Set baseline with mixed successes to get meaningful stddev
	baselineSamples := make([]QualitySample, 50)
	for i := range baselineSamples {
		baselineSamples[i] = QualitySample{
			Success:         i < 45,
			Confidence:      0.7 + float64(i%5)*0.05,
			ToolSuccessRate: 0.8 + float64(i%3)*0.05,
		}
	}
	dd.SetBaseline(baselineSamples)

	// Recent window with similar stats
	for i := 0; i < 20; i++ {
		dd.RecordSample(QualitySample{
			Success:         i < 18,
			Confidence:      0.7 + float64(i%5)*0.05,
			ToolSuccessRate: 0.8 + float64(i%3)*0.05,
		})
	}

	report := dd.CheckDrift()
	if report.Status != DriftStatusOK {
		t.Errorf("expected OK, got %s (drift score: %f)", report.Status, report.DriftScore)
	}
}

func TestCheckDrift_Drifting(t *testing.T) {
	dd := NewDriftDetector(DriftConfig{WindowSize: 50, DriftThreshold: 0.5, CheckInterval: time.Hour})

	baselineSamples := make([]QualitySample, 50)
	for i := range baselineSamples {
		baselineSamples[i] = QualitySample{
			Success:         i < 48,
			Confidence:      0.8 + float64(i%5)*0.04,
			ToolSuccessRate: 0.85 + float64(i%3)*0.05,
		}
	}
	dd.SetBaseline(baselineSamples)

	// Recent window with poor performance
	for i := 0; i < 20; i++ {
		dd.RecordSample(QualitySample{
			Success:         i < 5,
			Confidence:      0.2 + float64(i%3)*0.05,
			ToolSuccessRate: 0.2 + float64(i%4)*0.05,
		})
	}

	report := dd.CheckDrift()
	if report.Status != DriftStatusDrifting && report.Status != DriftStatusCritical {
		t.Errorf("expected drifting or critical, got %s (score: %f)", report.Status, report.DriftScore)
	}
}

func TestCheckDrift_Critical(t *testing.T) {
	dd := NewDriftDetector(DriftConfig{WindowSize: 50, DriftThreshold: 0.3, CheckInterval: time.Hour})

	baselineSamples := make([]QualitySample, 50)
	for i := range baselineSamples {
		baselineSamples[i] = QualitySample{
			Success:         i < 48,
			Confidence:      0.8 + float64(i%5)*0.04,
			ToolSuccessRate: 0.85 + float64(i%3)*0.05,
		}
	}
	dd.SetBaseline(baselineSamples)

	for i := 0; i < 20; i++ {
		dd.RecordSample(QualitySample{
			Success:         false,
			Confidence:      0.05 + float64(i%3)*0.02,
			ToolSuccessRate: 0.05,
		})
	}

	report := dd.CheckDrift()
	if report.Status != DriftStatusCritical {
		t.Errorf("expected critical, got %s (score: %f)", report.Status, report.DriftScore)
	}
}

func TestAlertsChannel(t *testing.T) {
	dd := NewDriftDetector(DriftConfig{WindowSize: 50, DriftThreshold: 0.5, CheckInterval: time.Hour})

	baselineSamples := make([]QualitySample, 50)
	for i := range baselineSamples {
		baselineSamples[i] = QualitySample{
			Success:         i < 48,
			Confidence:      0.7 + float64(i%5)*0.05,
			ToolSuccessRate: 0.8 + float64(i%3)*0.05,
		}
	}
	dd.SetBaseline(baselineSamples)

	for i := 0; i < 20; i++ {
		dd.RecordSample(QualitySample{
			Success:         false,
			Confidence:      0.1 + float64(i%3)*0.02,
			ToolSuccessRate: 0.1,
			DurationMs:      5000,
		})
	}

	report := dd.CheckDrift()

	alerts := dd.Alerts()
	select {
	case alert := <-alerts:
		if alert.Severity == "" {
			t.Error("expected non-empty severity")
		}
		if alert.Report == nil {
			t.Error("expected report in alert")
		}
	default:
		if report.Status != DriftStatusOK && report.Status != DriftStatusInsufficientData {
			t.Error("expected alert for drifting status")
		}
	}
}

func TestComputeBaseline_Empty(t *testing.T) {
	b := computeBaseline(nil)
	if b != nil {
		t.Errorf("expected nil for empty input, got %+v", b)
	}
}

func TestComputeBaseline(t *testing.T) {
	samples := []QualitySample{
		{Success: true, Confidence: 0.9, ToolSuccessRate: 1.0, ReplanCount: 0},
		{Success: true, Confidence: 0.8, ToolSuccessRate: 0.8, ReplanCount: 1},
		{Success: false, Confidence: 0.3, ToolSuccessRate: 0.5, ReplanCount: 2},
	}
	b := computeBaseline(samples)
	if b == nil {
		t.Fatal("expected non-nil baseline")
	}
	if math.Abs(b.SuccessRate-2.0/3.0) > 0.01 {
		t.Errorf("expected SuccessRate ~0.667, got %f", b.SuccessRate)
	}
	if math.Abs(b.MeanConfidence-(0.9+0.8+0.3)/3.0) > 0.01 {
		t.Errorf("unexpected MeanConfidence: %f", b.MeanConfidence)
	}
	if b.SampleCount != 3 {
		t.Errorf("expected SampleCount 3, got %d", b.SampleCount)
	}
}

func TestComputeSnapshot_Empty(t *testing.T) {
	s := computeSnapshot(nil)
	if s == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if s.SampleCount != 0 {
		t.Errorf("expected 0 SampleCount, got %d", s.SampleCount)
	}
}

func TestComputeSnapshot(t *testing.T) {
	samples := []QualitySample{
		{Success: true, Confidence: 0.9, ToolSuccessRate: 1.0, DurationMs: 100},
		{Success: false, Confidence: 0.5, ToolSuccessRate: 0.5, DurationMs: 200},
	}
	s := computeSnapshot(samples)
	if s.SampleCount != 2 {
		t.Errorf("expected SampleCount 2, got %d", s.SampleCount)
	}
	if s.SuccessRate != 0.5 {
		t.Errorf("expected SuccessRate 0.5, got %f", s.SuccessRate)
	}
}

func TestZScore(t *testing.T) {
	if zScore(10, 5, 2) != 2.5 {
		t.Errorf("expected 2.5, got %f", zScore(10, 5, 2))
	}
	if zScore(5, 5, 2) != 0 {
		t.Errorf("expected 0, got %f", zScore(5, 5, 2))
	}
	if zScore(10, 5, 0) != 0 {
		t.Errorf("expected 0 for zero stddev, got %f", zScore(10, 5, 0))
	}
}

func TestNewOnlineJudge(t *testing.T) {
	oj := NewOnlineJudge(func(ctx context.Context, system, user string) (string, error) {
		return `{"overall_score":0.8}`, nil
	})
	if oj == nil {
		t.Fatal("expected non-nil OnlineJudge")
	}
}

func TestOnlineJudge_Evaluate(t *testing.T) {
	oj := NewOnlineJudge(func(ctx context.Context, system, user string) (string, error) {
		return `{"scores":{"accuracy":0.9,"completeness":0.8,"efficiency":0.7,"helpfulness":0.9},"overall_score":0.825,"strengths":["clear"],"weaknesses":["slow"],"is_hallucination":false,"explanation":"Good job"}`, nil
	})

	result, err := oj.Evaluate(context.Background(), &JudgeSession{
		SessionID:   "test-session",
		UserRequest: "Do something",
		FinalAnswer: "Done",
		Complexity:  "medium",
		DurationMs:  1000,
		ToolsUsed:   []string{"bash"},
		ReplanCount: 0,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.OverallScore != 0.825 {
		t.Errorf("expected OverallScore 0.825, got %f", result.OverallScore)
	}
	if result.SessionID != "test-session" {
		t.Errorf("expected SessionID 'test-session', got %q", result.SessionID)
	}
}

func TestOnlineJudge_Evaluate_LLMError(t *testing.T) {
	oj := NewOnlineJudge(func(ctx context.Context, system, user string) (string, error) {
		return "", assertErrorf("llm error")
	})

	_, err := oj.Evaluate(context.Background(), &JudgeSession{
		SessionID:   "test",
		UserRequest: "hi",
		FinalAnswer: "hello",
	})
	if err == nil {
		t.Error("expected error from LLM failure")
	}
}

func TestOnlineJudge_Evaluate_BadJSON(t *testing.T) {
	oj := NewOnlineJudge(func(ctx context.Context, system, user string) (string, error) {
		return "not valid json", nil
	})

	result, err := oj.Evaluate(context.Background(), &JudgeSession{
		SessionID:   "test",
		UserRequest: "hi",
		FinalAnswer: "hello",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v (bad JSON should be tolerated)", err)
	}
	if result.OverallScore != 0.5 {
		t.Errorf("expected fallback OverallScore 0.5, got %f", result.OverallScore)
	}
}

func TestOnlineJudge_ShouldSample(t *testing.T) {
	oj := NewOnlineJudge(nil)
	if !oj.ShouldSample() {
		t.Error("ShouldSample should return true")
	}
}

func TestNewRegressionGuard(t *testing.T) {
	dd := NewDriftDetector(DefaultDriftConfig())
	rg := NewRegressionGuard(dd, true)
	if rg == nil {
		t.Fatal("expected non-nil RegressionGuard")
	}
	if !rg.autoRollback {
		t.Error("expected autoRollback true")
	}
}

func TestRegressionGuard_CheckAfterChange(t *testing.T) {
	dd := NewDriftDetector(DriftConfig{WindowSize: 50, DriftThreshold: 2.0, CheckInterval: time.Hour})

	baselineSamples := make([]QualitySample, 50)
	for i := range baselineSamples {
		baselineSamples[i] = QualitySample{
			Success:         i < 45,
			Confidence:      0.7 + float64(i%5)*0.05,
			ToolSuccessRate: 0.8 + float64(i%3)*0.05,
		}
	}
	dd.SetBaseline(baselineSamples)

	for i := 0; i < 20; i++ {
		dd.RecordSample(QualitySample{
			Success:         i < 18,
			Confidence:      0.7 + float64(i%5)*0.05,
			ToolSuccessRate: 0.8 + float64(i%3)*0.05,
		})
	}

	rg := NewRegressionGuard(dd, false)
	report, ok := rg.CheckAfterChange("test change")
	if !ok {
		t.Errorf("expected OK for similar performance, got drift score %f", report.DriftScore)
	}
}

func assertErrorf(msg string) error {
	return &simpleError{msg: msg}
}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }
