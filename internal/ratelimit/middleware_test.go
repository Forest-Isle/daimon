package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// testLimiter is a controllable limiter for middleware testing.
type testLimiter struct {
	allowed  bool
	waitTime time.Duration
	err      error
}

func (l *testLimiter) Allow(_ context.Context, _ string) (bool, time.Duration, error) {
	return l.allowed, l.waitTime, l.err
}

func TestMiddleware_PassesThroughWhenAllowed(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	limiter := &testLimiter{allowed: true}
	mw := RateLimitMiddleware(limiter, nil)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("expected body 'ok', got '%s'", rec.Body.String())
	}
	// Headers should be set for allowed requests
	if rec.Header().Get("X-RateLimit-Remaining") != "1" {
		t.Fatalf("expected X-RateLimit-Remaining=1, got %s", rec.Header().Get("X-RateLimit-Remaining"))
	}
}

func TestMiddleware_Returns429WhenLimited(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler should not be called when rate limited")
	})

	limiter := &testLimiter{allowed: false, waitTime: 5 * time.Second}
	mw := RateLimitMiddleware(limiter, nil)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests, got %d", rec.Code)
	}
	if rec.Body.String() != "rate limit exceeded\n" {
		t.Fatalf("expected body 'rate limit exceeded', got '%s'", rec.Body.String())
	}
}

func TestMiddleware_SetsRetryAfterHeader(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler should not be called")
	})

	limiter := &testLimiter{allowed: false, waitTime: 5 * time.Second}
	mw := RateLimitMiddleware(limiter, nil)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	mw(handler).ServeHTTP(rec, req)

	retryAfter := rec.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected Retry-After header to be set")
	}
	if retryAfter != "5" {
		t.Fatalf("expected Retry-After=5, got %s", retryAfter)
	}
}

func TestMiddleware_MinRetryAfterOne(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler should not be called")
	})

	// Very short wait time — should be rounded up to 1 second
	limiter := &testLimiter{allowed: false, waitTime: 100 * time.Millisecond}
	mw := RateLimitMiddleware(limiter, nil)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	mw(handler).ServeHTTP(rec, req)

	retryAfter := rec.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected Retry-After header to be set")
	}
	if retryAfter != "1" {
		t.Fatalf("expected Retry-After=1 (minimum), got %s", retryAfter)
	}
}

func TestMiddleware_SetsXRateLimitReset(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler should not be called")
	})

	limiter := &testLimiter{allowed: false, waitTime: 10 * time.Second}
	mw := RateLimitMiddleware(limiter, nil)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	beforeCall := time.Now().Unix()
	mw(handler).ServeHTTP(rec, req)

	resetStr := rec.Header().Get("X-RateLimit-Reset")
	if resetStr == "" {
		t.Fatal("expected X-RateLimit-Reset header to be set")
	}

	// The reset timestamp should be in the future
	resetVal := int64(0)
	for _, c := range resetStr {
		resetVal = resetVal*10 + int64(c-'0')
	}
	if resetVal <= beforeCall {
		t.Fatal("expected X-RateLimit-Reset to be in the future")
	}
}

func TestMiddleware_UsesCustomKeyFunc(t *testing.T) {
	var capturedKey string
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The key was already consumed by the limiter; nothing extra to check here
		w.WriteHeader(http.StatusOK)
	})

	limiter := &testLimiter{allowed: true}
	keyFunc := func(r *http.Request) string {
		capturedKey = r.Header.Get("X-User-ID")
		return capturedKey
	}
	mw := RateLimitMiddleware(limiter, keyFunc)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-User-ID", "user-42")
	rec := httptest.NewRecorder()

	mw(handler).ServeHTTP(rec, req)

	if capturedKey != "user-42" {
		t.Fatalf("expected key 'user-42', got '%s'", capturedKey)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
}

func TestMiddleware_UsesRemoteAddrAsDefaultKey(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limiter := &testLimiter{allowed: true}
	mw := RateLimitMiddleware(limiter, nil)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	mw(handler).ServeHTTP(rec, req)

	// Should set the key header to RemoteAddr
	key := rec.Header().Get("X-RateLimit-Limit-Key")
	if key == "" {
		t.Fatal("expected X-RateLimit-Limit-Key to be set")
	}
}

func TestHeaderKeyFunc(t *testing.T) {
	keyFunc := HeaderKeyFunc("X-User-ID")

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-User-ID", "custom-user")
	req.RemoteAddr = "192.0.2.1:1234"

	key := keyFunc(req)
	if key != "custom-user" {
		t.Fatalf("expected key 'custom-user', got '%s'", key)
	}

	// Fallback to remote addr when header is empty
	reqWithout := httptest.NewRequest(http.MethodGet, "/test", nil)
	reqWithout.RemoteAddr = "192.0.2.2:5678"
	key = keyFunc(reqWithout)
	if key != "192.0.2.2:5678" {
		t.Fatalf("expected fallback to remote addr, got '%s'", key)
	}
}
