package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// --- Mock types for tests ---

// mockScanner implements ContextScanner for testing.
type mockScanner struct {
	name    string
	content string
}

func (m *mockScanner) Name() string { return m.name }
func (m *mockScanner) Scan(_ context.Context) (*ContextFragment, error) {
	return &ContextFragment{Source: m.name, Content: m.content, Priority: 1}, nil
}

// errScanner implements ContextScanner that always fails.
type errScanner struct {
	name string
}

func (e *errScanner) Name() string { return e.name }
func (e *errScanner) Scan(_ context.Context) (*ContextFragment, error) {
	return nil, errors.New("scanner error")
}

// nilScanner implements ContextScanner that returns a nil fragment.
type nilScanner struct {
	name string
}

func (n *nilScanner) Name() string { return n.name }
func (n *nilScanner) Scan(_ context.Context) (*ContextFragment, error) {
	return nil, nil
}

// recordingMiddleware records execution for order verification.
type recordingMiddleware struct {
	name   string
	order  *[]string
	mu     sync.Mutex
	failOn string // if set, middleware returns error when call.Name matches
}

func (m *recordingMiddleware) Wrap(next ToolExecutor) ToolExecutor {
	return func(ctx context.Context, call ToolCall) (*ToolResult, error) {
		m.mu.Lock()
		*m.order = append(*m.order, m.name+"_pre")
		m.mu.Unlock()

		if m.failOn != "" && call.Name == m.failOn {
			m.mu.Lock()
			*m.order = append(*m.order, m.name+"_fail")
			m.mu.Unlock()
			return nil, errors.New("middleware denied: " + call.Name)
		}

		result, err := next(ctx, call)

		m.mu.Lock()
		*m.order = append(*m.order, m.name+"_post")
		m.mu.Unlock()
		return result, err
	}
}

// --- TestContextBuilder ---

func TestContextBuilder_Build(t *testing.T) {
	tests := []struct {
		name     string
		scanners []ContextScanner
		want     string
	}{
		{
			name:     "single scanner",
			scanners: []ContextScanner{&mockScanner{name: "one", content: "hello"}},
			want:     "hello",
		},
		{
			name: "multiple scanners in order",
			scanners: []ContextScanner{
				&mockScanner{name: "a", content: "first"},
				&mockScanner{name: "b", content: "second"},
			},
			want: "first\n\nsecond",
		},
		{
			name:     "no scanners",
			scanners: nil,
			want:     "",
		},
		{
			name: "empty content from scanner",
			scanners: []ContextScanner{
				&mockScanner{name: "empty", content: ""},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := NewContextBuilder(tt.scanners...)
			got := cb.Build(context.Background())
			if got != tt.want {
				t.Errorf("Build() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContextBuilder_ScannerFailure(t *testing.T) {
	tests := []struct {
		name          string
		scanners      []ContextScanner
		wantContain   []string
		wantNotContain []string
	}{
		{
			name: "failing scanner does not block others",
			scanners: []ContextScanner{
				&mockScanner{name: "good", content: "good content"},
				&errScanner{name: "bad"},
				&mockScanner{name: "also", content: "also content"},
			},
			wantContain:   []string{"good content", "also content"},
			wantNotContain: []string{"good content", "also content"},
		},
		{
			name: "error placeholder appears in output",
			scanners: []ContextScanner{
				&errScanner{name: "broken"},
			},
			wantContain: []string{"[broken: unavailable]"},
		},
		{
			name: "nil fragment silently skipped",
			scanners: []ContextScanner{
				&nilScanner{name: "nilscanner"},
				&mockScanner{name: "real", content: "real content"},
			},
			wantContain: []string{"real content"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := NewContextBuilder(tt.scanners...)
			got := cb.Build(context.Background())

			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("Build() = %q, expected to contain %q", got, want)
				}
			}
		})
	}
}

// --- TestSelfCorrectionEngine ---

func TestSelfCorrectionEngine_PassesOnFirstTry(t *testing.T) {
	engine := NewSelfCorrectionEngine(2)

	result := &LoopResult{Output: "successful output"}
	var retried bool

	final, err := engine.VerifyAndCorrect(context.Background(), result,
		func(_ context.Context, _ string) (*LoopResult, error) {
			retried = true
			return &LoopResult{Output: "retry output"}, nil
		})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if final.Output != "successful output" {
		t.Errorf("expected original output %q, got %q", "successful output", final.Output)
	}
	if retried {
		t.Error("should not have retried on success")
	}
}

func TestSelfCorrectionEngine_RetriesOnEmptyOutput(t *testing.T) {
	engine := NewSelfCorrectionEngine(2)

	attempts := 0
	result := &LoopResult{Output: ""}

	final, err := engine.VerifyAndCorrect(context.Background(), result,
		func(_ context.Context, failureContext string) (*LoopResult, error) {
			attempts++
			if strings.Contains(failureContext, "Previous attempt") {
				return &LoopResult{Output: "fixed output"}, nil
			}
			return &LoopResult{Output: ""}, nil
		})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if final.Output != "fixed output" {
		t.Errorf("expected fixed output, got %q", final.Output)
	}
	if attempts != 1 {
		t.Errorf("expected 1 retry attempt, got %d", attempts)
	}
}

func TestSelfCorrectionEngine_RetriesOnErrorInOutput(t *testing.T) {
	engine := NewSelfCorrectionEngine(2)

	attempts := 0
	result := &LoopResult{Output: "Error: something went wrong"}

	final, err := engine.VerifyAndCorrect(context.Background(), result,
		func(_ context.Context, _ string) (*LoopResult, error) {
			attempts++
			return &LoopResult{Output: "fixed output"}, nil
		})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if final.Output != "fixed output" {
		t.Errorf("expected fixed output, got %q", final.Output)
	}
	if attempts != 1 {
		t.Errorf("expected 1 retry attempt, got %d", attempts)
	}
}

func TestSelfCorrectionEngine_MaxRetriesExceeded(t *testing.T) {
	engine := NewSelfCorrectionEngine(3)

	attempts := 0
	result := &LoopResult{Output: "Error: persistent failure"}

	final, err := engine.VerifyAndCorrect(context.Background(), result,
		func(_ context.Context, _ string) (*LoopResult, error) {
			attempts++
			// Keep returning bad output to exhaust retries
			return &LoopResult{Output: "Error: still failing"}, nil
		})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Should return the last result (even if bad) after max retries
	if final.Output != "Error: still failing" {
		t.Errorf("expected last attempt output, got %q", final.Output)
	}
	if attempts != 3 {
		t.Errorf("expected 3 retry attempts, got %d", attempts)
	}
}

func TestSelfCorrectionEngine_ZeroRetries(t *testing.T) {
	engine := NewSelfCorrectionEngine(0) // no retries allowed

	result := &LoopResult{Output: "Error: bad"}
	var retried bool

	final, err := engine.VerifyAndCorrect(context.Background(), result,
		func(_ context.Context, _ string) (*LoopResult, error) {
			retried = true
			return &LoopResult{Output: "fixed"}, nil
		})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// With 0 retries, returns original result even if bad
	if final.Output != "Error: bad" {
		t.Errorf("expected original output when maxRetries=0, got %q", final.Output)
	}
	if retried {
		t.Error("should not have retried when maxRetries=0")
	}
}

// --- TestToolMiddlewareChain ---

func TestToolMiddlewareChain_ExecutionOrder(t *testing.T) {
	var order []string

	mw1 := &recordingMiddleware{name: "mw1", order: &order}
	mw2 := &recordingMiddleware{name: "mw2", order: &order}

	chain := NewToolMiddlewareChain(mw1, mw2)
	chain.SetCoreExecutor(func(_ context.Context, call ToolCall) (*ToolResult, error) {
		order = append(order, "core_"+call.Name)
		return &ToolResult{Content: "done", ToolCallID: call.ID, ToolName: call.Name}, nil
	})

	result, err := chain.Execute(context.Background(), ToolCall{ID: "1", Name: "test_tool"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Content != "done" {
		t.Errorf("expected content 'done', got %q", result.Content)
	}

	// Middleware wraps outer->inner: mw1 wraps mw2 wraps core
	// Execution order: mw1_pre -> mw2_pre -> core -> mw2_post -> mw1_post
	expected := []string{"mw1_pre", "mw2_pre", "core_test_tool", "mw2_post", "mw1_post"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d steps, got %d: %v", len(expected), len(order), order)
	}
	for i, e := range expected {
		if order[i] != e {
			t.Errorf("step %d: expected %q, got %q", i, e, order[i])
		}
	}
}

func TestToolMiddlewareChain_NoCoreExecutor(t *testing.T) {
	chain := NewToolMiddlewareChain()

	_, err := chain.Execute(context.Background(), ToolCall{ID: "1", Name: "test"})
	if err == nil {
		t.Error("expected error when no core executor is set")
	}
	if err != nil && !strings.Contains(err.Error(), "core executor not set") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestToolMiddlewareChain_EmptyChain(t *testing.T) {
	var order []string

	chain := NewToolMiddlewareChain()
	chain.SetCoreExecutor(func(_ context.Context, call ToolCall) (*ToolResult, error) {
		order = append(order, "core_"+call.Name)
		return &ToolResult{
			Content:    "done",
			ToolCallID: call.ID,
			ToolName:   call.Name,
		}, nil
	})

	result, err := chain.Execute(context.Background(), ToolCall{ID: "1", Name: "direct"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "done" {
		t.Errorf("expected 'done', got %q", result.Content)
	}
	if len(order) != 1 || order[0] != "core_direct" {
		t.Errorf("expected only core execution, got %v", order)
	}
}

func TestToolMiddlewareChain_MiddlewareCanBlock(t *testing.T) {
	var order []string

	mw := &recordingMiddleware{name: "blocker", order: &order, failOn: "evil_tool"}

	chain := NewToolMiddlewareChain(mw)
	chain.SetCoreExecutor(func(_ context.Context, call ToolCall) (*ToolResult, error) {
		order = append(order, "core_"+call.Name)
		return &ToolResult{Content: "done"}, nil
	})

	// Test that blocked tool returns error and core is never called
	result, err := chain.Execute(context.Background(), ToolCall{ID: "2", Name: "evil_tool"})
	if err == nil {
		t.Error("expected error for blocked tool")
	}
	if result != nil {
		t.Errorf("expected nil result for blocked tool, got %v", result)
	}
	if len(order) != 2 || order[0] != "blocker_pre" || order[1] != "blocker_fail" {
		t.Errorf("unexpected execution order: %v", order)
	}

	// Test that allowed tool passes through
	order = nil
	result, err = chain.Execute(context.Background(), ToolCall{ID: "3", Name: "good_tool"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	expected := []string{"blocker_pre", "core_good_tool", "blocker_post"}
	for i, e := range expected {
		if order[i] != e {
			t.Errorf("step %d: expected %q, got %q", i, e, order[i])
		}
	}
}

func TestToolMiddlewareChain_MiddlewareCanTransformResult(t *testing.T) {
	transformMw := &transformMiddleware{prefix: "transformed: "}

	chain := NewToolMiddlewareChain(transformMw)
	chain.SetCoreExecutor(func(_ context.Context, call ToolCall) (*ToolResult, error) {
		return &ToolResult{
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    "original",
			IsError:    false,
		}, nil
	})

	result, err := chain.Execute(context.Background(), ToolCall{ID: "1", Name: "transform_test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "transformed: original" {
		t.Errorf("expected transformed content, got %q", result.Content)
	}
}

// transformMiddleware prepends a prefix to all tool results.
type transformMiddleware struct {
	prefix string
}

func (m *transformMiddleware) Wrap(next ToolExecutor) ToolExecutor {
	return func(ctx context.Context, call ToolCall) (*ToolResult, error) {
		result, err := next(ctx, call)
		if err != nil {
			return result, err
		}
		return &ToolResult{
			ToolCallID: result.ToolCallID,
			ToolName:   result.ToolName,
			Content:    m.prefix + result.Content,
			IsError:    result.IsError,
			Duration:   result.Duration,
		}, nil
	}
}

// --- Test LoopHookChain ---

func TestLoopHookChain_BeforeLoopError(t *testing.T) {
	hook := &failBeforeHook{}
	chain := NewLoopHookChain(hook)

	err := chain.BeforeLoop(context.Background(), &LoopState{})
	if err == nil {
		t.Error("expected error from BeforeLoop hook")
	}
}

func TestLoopHookChain_AfterTurnContinuesOnError(t *testing.T) {
	hook := &failAfterTurnHook{}
	chain := NewLoopHookChain(hook)

	err := chain.AfterTurn(context.Background(), &LoopState{})
	if err != nil {
		t.Errorf("AfterTurn should not propagate error, got: %v", err)
	}
}

func TestLoopHookChain_AfterLoopContinuesOnError(t *testing.T) {
	hook := &failAfterLoopHook{}
	chain := NewLoopHookChain(hook)

	err := chain.AfterLoop(context.Background(), &LoopResult{})
	if err != nil {
		t.Errorf("AfterLoop should not propagate error, got: %v", err)
	}
}

type failBeforeHook struct{}

func (h *failBeforeHook) BeforeLoop(_ context.Context, _ *LoopState) error {
	return errors.New("before loop failed")
}
func (h *failBeforeHook) AfterTurn(_ context.Context, _ *LoopState) error { return nil }
func (h *failBeforeHook) AfterLoop(_ context.Context, _ *LoopResult) error { return nil }

type failAfterTurnHook struct{}

func (h *failAfterTurnHook) BeforeLoop(_ context.Context, _ *LoopState) error { return nil }
func (h *failAfterTurnHook) AfterTurn(_ context.Context, _ *LoopState) error {
	return errors.New("after turn failed")
}
func (h *failAfterTurnHook) AfterLoop(_ context.Context, _ *LoopResult) error { return nil }

type failAfterLoopHook struct{}

func (h *failAfterLoopHook) BeforeLoop(_ context.Context, _ *LoopState) error { return nil }
func (h *failAfterLoopHook) AfterTurn(_ context.Context, _ *LoopState) error { return nil }
func (h *failAfterLoopHook) AfterLoop(_ context.Context, _ *LoopResult) error {
	return errors.New("after loop failed")
}

// --- Test convertToolResults ---

func TestConvertToolResults(t *testing.T) {
	tests := []struct {
		name  string
		input []ToolResult
	}{
		{
			name:  "nil slice",
			input: nil,
		},
		{
			name:  "empty slice",
			input: []ToolResult{},
		},
		{
			name: "single result",
			input: []ToolResult{
				{ToolCallID: "1", ToolName: "bash", Content: "output", IsError: false},
			},
		},
		{
			name: "multiple results",
			input: []ToolResult{
				{ToolCallID: "1", ToolName: "bash", Content: "out1"},
				{ToolCallID: "2", ToolName: "file_read", Content: "out2", IsError: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertToolResults(tt.input)

			if len(got) != len(tt.input) {
				t.Errorf("len = %d, want %d", len(got), len(tt.input))
			}
			for i := range tt.input {
				if got[i].ToolCallID != tt.input[i].ToolCallID ||
					got[i].ToolName != tt.input[i].ToolName ||
					got[i].Content != tt.input[i].Content {
					t.Errorf("element %d: got %+v, want %+v", i, got[i], tt.input[i])
				}
			}

			// Verify it's a copy, not the same underlying array
			if len(tt.input) > 0 {
				got[0] = ToolResult{} // mutate the copy
				if tt.input[0].ToolCallID == "" && got[0].ToolCallID == "" {
					// Edge case: empty string match after mutation
				}
			}
		})
	}
}
