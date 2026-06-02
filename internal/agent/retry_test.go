package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/config"
)

// httpError is a test-local HTTP error type that implements httpStatusError.
type httpError struct {
	statusCode int
}

func (e *httpError) Error() string   { return fmt.Sprintf("HTTP %d", e.statusCode) }
func (e *httpError) StatusCode() int { return e.statusCode }

// mockProvider is a test double for Provider.
type mockProvider struct {
	completeResponses []func() (*CompletionResponse, error)
	completeCallCount int

	streamResponses []func() (StreamIterator, error)
	streamCallCount int
}

func (m *mockProvider) Complete(_ context.Context, _ CompletionRequest) (*CompletionResponse, error) {
	if m.completeCallCount < len(m.completeResponses) {
		resp := m.completeResponses[m.completeCallCount]
		m.completeCallCount++
		return resp()
	}
	// Default: success with empty response.
	m.completeCallCount++
	return &CompletionResponse{}, nil
}

func (m *mockProvider) Stream(_ context.Context, _ CompletionRequest) (StreamIterator, error) {
	if m.streamCallCount < len(m.streamResponses) {
		resp := m.streamResponses[m.streamCallCount]
		m.streamCallCount++
		return resp()
	}
	m.streamCallCount++
	return &nopStreamIterator{}, nil
}

// nopStreamIterator is a no-op StreamIterator for tests.
type nopStreamIterator struct{}

func (n *nopStreamIterator) Next() (StreamDelta, error) { return StreamDelta{Done: true}, nil }
func (n *nopStreamIterator) Close()                     {}

// instantRetryConfig returns a RetryConfig with zero delays for fast tests.
func instantRetryConfig(maxRetries int) config.RetryConfig {
	return config.RetryConfig{
		MaxRetries: maxRetries,
		BaseDelay:  0,
		MaxDelay:   0,
	}
}

// ---------------------------------------------------------------------------
// TestIsRetryable
// ---------------------------------------------------------------------------

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "context.Canceled",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "context.DeadlineExceeded",
			err:  context.DeadlineExceeded,
			want: false,
		},
		{
			name: "wrapped context.Canceled",
			err:  fmt.Errorf("wrap: %w", context.Canceled),
			want: false,
		},
		{
			name: "HTTP 429 rate limit",
			err:  &httpError{429},
			want: true,
		},
		{
			name: "HTTP 500 internal server error",
			err:  &httpError{500},
			want: true,
		},
		{
			name: "HTTP 502 bad gateway",
			err:  &httpError{502},
			want: true,
		},
		{
			name: "HTTP 503 service unavailable",
			err:  &httpError{503},
			want: true,
		},
		{
			name: "HTTP 529 overloaded",
			err:  &httpError{529},
			want: true,
		},
		{
			name: "HTTP 400 bad request",
			err:  &httpError{400},
			want: false,
		},
		{
			name: "HTTP 401 unauthorized",
			err:  &httpError{401},
			want: false,
		},
		{
			name: "HTTP 403 forbidden",
			err:  &httpError{403},
			want: false,
		},
		{
			name: "HTTP 404 not found",
			err:  &httpError{404},
			want: false,
		},
		{
			name: "generic network error",
			err:  errors.New("connection reset by peer"),
			want: true,
		},
		{
			name: "wrapped HTTP 503",
			err:  fmt.Errorf("api: %w", &httpError{503}),
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isRetryable(tc.err)
			if got != tc.want {
				t.Errorf("isRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestRetryProviderCompleteRetriesOnTransientError
// ---------------------------------------------------------------------------

func TestRetryProviderCompleteRetriesOnTransientError(t *testing.T) {
	callCount := 0
	mock := &mockProvider{
		completeResponses: []func() (*CompletionResponse, error){
			func() (*CompletionResponse, error) {
				callCount++
				return nil, &httpError{503}
			},
			func() (*CompletionResponse, error) {
				callCount++
				return nil, &httpError{503}
			},
			func() (*CompletionResponse, error) {
				callCount++
				return &CompletionResponse{Text: "ok"}, nil
			},
		},
	}

	provider := NewRetryProvider(mock, instantRetryConfig(3))
	resp, err := provider.Complete(context.Background(), CompletionRequest{})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp.Text != "ok" {
		t.Errorf("expected response text 'ok', got %q", resp.Text)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

// ---------------------------------------------------------------------------
// TestRetryProviderCompleteNoRetryOnNonRetryable
// ---------------------------------------------------------------------------

func TestRetryProviderCompleteNoRetryOnNonRetryable(t *testing.T) {
	callCount := 0
	mock := &mockProvider{
		completeResponses: []func() (*CompletionResponse, error){
			func() (*CompletionResponse, error) {
				callCount++
				return nil, &httpError{401}
			},
			// This second response should never be reached.
			func() (*CompletionResponse, error) {
				callCount++
				return &CompletionResponse{Text: "should not happen"}, nil
			},
		},
	}

	provider := NewRetryProvider(mock, instantRetryConfig(3))
	_, err := provider.Complete(context.Background(), CompletionRequest{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if callCount != 1 {
		t.Errorf("expected exactly 1 call (no retry), got %d", callCount)
	}
	var he *httpError
	if !errors.As(err, &he) || he.StatusCode() != 401 {
		t.Errorf("expected HTTP 401 error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestRetryProviderCompleteExhaustsRetries
// ---------------------------------------------------------------------------

func TestRetryProviderCompleteExhaustsRetries(t *testing.T) {
	const maxRetries = 3
	callCount := 0
	transientErr := &httpError{500}

	// Build a provider that always returns a transient error.
	responses := make([]func() (*CompletionResponse, error), maxRetries+1)
	for i := range responses {
		responses[i] = func() (*CompletionResponse, error) {
			callCount++
			return nil, transientErr
		}
	}
	mock := &mockProvider{completeResponses: responses}

	provider := NewRetryProvider(mock, instantRetryConfig(maxRetries))
	_, err := provider.Complete(context.Background(), CompletionRequest{})
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	// Total attempts = 1 initial + maxRetries retries.
	expectedCalls := maxRetries + 1
	if callCount != expectedCalls {
		t.Errorf("expected %d calls, got %d", expectedCalls, callCount)
	}
}

// ---------------------------------------------------------------------------
// TestRetryProviderStreamRetries
// ---------------------------------------------------------------------------

func TestRetryProviderStreamRetries(t *testing.T) {
	callCount := 0
	successIter := &nopStreamIterator{}

	mock := &mockProvider{
		streamResponses: []func() (StreamIterator, error){
			func() (StreamIterator, error) {
				callCount++
				return nil, errors.New("connection refused")
			},
			func() (StreamIterator, error) {
				callCount++
				return nil, errors.New("connection refused")
			},
			func() (StreamIterator, error) {
				callCount++
				return successIter, nil
			},
		},
	}

	provider := NewRetryProvider(mock, instantRetryConfig(3))
	iter, err := provider.Stream(context.Background(), CompletionRequest{})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if iter != successIter {
		t.Error("expected the success iterator to be returned")
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

// ---------------------------------------------------------------------------
// TestRetryProviderBackoff
// ---------------------------------------------------------------------------

func TestRetryProviderBackoff(t *testing.T) {
	cfg := config.RetryConfig{
		MaxRetries: 3,
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   1 * time.Second,
	}
	p := NewRetryProvider(nil, cfg)

	// Run many times to verify bounds probabilistically.
	for i := 0; i < 1000; i++ {
		for attempt := 0; attempt < cfg.MaxRetries; attempt++ {
			d := p.backoff(attempt)
			base := cfg.BaseDelay * (1 << uint(attempt))
			if base > cfg.MaxDelay {
				base = cfg.MaxDelay
			}
			maxExpected := time.Duration(float64(base) * 1.25)
			if d < base {
				t.Errorf("attempt %d: backoff %v < base %v", attempt, d, base)
			}
			if d > maxExpected {
				t.Errorf("attempt %d: backoff %v > maxExpected %v", attempt, d, maxExpected)
			}
		}
	}
}
