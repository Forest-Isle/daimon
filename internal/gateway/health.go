package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Checker is the interface that health checks must implement.
type Checker interface {
	Check(ctx context.Context) error
}

// CheckerFunc is an adapter to allow ordinary functions to be used as Checkers.
type CheckerFunc func(ctx context.Context) error

func (f CheckerFunc) Check(ctx context.Context) error {
	return f(ctx)
}

// HealthStatus represents the health status of a single check.
type HealthStatus string

const (
	HealthOK    HealthStatus = "ok"
	HealthError HealthStatus = "error"
)

// healthCheckResult holds the result of a single health check.
type healthCheckResult struct {
	Status     HealthStatus `json:"status"`
	DurationMs int64        `json:"duration_ms"`
	Error      string       `json:"error,omitempty"`
}

// healthReport is the JSON body for the /health endpoint.
type healthReport struct {
	Status HealthStatus                 `json:"status"`
	Checks map[string]healthCheckResult `json:"checks"`
}

// healthRegistry aggregates multiple named health checkers.
type healthRegistry struct {
	mu       sync.RWMutex
	checkers map[string]Checker
}

func newHealthRegistry() *healthRegistry {
	return &healthRegistry{
		checkers: make(map[string]Checker),
	}
}

func (r *healthRegistry) Register(name string, checker Checker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkers[name] = checker
}

func (r *healthRegistry) Check(ctx context.Context) healthReport {
	r.mu.RLock()
	names := make([]string, 0, len(r.checkers))
	checkers := make(map[string]Checker, len(r.checkers))
	for name, c := range r.checkers {
		names = append(names, name)
		checkers[name] = c
	}
	r.mu.RUnlock()

	report := healthReport{
		Status: HealthOK,
		Checks: make(map[string]healthCheckResult, len(checkers)),
	}

	for _, name := range names {
		c := checkers[name]
		start := time.Now()
		err := c.Check(ctx)
		elapsed := time.Since(start)

		result := healthCheckResult{
			Status:     HealthOK,
			DurationMs: elapsed.Milliseconds(),
		}
		if err != nil {
			result.Status = HealthError
			result.Error = err.Error()
			report.Status = HealthError
		}
		report.Checks[name] = result
	}

	return report
}

// startHealthServer starts a standalone HTTP server for health check endpoints
// (/healthz, /readyz, /health).
func (gw *Gateway) startHealthServer() {
	if gw.healthRegistry == nil {
		slog.Warn("health: registry not initialized, skipping health server")
		return
	}

	port := gw.Config().Health.Port
	if port <= 0 {
		port = 9090
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", gw.handleLiveness)
	mux.HandleFunc("/readyz", gw.handleReadiness)
	mux.HandleFunc("/health", gw.handleHealth)

	gw.healthSrv = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	go func() {
		slog.Info("health check server starting", "addr", gw.healthSrv.Addr)
		if err := gw.healthSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("health check server error", "err", err)
		}
	}()
}

func (gw *Gateway) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (gw *Gateway) handleReadiness(w http.ResponseWriter, r *http.Request) {
	report := gw.healthRegistry.Check(r.Context())
	statusCode := http.StatusOK
	if report.Status != HealthOK {
		statusCode = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(report); err != nil {
		slog.Warn("health: failed to encode readiness response", "err", err)
	}
}

func (gw *Gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	report := gw.healthRegistry.Check(r.Context())
	statusCode := http.StatusOK
	if report.Status != HealthOK {
		statusCode = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(report); err != nil {
		slog.Warn("health: failed to encode health response", "err", err)
	}
}
