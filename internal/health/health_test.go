package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLivenessReturns200(t *testing.T) {
	reg := NewRegistry()
	handler := NewHandler(reg)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", body["status"])
	}
}

func TestReadinessAllPassingReturns200(t *testing.T) {
	reg := NewRegistry()
	reg.Register("db", CheckerFunc(func(ctx context.Context) error {
		return nil
	}))
	reg.Register("cache", CheckerFunc(func(ctx context.Context) error {
		return nil
	}))

	handler := NewHandler(reg)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var report FullReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if report.Status != StatusOK {
		t.Errorf("expected status 'ok', got %q", report.Status)
	}
	if len(report.Checks) != 2 {
		t.Errorf("expected 2 checks, got %d", len(report.Checks))
	}
	for name, result := range report.Checks {
		if result.Status != StatusOK {
			t.Errorf("check %q expected status 'ok', got %q", name, result.Status)
		}
	}
}

func TestReadinessFailingCheckReturns503(t *testing.T) {
	reg := NewRegistry()
	reg.Register("db", CheckerFunc(func(ctx context.Context) error {
		return nil
	}))
	reg.Register("bad-service", CheckerFunc(func(ctx context.Context) error {
		return errors.New("connection refused")
	}))

	handler := NewHandler(reg)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}

	var report FullReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if report.Status != StatusError {
		t.Errorf("expected status 'error', got %q", report.Status)
	}

	// db should still be ok
	if report.Checks["db"].Status != StatusOK {
		t.Errorf("check 'db' expected status 'ok', got %q", report.Checks["db"].Status)
	}

	// bad-service should be error
	if report.Checks["bad-service"].Status != StatusError {
		t.Errorf("check 'bad-service' expected status 'error', got %q", report.Checks["bad-service"].Status)
	}
	if report.Checks["bad-service"].Error != "connection refused" {
		t.Errorf("check 'bad-service' expected error 'connection refused', got %q", report.Checks["bad-service"].Error)
	}
}

func TestHealthEndpointReturnsAllChecks(t *testing.T) {
	reg := NewRegistry()
	reg.Register("database", CheckerFunc(func(ctx context.Context) error {
		return nil
	}))
	reg.Register("redis", CheckerFunc(func(ctx context.Context) error {
		return errors.New("timeout")
	}))

	handler := NewHandler(reg)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}

	var report FullReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if report.Status != StatusError {
		t.Errorf("expected status 'error', got %q", report.Status)
	}
	if len(report.Checks) != 2 {
		t.Errorf("expected 2 checks, got %d", len(report.Checks))
	}
	if report.Checks["database"].Status != StatusOK {
		t.Errorf("database should be ok")
	}
	if report.Checks["redis"].Status != StatusError {
		t.Errorf("redis should be error")
	}
	if report.Checks["redis"].Error != "timeout" {
		t.Errorf("redis error should be 'timeout', got %q", report.Checks["redis"].Error)
	}
}

func TestHealthAllPassingReturns200(t *testing.T) {
	reg := NewRegistry()
	reg.Register("database", CheckerFunc(func(ctx context.Context) error {
		return nil
	}))
	reg.Register("docker", CheckerFunc(func(ctx context.Context) error {
		return nil
	}))

	handler := NewHandler(reg)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var report FullReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if report.Status != StatusOK {
		t.Errorf("expected status 'ok', got %q", report.Status)
	}
	if len(report.Checks) != 2 {
		t.Errorf("expected 2 checks, got %d", len(report.Checks))
	}
}

func TestRegistryUnregister(t *testing.T) {
	reg := NewRegistry()
	reg.Register("db", CheckerFunc(func(ctx context.Context) error {
		return errors.New("fail")
	}))

	report := reg.Check(context.Background())
	if report.Status != StatusError {
		t.Fatalf("expected error status")
	}

	reg.Unregister("db")

	report = reg.Check(context.Background())
	if report.Status != StatusOK {
		t.Errorf("expected ok status after unregister, got %q", report.Status)
	}
	if len(report.Checks) != 0 {
		t.Errorf("expected 0 checks, got %d", len(report.Checks))
	}
}

func TestRegisterRoutes(t *testing.T) {
	reg := NewRegistry()
	handler := NewHandler(reg)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// /healthz should be registered
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("healthz expected 200, got %d", rec.Code)
	}

	// /readyz should be registered
	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("readyz expected 200, got %d", rec.Code)
	}

	// /health should be registered
	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("health expected 200, got %d", rec.Code)
	}
}

func TestEmptyRegistry(t *testing.T) {
	reg := NewRegistry()
	report := reg.Check(context.Background())

	if report.Status != StatusOK {
		t.Errorf("empty registry should report 'ok', got %q", report.Status)
	}
	if len(report.Checks) != 0 {
		t.Errorf("empty registry should have 0 checks, got %d", len(report.Checks))
	}
}

func TestCheckerFuncAdapter(t *testing.T) {
	called := false
	c := CheckerFunc(func(ctx context.Context) error {
		called = true
		return nil
	})

	if err := c.Check(context.Background()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected CheckerFunc to be called")
	}
}

func TestDurationMsIsSet(t *testing.T) {
	reg := NewRegistry()
	reg.Register("fast", CheckerFunc(func(ctx context.Context) error {
		return nil
	}))

	report := reg.Check(context.Background())

	result := report.Checks["fast"]
	if result.DurationMs < 0 {
		t.Errorf("duration_ms should be >= 0, got %d", result.DurationMs)
	}
}
