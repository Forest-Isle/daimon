package health

import (
	"context"
	"encoding/json"
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

// Status represents the health status of a single check.
type Status string

const (
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

// CheckResult holds the result of a single health check.
type CheckResult struct {
	Status     Status `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// FullReport is the JSON body for the /health endpoint.
type FullReport struct {
	Status Status                 `json:"status"`
	Checks map[string]CheckResult `json:"checks"`
}

// Registry aggregates multiple named health checkers.
type Registry struct {
	mu       sync.RWMutex
	checkers map[string]Checker
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		checkers: make(map[string]Checker),
	}
}

// Register adds a named checker to the registry.
// It overwrites any previously registered checker with the same name.
func (r *Registry) Register(name string, checker Checker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkers[name] = checker
}

// Unregister removes a named checker from the registry.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.checkers, name)
}

// Check runs all registered checkers and returns the results.
func (r *Registry) Check(ctx context.Context) FullReport {
	r.mu.RLock()
	names := make([]string, 0, len(r.checkers))
	checkers := make(map[string]Checker, len(r.checkers))
	for name, c := range r.checkers {
		names = append(names, name)
		checkers[name] = c
	}
	r.mu.RUnlock()

	report := FullReport{
		Status: StatusOK,
		Checks: make(map[string]CheckResult, len(checkers)),
	}

	for _, name := range names {
		c := checkers[name]
		start := time.Now()
		err := c.Check(ctx)
		elapsed := time.Since(start)

		result := CheckResult{
			Status:     StatusOK,
			DurationMs: elapsed.Milliseconds(),
		}
		if err != nil {
			result.Status = StatusError
			result.Error = err.Error()
			report.Status = StatusError
		}
		report.Checks[name] = result
	}

	return report
}

// Handler serves health check HTTP endpoints.
type Handler struct {
	registry *Registry
}

// NewHandler creates a new Handler backed by the given registry.
func NewHandler(registry *Registry) *Handler {
	return &Handler{registry: registry}
}

// ServeHTTP dispatches health check requests to the appropriate handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		h.handleLiveness(w, r)
	case "/readyz":
		h.handleReadiness(w, r)
	case "/health":
		h.handleHealth(w, r)
	default:
		http.NotFound(w, r)
	}
}

// RegisterRoutes registers health check endpoints on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", h.handleLiveness)
	mux.HandleFunc("/readyz", h.handleReadiness)
	mux.HandleFunc("/health", h.handleHealth)
}

// handleLiveness always returns 200 — the process is alive if it can serve HTTP.
func (h *Handler) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleReadiness runs all registered checks and returns 503 if any fail.
func (h *Handler) handleReadiness(w http.ResponseWriter, r *http.Request) {
	report := h.registry.Check(r.Context())

	statusCode := http.StatusOK
	if report.Status != StatusOK {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(report); err != nil {
		slog.Warn("health: failed to encode readiness response", "err", err)
	}
}

// handleHealth returns the full status of all checks as JSON.
func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	report := h.registry.Check(r.Context())

	statusCode := http.StatusOK
	if report.Status != StatusOK {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(report); err != nil {
		slog.Warn("health: failed to encode health response", "err", err)
	}
}
