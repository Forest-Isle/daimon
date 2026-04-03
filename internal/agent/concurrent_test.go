package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// slowReadOnlyTool is a mock tool that sleeps and implements ReadOnlyTool.
type slowReadOnlyTool struct {
	name      string
	delay     time.Duration
	execCount *atomic.Int32
}

func (t *slowReadOnlyTool) Name() string                                    { return t.name }
func (t *slowReadOnlyTool) Description() string                             { return "mock read-only tool" }
func (t *slowReadOnlyTool) InputSchema() map[string]any                     { return nil }
func (t *slowReadOnlyTool) RequiresApproval() bool                          { return false }
func (t *slowReadOnlyTool) IsReadOnly() bool                                { return true }
func (t *slowReadOnlyTool) Execute(_ context.Context, _ []byte) (tool.Result, error) {
	t.execCount.Add(1)
	time.Sleep(t.delay)
	return tool.Result{Output: "result from " + t.name}, nil
}

// slowWriteTool is a mock tool that does NOT implement ReadOnlyTool.
type slowWriteTool struct {
	name      string
	delay     time.Duration
	execCount *atomic.Int32
}

func (t *slowWriteTool) Name() string                                    { return t.name }
func (t *slowWriteTool) Description() string                             { return "mock write tool" }
func (t *slowWriteTool) InputSchema() map[string]any                     { return nil }
func (t *slowWriteTool) RequiresApproval() bool                          { return false }
func (t *slowWriteTool) Execute(_ context.Context, _ []byte) (tool.Result, error) {
	t.execCount.Add(1)
	time.Sleep(t.delay)
	return tool.Result{Output: "result from " + t.name}, nil
}

// newTestDB creates a temporary SQLite database for testing.
func newTestDB(t *testing.T) *store.DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// concurrentTestSession creates a minimal session for concurrent tests.
func concurrentTestSession() *session.Session {
	return &session.Session{
		ID:        "test-session",
		Channel:   "test",
		ChannelID: "test-channel",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Metadata:  make(map[string]string),
	}
}

func TestConcurrentReadOnlyExecution(t *testing.T) {
	// Suppress log output during test
	_ = os.Stderr

	db := newTestDB(t)
	registry := tool.NewRegistry()
	counts := [3]*atomic.Int32{{}, {}, {}}

	for i := 0; i < 3; i++ {
		registry.Register(&slowReadOnlyTool{
			name:      fmt.Sprintf("read_%d", i),
			delay:     100 * time.Millisecond,
			execCount: counts[i],
		})
	}

	rt := &Runtime{
		tools: registry,
		db:    db,
		cfg:   config.AgentConfig{},
		concurrentCfg: config.ConcurrentExecutionConfig{
			Enabled:        true,
			MaxConcurrency: 4,
		},
	}

	sess := concurrentTestSession()

	toolCalls := []ToolUseBlock{
		{ID: "tc_0", Name: "read_0", Input: "{}"},
		{ID: "tc_1", Name: "read_1", Input: "{}"},
		{ID: "tc_2", Name: "read_2", Input: "{}"},
	}

	start := time.Now()
	rt.executeTools(context.Background(), nil, sess, channel.MessageTarget{}, toolCalls)
	elapsed := time.Since(start)

	// If truly concurrent, should take ~100ms not ~300ms
	if elapsed > 250*time.Millisecond {
		t.Errorf("concurrent execution took %v, expected ~100ms (tools ran sequentially?)", elapsed)
	}

	// All tools should have been executed exactly once
	for i, c := range counts {
		if c.Load() != 1 {
			t.Errorf("tool %d executed %d times, expected 1", i, c.Load())
		}
	}

	// Verify results were added to session
	history := sess.History()
	toolResults := 0
	for _, m := range history {
		if m.Role == "tool_result" {
			toolResults++
		}
	}
	if toolResults != 3 {
		t.Errorf("expected 3 tool_result messages, got %d", toolResults)
	}
}

func TestMixedReadWriteExecution(t *testing.T) {
	db := newTestDB(t)
	registry := tool.NewRegistry()
	var readCount, writeCount atomic.Int32

	registry.Register(&slowReadOnlyTool{
		name: "reader", delay: 50 * time.Millisecond, execCount: &readCount,
	})
	registry.Register(&slowWriteTool{
		name: "writer", delay: 10 * time.Millisecond, execCount: &writeCount,
	})

	rt := &Runtime{
		tools: registry,
		db:    db,
		cfg:   config.AgentConfig{},
		concurrentCfg: config.ConcurrentExecutionConfig{
			Enabled:        true,
			MaxConcurrency: 4,
		},
	}

	sess := concurrentTestSession()

	toolCalls := []ToolUseBlock{
		{ID: "tc_read", Name: "reader", Input: "{}"},
		{ID: "tc_write", Name: "writer", Input: "{}"},
	}

	rt.executeTools(context.Background(), nil, sess, channel.MessageTarget{}, toolCalls)

	if readCount.Load() != 1 {
		t.Errorf("reader executed %d times, expected 1", readCount.Load())
	}
	if writeCount.Load() != 1 {
		t.Errorf("writer executed %d times, expected 1", writeCount.Load())
	}

	// Verify both results in session history
	history := sess.History()
	toolResults := 0
	for _, m := range history {
		if m.Role == "tool_result" {
			toolResults++
		}
	}
	if toolResults != 2 {
		t.Errorf("expected 2 tool_result messages, got %d", toolResults)
	}
}

func TestSequentialFallbackWhenDisabled(t *testing.T) {
	db := newTestDB(t)
	registry := tool.NewRegistry()
	counts := [3]*atomic.Int32{{}, {}, {}}

	for i := 0; i < 3; i++ {
		registry.Register(&slowReadOnlyTool{
			name:      fmt.Sprintf("read_%d", i),
			delay:     50 * time.Millisecond,
			execCount: counts[i],
		})
	}

	// Concurrency disabled
	rt := &Runtime{
		tools: registry,
		db:    db,
		cfg:   config.AgentConfig{},
		concurrentCfg: config.ConcurrentExecutionConfig{
			Enabled: false,
		},
	}

	sess := concurrentTestSession()

	toolCalls := []ToolUseBlock{
		{ID: "tc_0", Name: "read_0", Input: "{}"},
		{ID: "tc_1", Name: "read_1", Input: "{}"},
		{ID: "tc_2", Name: "read_2", Input: "{}"},
	}

	start := time.Now()
	rt.executeTools(context.Background(), nil, sess, channel.MessageTarget{}, toolCalls)
	elapsed := time.Since(start)

	// Sequential: should take ~150ms (3 × 50ms)
	if elapsed < 120*time.Millisecond {
		t.Errorf("sequential execution took %v, expected ~150ms (ran concurrently?)", elapsed)
	}

	// All tools should have been executed exactly once
	for i, c := range counts {
		if c.Load() != 1 {
			t.Errorf("tool %d executed %d times, expected 1", i, c.Load())
		}
	}
}
