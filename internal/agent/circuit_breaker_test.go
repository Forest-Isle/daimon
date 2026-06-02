package agent

import (
	"testing"
	"time"
)

func TestCircuitBreaker_ClosedAllowsRequests(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)
	if !cb.Allow() {
		t.Fatal("expected Allow()=true in closed state")
	}
	if cb.State() != CircuitClosed {
		t.Fatal("expected state=closed")
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	// 3 consecutive failures should open the circuit.
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}
	if cb.State() != CircuitOpen {
		t.Fatal("expected circuit to open after 3 consecutive failures")
	}
	if cb.Allow() {
		t.Fatal("expected Allow()=false in open state")
	}
}

func TestCircuitBreaker_HalfOpenProbe(t *testing.T) {
	cb := NewCircuitBreaker(2, 10*time.Millisecond)

	// Open the circuit.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("expected open state")
	}

	// Wait for timeout.
	time.Sleep(20 * time.Millisecond)

	// First Allow() after timeout transitions to half-open.
	if !cb.Allow() {
		t.Fatal("expected Allow()=true after timeout (half-open probe)")
	}
	// Subsequent calls should be denied until probe succeeds.
	if cb.Allow() {
		t.Fatal("expected Allow()=false in half-open (only first probe allowed)")
	}
}

func TestCircuitBreaker_SuccessClosesCircuit(t *testing.T) {
	cb := NewCircuitBreaker(2, 10*time.Millisecond)

	// Open
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait and probe
	time.Sleep(20 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("expected probe allowed")
	}

	// Success should close
	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Fatal("expected circuit to close after success")
	}
	if !cb.Allow() {
		t.Fatal("expected Allow()=true in closed state")
	}
}

func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
	cb := NewCircuitBreaker(2, 10*time.Millisecond)

	// Open
	cb.RecordFailure()
	cb.RecordFailure()

	// Probe
	time.Sleep(20 * time.Millisecond)
	cb.Allow()

	// Probe fails → re-open immediately
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("expected circuit to re-open after half-open probe failure")
	}
}
