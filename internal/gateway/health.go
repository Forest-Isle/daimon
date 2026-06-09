package gateway

import (
	"context"
	"sync"
	"time"
)

type Checker interface { Check(ctx context.Context) error }
type CheckerFunc func(ctx context.Context) error
func (f CheckerFunc) Check(ctx context.Context) error { return f(ctx) }

type HealthStatus string
const ( HealthOK HealthStatus = "ok"; HealthError HealthStatus = "error" )

type healthCheckResult struct {
	Status     HealthStatus `json:"status"`
	DurationMs int64        `json:"duration_ms"`
	Error      string       `json:"error,omitempty"`
}

type healthReport struct {
	Status HealthStatus                 `json:"status"`
	Checks map[string]healthCheckResult `json:"checks"`
}

type healthRegistry struct {
	mu       sync.RWMutex
	checkers map[string]Checker
}

func newHealthRegistry() *healthRegistry { return &healthRegistry{checkers: make(map[string]Checker)} }

func (r *healthRegistry) Register(name string, checker Checker) {
	r.mu.Lock(); defer r.mu.Unlock()
	r.checkers[name] = checker
}

func (r *healthRegistry) Check(ctx context.Context) healthReport {
	r.mu.RLock()
	names := make([]string, 0, len(r.checkers))
	m := make(map[string]Checker, len(r.checkers))
	for n, c := range r.checkers { names = append(names, n); m[n] = c }
	r.mu.RUnlock()
	report := healthReport{Status: HealthOK, Checks: make(map[string]healthCheckResult, len(m))}
	for _, name := range names {
		c := m[name]
		start := time.Now()
		err := c.Check(ctx)
		elapsed := time.Since(start)
		result := healthCheckResult{Status: HealthOK, DurationMs: elapsed.Milliseconds()}
		if err != nil { result.Status = HealthError; result.Error = err.Error(); report.Status = HealthError }
		report.Checks[name] = result
	}
	return report
}
