package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/core"
)

// fakeProvider is a deterministic Provider used to script multi-turn
// agentic flows in tests.
type fakeProvider struct {
	mu    sync.Mutex
	turns []core.LLMResponse
	calls int
}

func (f *fakeProvider) Complete(_ context.Context, _ core.LLMRequest) (*core.LLMResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls >= len(f.turns) {
		return &core.LLMResponse{Text: "(no more)", StopReason: core.StopEndTurn}, nil
	}
	r := f.turns[f.calls]
	f.calls++
	return &r, nil
}

func (f *fakeProvider) Stream(_ context.Context, _ core.LLMRequest) (core.Stream, error) {
	return nil, errors.New("not used")
}

func newEcho() core.Tool {
	return &core.ToolFunc{
		S: core.ToolSchema{
			Name:        "echo",
			Description: "echoes its message field",
			InputSchema: map[string]any{"type": "object"},
		},
		IsReadOnly: true,
		Fn: func(_ context.Context, input json.RawMessage) (core.ToolResult, error) {
			var p struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(input, &p)
			return core.ToolResult{Output: p.Message}, nil
		},
	}
}

func newSlowEcho(d time.Duration, hits *int64) core.Tool {
	return &core.ToolFunc{
		S: core.ToolSchema{
			Name: "slow", Description: "slow", InputSchema: map[string]any{"type": "object"},
		},
		IsReadOnly: true,
		Fn: func(ctx context.Context, _ json.RawMessage) (core.ToolResult, error) {
			atomic.AddInt64(hits, 1)
			select {
			case <-time.After(d):
				return core.ToolResult{Output: "ok"}, nil
			case <-ctx.Done():
				return core.ToolResult{Error: ctx.Err().Error()}, nil
			}
		},
	}
}

// TestSingleTurn exercises the simplest flow: prompt → reply, no tools.
func TestSingleTurn(t *testing.T) {
	prov := &fakeProvider{turns: []core.LLMResponse{
		{Text: "hello back", StopReason: core.StopEndTurn},
	}}
	ag := core.New(prov, nil, nil, core.Config{})
	out, stop, err := ag.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "hello back" {
		t.Fatalf("got %q", out)
	}
	if stop != core.StopEndTurn {
		t.Fatalf("stop %v", stop)
	}
}

// TestToolRoundtrip verifies a single tool_use → tool result → final text.
func TestToolRoundtrip(t *testing.T) {
	reg := core.NewToolRegistry()
	reg.Register(newEcho())

	prov := &fakeProvider{turns: []core.LLMResponse{
		{ToolCalls: []core.ToolCall{{ID: "u1", Name: "echo", Input: json.RawMessage(`{"message":"42"}`)}}, StopReason: core.StopToolUse},
		{Text: "result was 42", StopReason: core.StopEndTurn},
	}}
	ag := core.New(prov, reg, nil, core.Config{})
	out, stop, err := ag.Run(context.Background(), "what is the answer")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "result was 42" {
		t.Fatalf("got %q", out)
	}
	if stop != core.StopEndTurn {
		t.Fatalf("stop %v", stop)
	}
}

// TestParallelReadOnlyTools confirms that read-only batches actually run
// concurrently — measured wall-clock under sequential semantics would be
// 4 × 50ms = 200ms; with parallelism it should be < 150ms.
func TestParallelReadOnlyTools(t *testing.T) {
	reg := core.NewToolRegistry()
	var hits int64
	reg.Register(newSlowEcho(50*time.Millisecond, &hits))

	calls := []core.ToolCall{
		{ID: "u1", Name: "slow", Input: json.RawMessage(`{}`)},
		{ID: "u2", Name: "slow", Input: json.RawMessage(`{}`)},
		{ID: "u3", Name: "slow", Input: json.RawMessage(`{}`)},
		{ID: "u4", Name: "slow", Input: json.RawMessage(`{}`)},
	}
	prov := &fakeProvider{turns: []core.LLMResponse{
		{ToolCalls: calls, StopReason: core.StopToolUse},
		{Text: "done", StopReason: core.StopEndTurn},
	}}
	ag := core.New(prov, reg, nil, core.Config{ParallelTools: 4})

	start := time.Now()
	if _, _, err := ag.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	elapsed := time.Since(start)
	if got := atomic.LoadInt64(&hits); got != 4 {
		t.Fatalf("hits=%d", got)
	}
	if elapsed >= 150*time.Millisecond {
		t.Fatalf("expected parallel execution, elapsed=%v", elapsed)
	}
}

// TestGateDeny ensures a denied tool surfaces as a tool error message
// (not a hard runtime failure) so the model can self-recover.
func TestGateDeny(t *testing.T) {
	reg := core.NewToolRegistry()
	reg.Register(newEcho())

	deny := core.GateFunc(func(_ context.Context, req core.PermissionRequest) (core.Decision, string, error) {
		return core.DecisionDeny, "no echoes today", nil
	})

	mem := core.NewInMemory()
	prov := &fakeProvider{turns: []core.LLMResponse{
		{ToolCalls: []core.ToolCall{{ID: "u1", Name: "echo", Input: json.RawMessage(`{"message":"x"}`)}}, StopReason: core.StopToolUse},
		{Text: "ok, sorry", StopReason: core.StopEndTurn},
	}}
	ag := core.New(prov, reg, mem, core.Config{Gate: deny})
	if _, _, err := ag.Run(context.Background(), "echo"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	hist, _ := mem.Snapshot(context.Background())
	var found bool
	for _, m := range hist {
		if m.Role == core.RoleTool && m.ToolUseID == "u1" {
			if !strings.Contains(m.Content, "denied by policy") {
				t.Fatalf("expected denial in tool result, got %q", m.Content)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("did not find tool result for u1")
	}
}

// TestMaxTurns guarantees the loop terminates even if the model keeps
// requesting tools forever.
func TestMaxTurns(t *testing.T) {
	reg := core.NewToolRegistry()
	reg.Register(newEcho())
	loop := core.LLMResponse{
		ToolCalls:  []core.ToolCall{{ID: "u1", Name: "echo", Input: json.RawMessage(`{"message":"x"}`)}},
		StopReason: core.StopToolUse,
	}
	prov := &fakeProvider{turns: []core.LLMResponse{loop, loop, loop, loop, loop, loop, loop, loop, loop, loop}}
	ag := core.New(prov, reg, nil, core.Config{MaxTurns: 3})
	_, stop, err := ag.Run(context.Background(), "spin")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stop != core.StopMaxTurns {
		t.Fatalf("expected StopMaxTurns, got %v", stop)
	}
}

// TestCacheMiddleware verifies idempotent read-only calls are deduped.
func TestCacheMiddleware(t *testing.T) {
	reg := core.NewToolRegistry()
	var hits int64
	reg.Register(&core.ToolFunc{
		S:          core.ToolSchema{Name: "ro", Description: "ro", InputSchema: map[string]any{"type": "object"}},
		IsReadOnly: true,
		Fn: func(_ context.Context, _ json.RawMessage) (core.ToolResult, error) {
			atomic.AddInt64(&hits, 1)
			return core.ToolResult{Output: "v"}, nil
		},
	})
	prov := &fakeProvider{turns: []core.LLMResponse{
		{ToolCalls: []core.ToolCall{
			{ID: "a", Name: "ro", Input: json.RawMessage(`{"x":1}`)},
			{ID: "b", Name: "ro", Input: json.RawMessage(`{"x":1}`)},
		}, StopReason: core.StopToolUse},
		{Text: "ok", StopReason: core.StopEndTurn},
	}}
	ag := core.New(prov, reg, nil, core.Config{
		ParallelTools:  1,
		ToolMiddleware: []core.ToolMiddleware{core.CacheToolMiddleware(reg)},
	})
	if _, _, err := ag.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("expected 1 underlying call, got %d", got)
	}
}

// TestEventStream covers ordering of emitted events.
func TestEventStream(t *testing.T) {
	var got []core.EventKind
	var mu sync.Mutex
	sink := core.EventSinkFunc(func(e core.Event) {
		mu.Lock()
		got = append(got, e.Kind)
		mu.Unlock()
	})

	reg := core.NewToolRegistry()
	reg.Register(newEcho())
	prov := &fakeProvider{turns: []core.LLMResponse{
		{ToolCalls: []core.ToolCall{{ID: "u1", Name: "echo", Input: json.RawMessage(`{"message":"hi"}`)}}, StopReason: core.StopToolUse},
		{Text: "done", StopReason: core.StopEndTurn},
	}}
	ag := core.New(prov, reg, nil, core.Config{Sink: sink})
	if _, _, err := ag.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	// Spot-check sequence (other events may interleave).
	if got[0] != core.EventStart {
		t.Fatalf("expected first event to be start, got %v", got[0])
	}
	if got[len(got)-1] != core.EventFinish {
		t.Fatalf("expected last event to be finish, got %v", got[len(got)-1])
	}
}

// agentHistory and findToolResult helpers were removed; tests inspect a
// custom InMemory passed via constructor when they need post-mortem state.
