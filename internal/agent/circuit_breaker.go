package agent

import (
	"sync"
	"time"
)

// CircuitState represents the current state of the circuit breaker.
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // normal, requests flow through
	CircuitOpen                         // failing, requests rejected immediately
	CircuitHalfOpen                     // testing if upstream recovered
)

func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements the circuit breaker pattern for LLM provider calls.
// After consecutiveFailures consecutive failures, the circuit opens for openTimeout.
// In half-open state, a single probe request tests if the upstream recovered.
type CircuitBreaker struct {
	mu                  sync.Mutex
	state               CircuitState
	consecutiveFailures int
	failureThreshold    int
	openTimeout         time.Duration
	openedAt            time.Time
}

// NewCircuitBreaker creates a circuit breaker.
func NewCircuitBreaker(failureThreshold int, openTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:            CircuitClosed,
		failureThreshold: failureThreshold,
		openTimeout:      openTimeout,
	}
}

// Allow returns true if the request should proceed.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(cb.openedAt) >= cb.openTimeout {
			cb.state = CircuitHalfOpen
			return true
		}
		return false
	case CircuitHalfOpen:
		return false
	}
	return false
}

// RecordSuccess resets the circuit to closed.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitClosed
	cb.consecutiveFailures = 0
}

// RecordFailure increments failures and opens circuit if threshold reached.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFailures++
	switch cb.state {
	case CircuitClosed:
		if cb.consecutiveFailures >= cb.failureThreshold {
			cb.state = CircuitOpen
			cb.openedAt = time.Now()
		}
	case CircuitHalfOpen:
		cb.state = CircuitOpen
		cb.openedAt = time.Now()
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}
