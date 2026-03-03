package agent

import (
	"fmt"
	"sync"
	"time"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"    // normal operation
	CircuitOpen     CircuitState = "open"      // failing, reject requests
	CircuitHalfOpen CircuitState = "half_open" // testing recovery
)

// CircuitBreaker implements the circuit breaker pattern for sub-agent calls.
type CircuitBreaker struct {
	mu            sync.Mutex
	state         CircuitState
	failureCount  int
	successCount  int
	threshold     int           // failures before opening
	resetAfter    time.Duration // time before attempting half-open
	lastFailTime  time.Time
	halfOpenLimit int // max requests in half-open state
}

// NewCircuitBreaker creates a new CircuitBreaker with default settings.
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		state:         CircuitClosed,
		threshold:     3,
		resetAfter:    60 * time.Second,
		halfOpenLimit: 1,
	}
}

// Allow checks if a request should be allowed through the circuit breaker.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return nil
	case CircuitOpen:
		// Check if enough time has passed to try half-open
		if time.Since(cb.lastFailTime) > cb.resetAfter {
			cb.state = CircuitHalfOpen
			cb.successCount = 0
			return nil
		}
		return fmt.Errorf("circuit breaker open: agent is failing")
	case CircuitHalfOpen:
		// Allow limited requests in half-open state
		if cb.successCount < cb.halfOpenLimit {
			return nil
		}
		return fmt.Errorf("circuit breaker half-open: testing recovery")
	}

	return nil
}

// RecordSuccess records a successful execution.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = 0

	if cb.state == CircuitHalfOpen {
		cb.successCount++
		// If we've had enough successes in half-open, close the circuit
		if cb.successCount >= cb.halfOpenLimit {
			cb.state = CircuitClosed
		}
	}
}

// RecordFailure records a failed execution.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++
	cb.lastFailTime = time.Now()

	if cb.state == CircuitHalfOpen {
		// Failure in half-open state immediately reopens the circuit
		cb.state = CircuitOpen
		return
	}

	if cb.failureCount >= cb.threshold {
		cb.state = CircuitOpen
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}
