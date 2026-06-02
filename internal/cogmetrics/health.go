package cogmetrics

import (
	"sync"
	"time"
)

// HealthStatus represents the overall cognitive health assessment.
type HealthStatus struct {
	Score       float64            `json:"score"`      // 0.0 (critical) to 1.0 (healthy)
	Indicators  map[string]float64 `json:"indicators"` // per-metric values
	Violations  []Violation        `json:"violations"` // active threshold violations
	LastChecked time.Time          `json:"last_checked"`
}

// Violation records a metric that exceeded its threshold.
type Violation struct {
	Metric    string  `json:"metric"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Action    string  `json:"action"`
}

// HealthChecker computes cognitive health from accumulated metrics.
type HealthChecker struct {
	mu      sync.RWMutex
	metrics map[string]*MetricWindow // sliding window per metric
	rules   []HealthRule
}

// MetricWindow tracks recent values for a metric.
type MetricWindow struct {
	Values  []float64
	Times   []time.Time
	MaxSize int
}

// NewMetricWindow creates a MetricWindow with the given capacity.
func NewMetricWindow(maxSize int) *MetricWindow {
	return &MetricWindow{MaxSize: maxSize}
}

// Add appends a value to the window, evicting the oldest if full.
func (w *MetricWindow) Add(value float64) {
	w.Values = append(w.Values, value)
	w.Times = append(w.Times, time.Now())
	if len(w.Values) > w.MaxSize {
		w.Values = w.Values[1:]
		w.Times = w.Times[1:]
	}
}

// Average returns the mean of all values in the window.
func (w *MetricWindow) Average() float64 {
	if len(w.Values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range w.Values {
		sum += v
	}
	return sum / float64(len(w.Values))
}

// Last returns the most recently added value, or 0 if empty.
func (w *MetricWindow) Last() float64 {
	if len(w.Values) == 0 {
		return 0
	}
	return w.Values[len(w.Values)-1]
}

// Count returns the number of values in the window.
func (w *MetricWindow) Count() int { return len(w.Values) }

// NewHealthChecker creates a health checker with default rules.
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		metrics: make(map[string]*MetricWindow),
		rules:   DefaultHealthRules(),
	}
}

// Record adds a metric observation.
func (h *HealthChecker) Record(metric string, value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	w, ok := h.metrics[metric]
	if !ok {
		w = NewMetricWindow(100)
		h.metrics[metric] = w
	}
	w.Add(value)
}

// Check evaluates all health rules and returns the current status.
func (h *HealthChecker) Check() HealthStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()

	status := HealthStatus{
		Score:       1.0,
		Indicators:  make(map[string]float64),
		LastChecked: time.Now(),
	}

	for metric, window := range h.metrics {
		status.Indicators[metric] = window.Last()
	}

	for _, rule := range h.rules {
		w, ok := h.metrics[rule.Metric]
		if !ok {
			continue
		}

		if rule.MinSamples > 0 && w.Count() < rule.MinSamples {
			continue
		}

		var value float64
		if rule.UseAverage {
			value = w.Average()
		} else {
			value = w.Last()
		}

		if rule.Exceeds(value) {
			status.Violations = append(status.Violations, Violation{
				Metric:    rule.Metric,
				Value:     value,
				Threshold: rule.Threshold,
				Action:    rule.Action.String(),
			})
			status.Score -= rule.Severity
			if status.Score < 0 {
				status.Score = 0
			}
		}
	}

	return status
}
