package taskledger

import (
	"context"
	"time"
)

// noopTaskLedger implements TaskLedger with all no-op methods.
type noopTaskLedger struct{}

func (noopTaskLedger) Register(_ context.Context, _ Task) error             { return nil }
func (noopTaskLedger) Get(_ context.Context, _ string) (*Task, error)       { return nil, nil }
func (noopTaskLedger) Update(_ context.Context, _ Task) error               { return nil }
func (noopTaskLedger) List(_ context.Context, _ TaskFilter) ([]Task, error) { return nil, nil }
func (noopTaskLedger) Cancel(_ context.Context, _ string, _ string) error   { return nil }
func (noopTaskLedger) ClaimNext(_ context.Context, _ TaskKind, _ string) (*Task, error) {
	return nil, nil
}
func (noopTaskLedger) Heartbeat(_ context.Context, _ string) error         { return nil }
func (noopTaskLedger) GetTree(_ context.Context, _ string) ([]Task, error) { return nil, nil }
func (noopTaskLedger) DetectStale(_ context.Context, _ time.Duration) ([]Task, error) {
	return nil, nil
}

// NoopTaskLedger returns a TaskLedger that discards all operations.
func NoopTaskLedger() TaskLedger { return noopTaskLedger{} }

var _ TaskLedger = noopTaskLedger{}
