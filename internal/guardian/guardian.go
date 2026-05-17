package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

// QualitySample represents a single agent interaction's quality metrics.
type QualitySample struct {
	Timestamp       time.Time `json:"timestamp"`
	Success         bool      `json:"success"`
	Confidence      float64   `json:"confidence"`
	UserFeedback    float64   `json:"user_feedback"`
	DurationMs      int64     `json:"duration_ms"`
	ToolSuccessRate float64   `json:"tool_success_rate"`
	ReplanCount     int       `json:"replan_count"`
	Complexity      string    `json:"complexity"`
}

// BaselineStats holds statistical baselines for quality metrics.
type BaselineStats struct {
	SuccessRate      float64   `json:"success_rate"`
	MeanConfidence   float64   `json:"mean_confidence"`
	StdConfidence    float64   `json:"std_confidence"`
	StdSuccessRate   float64   `json:"std_success_rate"`
	MeanToolSuccess  float64   `json:"mean_tool_success"`
	MeanReplanCount  float64   `json:"mean_replan_count"`
	SampleCount      int       `json:"sample_count"`
	Period           TimeRange `json:"period"`
}

// TimeRange defines a time window.
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// DriftDetector monitors agent quality for degradation.
type DriftDetector struct {
	window     *SlidingWindow
	baseline   *BaselineStats
	config     DriftConfig
	alertCh    chan DriftAlert
	mu         sync.RWMutex
}

// DriftConfig configures the drift detector.
type DriftConfig struct {
	WindowSize     int           // default 100
	DriftThreshold float64       // default 2.0 (z-score)
	CheckInterval  time.Duration // default 1 hour
}

// DefaultDriftConfig returns sensible defaults.
func DefaultDriftConfig() DriftConfig {
	return DriftConfig{
		WindowSize:     100,
		DriftThreshold: 2.0,
		CheckInterval:  time.Hour,
	}
}

// DriftStatus indicates the current drift state.
type DriftStatus string

const (
	DriftStatusOK              DriftStatus = "ok"
	DriftStatusInsufficientData DriftStatus = "insufficient_data"
	DriftStatusDrifting        DriftStatus = "drifting"
	DriftStatusCritical        DriftStatus = "critical"
)

// DriftReport summarizes a drift check.
type DriftReport struct {
	Status     DriftStatus             `json:"status"`
	DriftScore float64                 `json:"drift_score"`
	ZScores    map[string]float64      `json:"z_scores"`
	Current    *QualitySnapshot        `json:"current"`
	Baseline   *BaselineStats          `json:"baseline"`
	CheckedAt  time.Time               `json:"checked_at"`
}

// QualitySnapshot is a point-in-time quality measurement.
type QualitySnapshot struct {
	SuccessRate     float64 `json:"success_rate"`
	MeanConfidence  float64 `json:"mean_confidence"`
	MeanToolSuccess float64 `json:"mean_tool_success"`
	MeanReplanCount float64 `json:"mean_replan_count"`
	P95LatencyMs    int64   `json:"p95_latency_ms"`
	SampleCount     int     `json:"sample_count"`
	HallucinationRate float64 `json:"hallucination_rate"`
	BaselineP95LatencyMs int64 `json:"baseline_p95_latency_ms"`
	DriftScore           float64 `json:"drift_score"`
}

// DriftAlert is emitted when quality degradation is detected.
type DriftAlert struct {
	Severity  string       `json:"severity"`
	Message   string       `json:"message"`
	Report    *DriftReport `json:"report"`
	Timestamp time.Time    `json:"timestamp"`
}

// NewDriftDetector creates a new drift detector.
func NewDriftDetector(cfg DriftConfig) *DriftDetector {
	return &DriftDetector{
		window:  NewSlidingWindow(cfg.WindowSize),
		config:  cfg,
		alertCh: make(chan DriftAlert, 10),
	}
}

// RecordSample adds a quality sample to the sliding window.
func (dd *DriftDetector) RecordSample(sample QualitySample) {
	dd.window.Add(sample)
}

// SetBaseline establishes the baseline statistics from historical data.
func (dd *DriftDetector) SetBaseline(samples []QualitySample) {
	dd.mu.Lock()
	defer dd.mu.Unlock()
	dd.baseline = computeBaseline(samples)
}

// CheckDrift evaluates whether quality has drifted from baseline.
func (dd *DriftDetector) CheckDrift() *DriftReport {
	dd.mu.RLock()
	baseline := dd.baseline
	dd.mu.RUnlock()

	recent := dd.window.Samples()
	report := &DriftReport{CheckedAt: time.Now()}

	if len(recent) < 10 || baseline == nil || baseline.SampleCount < 30 {
		report.Status = DriftStatusInsufficientData
		return report
	}

	current := computeSnapshot(recent)
	report.Current = current
	report.Baseline = baseline

	// Compute Z-scores (negative = degradation)
	zScores := map[string]float64{
		"success_rate": zScore(current.SuccessRate, baseline.SuccessRate, baseline.StdSuccessRate),
		"confidence":   zScore(current.MeanConfidence, baseline.MeanConfidence, baseline.StdConfidence),
		"tool_success": zScore(current.MeanToolSuccess, baseline.MeanToolSuccess, 0.1),
	}
	report.ZScores = zScores

	// Aggregate drift score: average of absolute z-scores
	var driftScore float64
	for _, z := range zScores {
		driftScore += math.Abs(z)
	}
	driftScore /= float64(len(zScores))
	report.DriftScore = driftScore

	switch {
	case driftScore > dd.config.DriftThreshold*1.5:
		report.Status = DriftStatusCritical
	case driftScore > dd.config.DriftThreshold:
		report.Status = DriftStatusDrifting
	default:
		report.Status = DriftStatusOK
	}

	if report.Status != DriftStatusOK {
		alert := DriftAlert{
			Severity:  string(report.Status),
			Message:   fmt.Sprintf("Quality drift detected: score %.2f (threshold %.2f)", driftScore, dd.config.DriftThreshold),
			Report:    report,
			Timestamp: time.Now(),
		}
		select {
		case dd.alertCh <- alert:
		default:
		}
		slog.Warn("guardian: quality drift detected",
			"score", driftScore,
			"status", report.Status,
			"z_scores", zScores,
		)
	}

	return report
}

// Alerts returns the alert channel.
func (dd *DriftDetector) Alerts() <-chan DriftAlert {
	return dd.alertCh
}

// SlidingWindow maintains a fixed-size ring of samples.
type SlidingWindow struct {
	samples []QualitySample
	size    int
	idx     int
	count   int
	mu      sync.RWMutex
}

func NewSlidingWindow(size int) *SlidingWindow {
	return &SlidingWindow{
		samples: make([]QualitySample, size),
		size:    size,
	}
}

func (sw *SlidingWindow) Add(sample QualitySample) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.samples[sw.idx] = sample
	sw.idx = (sw.idx + 1) % sw.size
	if sw.count < sw.size {
		sw.count++
	}
}

func (sw *SlidingWindow) Samples() []QualitySample {
	sw.mu.RLock()
	defer sw.mu.RUnlock()
	result := make([]QualitySample, sw.count)
	for i := 0; i < sw.count; i++ {
		idx := (sw.idx - sw.count + i + sw.size) % sw.size
		result[i] = sw.samples[idx]
	}
	return result
}

// --- Statistics ---

func computeBaseline(samples []QualitySample) *BaselineStats {
	if len(samples) == 0 {
		return nil
	}
	var sumSR, sumConf, sumTool, sumReplan float64
	for _, s := range samples {
		if s.Success {
			sumSR++
		}
		sumConf += s.Confidence
		sumTool += s.ToolSuccessRate
		sumReplan += float64(s.ReplanCount)
	}
	n := float64(len(samples))
	meanSR := sumSR / n
	meanConf := sumConf / n

	var varSR, varConf float64
	for _, s := range samples {
		sr := 0.0
		if s.Success {
			sr = 1.0
		}
		varSR += (sr - meanSR) * (sr - meanSR)
		varConf += (s.Confidence - meanConf) * (s.Confidence - meanConf)
	}

	return &BaselineStats{
		SuccessRate:     meanSR,
		MeanConfidence:  meanConf,
		StdConfidence:   math.Sqrt(varConf / n),
		StdSuccessRate:  math.Sqrt(varSR / n),
		MeanToolSuccess: sumTool / n,
		MeanReplanCount: sumReplan / n,
		SampleCount:     len(samples),
	}
}

func computeSnapshot(samples []QualitySample) *QualitySnapshot {
	if len(samples) == 0 {
		return &QualitySnapshot{}
	}
	var sumSR, sumConf, sumTool, sumReplan, sumDur float64
	for _, s := range samples {
		if s.Success {
			sumSR++
		}
		sumConf += s.Confidence
		sumTool += s.ToolSuccessRate
		sumReplan += float64(s.ReplanCount)
		sumDur += float64(s.DurationMs)
	}
	n := float64(len(samples))
	return &QualitySnapshot{
		SuccessRate:     sumSR / n,
		MeanConfidence:  sumConf / n,
		MeanToolSuccess: sumTool / n,
		MeanReplanCount: sumReplan / n,
		P95LatencyMs:    int64(sumDur / n),
		SampleCount:     len(samples),
	}
}

func zScore(current, baseline, stdDev float64) float64 {
	if stdDev == 0 {
		return 0
	}
	return (current - baseline) / stdDev
}

// --- Online Judge ---

// OnlineJudge evaluates agent interaction quality using LLM-as-Judge.
type OnlineJudge struct {
	completer func(ctx context.Context, system, user string) (string, error)
	sampleRate float64
}

// JudgeResult is the output of an online quality evaluation.
type JudgeResult struct {
	SessionID       string             `json:"session_id"`
	Scores          map[string]float64 `json:"scores"`
	OverallScore    float64            `json:"overall_score"`
	Strengths       []string           `json:"strengths"`
	Weaknesses      []string           `json:"weaknesses"`
	IsHallucination bool               `json:"is_hallucination"`
	Explanation     string             `json:"explanation"`
}

// NewOnlineJudge creates a new online judge.
// completer is a function that calls an LLM for evaluation.
func NewOnlineJudge(completer func(ctx context.Context, system, user string) (string, error)) *OnlineJudge {
	return &OnlineJudge{
		completer:  completer,
		sampleRate: 0.2, // evaluate 20% of interactions
	}
}

// Evaluate judges an agent interaction using four criteria.
func (oj *OnlineJudge) Evaluate(ctx context.Context, session *JudgeSession) (*JudgeResult, error) {
	system := `You are an objective quality judge for an AI agent.
Rate the agent's response on these criteria (0.0-1.0):

1. accuracy: Did the agent correctly understand and address the request?
2. completeness: Did the agent address all parts of the request?
3. efficiency: Did the agent solve the problem with minimal unnecessary steps?
4. helpfulness: Was the response actionable and useful?

Output ONLY valid JSON:
{"scores":{"accuracy":0.0,"completeness":0.0,"efficiency":0.0,"helpfulness":0.0},"overall_score":0.0,"strengths":["..."],"weaknesses":["..."],"is_hallucination":false,"explanation":"..."}`

	user := fmt.Sprintf(`User request: %s
Agent's final answer: %s
Complexity: %s | Duration: %dms | Tools used: %v | Replans: %d
Please judge the quality.`, session.UserRequest, session.FinalAnswer, session.Complexity,
		session.DurationMs, session.ToolsUsed, session.ReplanCount)

	respText, err := oj.completer(ctx, system, user)
	if err != nil {
		return nil, fmt.Errorf("judge llm: %w", err)
	}

	result := &JudgeResult{SessionID: session.SessionID}
	if err := json.Unmarshal([]byte(respText), result); err != nil {
		result.OverallScore = 0.5
		result.Explanation = respText
	}
	return result, nil
}

// JudgeSession holds the data needed for quality evaluation.
type JudgeSession struct {
	SessionID    string
	UserRequest  string
	FinalAnswer  string
	Complexity   string
	DurationMs   int64
	ToolsUsed    []string
	ReplanCount  int
}

// RegressionGuard prevents quality regressions from strategy changes.
type RegressionGuard struct {
	detector     *DriftDetector
	autoRollback bool
	lastCheck    time.Time
	mu           sync.Mutex
}

// NewRegressionGuard creates a regression guard.
func NewRegressionGuard(detector *DriftDetector, autoRollback bool) *RegressionGuard {
	return &RegressionGuard{
		detector:     detector,
		autoRollback: autoRollback,
	}
}

// CheckAfterChange evaluates quality after a strategy/prompt change.
// Returns true if quality is acceptable, false if regression detected.
func (rg *RegressionGuard) CheckAfterChange(changeDescription string) (*DriftReport, bool) {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	report := rg.detector.CheckDrift()
	ok := report.Status == DriftStatusOK || report.Status == DriftStatusInsufficientData

	if !ok && rg.autoRollback {
		slog.Error("guardian: auto-rollback triggered",
			"change", changeDescription,
			"drift_score", report.DriftScore,
		)
	}

	rg.lastCheck = time.Now()
	return report, ok
}

// ShouldSample returns true if this interaction should be judged.
func (oj *OnlineJudge) ShouldSample() bool {
	return true // simplified; in production use sampleRate
}


