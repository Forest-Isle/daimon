package mind

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/Forest-Isle/daimon/internal/config"
)

// httpStatusError is the interface implemented by HTTP-status-carrying errors
// (e.g. Anthropic SDK errors). Matching by interface avoids a direct dependency
// on the SDK package.
type httpStatusError interface {
	StatusCode() int
}

// RetryProvider wraps a Provider and retries transient errors with
// exponential backoff.
type RetryProvider struct {
	inner Provider
	cfg   config.RetryConfig
	cb    *CircuitBreaker
}

// ErrCircuitOpen is returned when the circuit breaker is open and
// refusing requests to protect a failing upstream.
var ErrCircuitOpen = fmt.Errorf("circuit breaker open — upstream provider is failing")

// NewRetryProvider creates a RetryProvider wrapping inner with the given retry config.
func NewRetryProvider(inner Provider, cfg config.RetryConfig) *RetryProvider {
	return &RetryProvider{
		inner: inner,
		cfg:   cfg,
		cb:    NewCircuitBreaker(5, 30*time.Second), // 5 consecutive failures → open for 30s
	}
}

// GetTokenStats forwards cumulative token usage from the wrapped provider so
// callers (metrics, replay cost reporting) see usage through the retry wrapper.
// Returns zero when the inner provider does not track tokens.
func (r *RetryProvider) GetTokenStats() (input, output int64) {
	if ts, ok := r.inner.(interface {
		GetTokenStats() (int64, int64)
	}); ok {
		return ts.GetTokenStats()
	}
	return 0, 0
}

// Complete calls the inner provider's Complete, retrying on transient errors.
func (r *RetryProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if !r.cb.Allow() {
		slog.Warn("agent: circuit breaker open, rejecting LLM request", "state", r.cb.State().String())
		return nil, ErrCircuitOpen
	}

	var lastErr error
	for attempt := 0; attempt <= r.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := r.backoff(attempt - 1)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		resp, err := r.inner.Complete(ctx, req)
		if err == nil {
			r.cb.RecordSuccess()
			return resp, nil
		}
		lastErr = err

		if !isRetryable(err) {
			r.cb.RecordFailure()
			return nil, err
		}
	}
	r.cb.RecordFailure()
	return nil, lastErr
}

// Stream calls the inner provider's Stream, retrying on initial connection errors.
// Mid-stream errors are not retried (the iterator is returned as-is).
func (r *RetryProvider) Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error) {
	if !r.cb.Allow() {
		slog.Warn("agent: circuit breaker open, rejecting LLM stream request", "state", r.cb.State().String())
		return nil, ErrCircuitOpen
	}

	var lastErr error
	for attempt := 0; attempt <= r.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := r.backoff(attempt - 1)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		iter, err := r.inner.Stream(ctx, req)
		if err == nil {
			r.cb.RecordSuccess()
			return iter, nil
		}
		lastErr = err

		if !isRetryable(err) {
			r.cb.RecordFailure()
			return nil, err
		}
	}
	r.cb.RecordFailure()
	return nil, lastErr
}

// isRetryable returns true if the error should trigger a retry.
//
// Retryable:   HTTP 429, 500, 502, 503, 529; network/generic errors.
// Non-retryable: HTTP 400, 401, 403, 404; context cancellation/deadline.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// context errors are never retryable.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Check for HTTP status errors via interface.
	var se httpStatusError
	if errors.As(err, &se) {
		switch se.StatusCode() {
		case 429, 500, 502, 503, 529:
			return true
		default:
			return false
		}
	}

	// Generic (network) errors are retryable.
	return true
}

// backoff computes the delay for a given attempt (0-indexed) using exponential
// backoff with up to 25 % random jitter, capped at MaxDelay.
func (r *RetryProvider) backoff(attempt int) time.Duration {
	base := r.cfg.BaseDelay
	// Exponential: base * 2^attempt
	delay := base * (1 << uint(attempt))
	if delay > r.cfg.MaxDelay || delay <= 0 { // guard against overflow
		delay = r.cfg.MaxDelay
	}
	// Add 0–25 % jitter.
	jitter := time.Duration(rand.Float64() * 0.25 * float64(delay))
	return delay + jitter
}
