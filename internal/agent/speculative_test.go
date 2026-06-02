package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// mockReadOnlyTool implements tool.Tool and tool.ReadOnlyTool.
type mockReadOnlyTool struct {
	name    string
	delay   time.Duration
	output  string
	execErr error
	called  atomic.Int32
}

func (m *mockReadOnlyTool) Name() string                { return m.name }
func (m *mockReadOnlyTool) Description() string         { return "mock read-only tool" }
func (m *mockReadOnlyTool) InputSchema() map[string]any { return nil }
func (m *mockReadOnlyTool) RequiresApproval() bool      { return false }
func (m *mockReadOnlyTool) IsReadOnly() bool            { return true }
func (m *mockReadOnlyTool) Execute(ctx context.Context, _ []byte) (tool.Result, error) {
	m.called.Add(1)
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return tool.Result{}, ctx.Err()
		}
	}
	if m.execErr != nil {
		return tool.Result{}, m.execErr
	}
	return tool.Result{Output: m.output}, nil
}

// mockWriteTool implements tool.Tool but NOT ReadOnlyTool (write-capable).
type mockWriteTool struct {
	name string
}

func (m *mockWriteTool) Name() string                { return m.name }
func (m *mockWriteTool) Description() string         { return "mock write tool" }
func (m *mockWriteTool) InputSchema() map[string]any { return nil }
func (m *mockWriteTool) RequiresApproval() bool      { return false }
func (m *mockWriteTool) Execute(_ context.Context, _ []byte) (tool.Result, error) {
	return tool.Result{Output: "written"}, nil
}

func newTestRegistry(tools ...tool.Tool) *tool.Registry {
	r := tool.NewRegistry()
	for _, t := range tools {
		r.Register(t)
	}
	return r
}

func TestSpeculativeExecutor_TryLaunch_ReadOnly(t *testing.T) {
	ro := &mockReadOnlyTool{name: "file_read", output: "hello"}
	reg := newTestRegistry(ro)
	se := NewSpeculativeExecutor(reg, 3)

	launched := se.TryLaunch(context.Background(), "tu_1", "file_read", `{"path":"a.txt"}`)
	if !launched {
		t.Fatal("expected TryLaunch to return true for read-only tool")
	}

	// Wait for completion then collect.
	time.Sleep(50 * time.Millisecond)
	result, err := se.Collect("tu_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Output != "hello" {
		t.Fatalf("expected output 'hello', got %q", result.Output)
	}
	if ro.called.Load() != 1 {
		t.Fatalf("expected Execute called once, got %d", ro.called.Load())
	}
}

func TestSpeculativeExecutor_TryLaunch_NonReadOnly_Rejected(t *testing.T) {
	w := &mockWriteTool{name: "file_write"}
	reg := newTestRegistry(w)
	se := NewSpeculativeExecutor(reg, 3)

	launched := se.TryLaunch(context.Background(), "tu_1", "file_write", `{}`)
	if launched {
		t.Fatal("expected TryLaunch to return false for write tool")
	}

	result, err := se.Collect("tu_1")
	if err != nil {
		t.Fatal("unexpected error from Collect")
	}
	if result != nil {
		t.Fatal("expected nil result for non-launched tool")
	}
}

func TestSpeculativeExecutor_TryLaunch_MaxInFlight(t *testing.T) {
	slow := func(name string) *mockReadOnlyTool {
		return &mockReadOnlyTool{name: name, delay: 2 * time.Second, output: "ok"}
	}
	t1, t2, t3, t4 := slow("t1"), slow("t2"), slow("t3"), slow("t4")
	reg := newTestRegistry(t1, t2, t3, t4)
	se := NewSpeculativeExecutor(reg, 3)

	if !se.TryLaunch(context.Background(), "a", "t1", "") {
		t.Fatal("launch 1 should succeed")
	}
	if !se.TryLaunch(context.Background(), "b", "t2", "") {
		t.Fatal("launch 2 should succeed")
	}
	if !se.TryLaunch(context.Background(), "c", "t3", "") {
		t.Fatal("launch 3 should succeed")
	}
	if se.TryLaunch(context.Background(), "d", "t4", "") {
		t.Fatal("launch 4 should be rejected (max in-flight = 3)")
	}

	se.CancelAll()
}

func TestSpeculativeExecutor_CancelAll(t *testing.T) {
	slow := &mockReadOnlyTool{name: "slow", delay: 5 * time.Second, output: "done"}
	reg := newTestRegistry(slow)
	se := NewSpeculativeExecutor(reg, 3)

	se.TryLaunch(context.Background(), "tu_slow", "slow", "")
	time.Sleep(20 * time.Millisecond) // let goroutine start

	se.CancelAll()
	time.Sleep(20 * time.Millisecond) // let cancellation propagate

	result, err := se.Collect("tu_slow")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result after cancel, got %+v", result)
	}
}

func TestSpeculativeExecutor_Collect_UnknownID(t *testing.T) {
	reg := newTestRegistry()
	se := NewSpeculativeExecutor(reg, 3)

	result, err := se.Collect("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result for unknown ID")
	}
}

func TestSpeculativeExecutor_Reset(t *testing.T) {
	ro := &mockReadOnlyTool{name: "reader", output: "data"}
	reg := newTestRegistry(ro)
	se := NewSpeculativeExecutor(reg, 3)

	se.TryLaunch(context.Background(), "tu_r", "reader", "")
	time.Sleep(50 * time.Millisecond)

	se.Reset()

	result, err := se.Collect("tu_r")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result after Reset")
	}

	// Verify we can launch again after reset.
	if !se.TryLaunch(context.Background(), "tu_r2", "reader", "") {
		t.Fatal("expected TryLaunch to succeed after Reset")
	}

	se.CancelAll()
}

func TestSpeculativeExecutor_TryLaunch_DuplicateID(t *testing.T) {
	ro := &mockReadOnlyTool{name: "reader", delay: time.Second, output: "ok"}
	reg := newTestRegistry(ro)
	se := NewSpeculativeExecutor(reg, 3)

	if !se.TryLaunch(context.Background(), "dup", "reader", "") {
		t.Fatal("first launch should succeed")
	}
	if se.TryLaunch(context.Background(), "dup", "reader", "") {
		t.Fatal("duplicate toolUseID should be rejected")
	}

	se.CancelAll()
}

func TestSpeculativeExecutor_TryLaunch_UnknownTool(t *testing.T) {
	reg := newTestRegistry()
	se := NewSpeculativeExecutor(reg, 3)

	if se.TryLaunch(context.Background(), "tu_1", "nonexistent", "") {
		t.Fatal("expected TryLaunch to return false for unknown tool")
	}
}

func TestSpeculativeExecutor_DefaultMaxInFlight(t *testing.T) {
	reg := newTestRegistry()
	se := NewSpeculativeExecutor(reg, 0)

	se.mu.Lock()
	max := se.maxInFlight
	se.mu.Unlock()

	if max != 3 {
		t.Fatalf("expected default maxInFlight=3, got %d", max)
	}
}
