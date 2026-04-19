package tool

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type testInterceptor struct {
	name string
	fn   func(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error)
}

func (t *testInterceptor) Name() string { return t.name }

func (t *testInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	return t.fn(ctx, call, next)
}

func TestInterceptorChain_EmptyChain(t *testing.T) {
	chain := NewInterceptorChain(nil)

	called := false
	final := func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "final"}, nil
	}

	result, err := chain.Execute(context.Background(), &ToolCall{ToolName: "test"}, final)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("final function was not called")
	}
	if result.Output != "final" {
		t.Fatalf("expected output 'final', got %q", result.Output)
	}
}

func TestInterceptorChain_Order(t *testing.T) {
	var order []string

	first := &testInterceptor{
		name: "first",
		fn: func(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
			order = append(order, "first-before")
			result, err := next(ctx, call)
			order = append(order, "first-after")
			return result, err
		},
	}

	second := &testInterceptor{
		name: "second",
		fn: func(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
			order = append(order, "second-before")
			result, err := next(ctx, call)
			order = append(order, "second-after")
			return result, err
		},
	}

	chain := NewInterceptorChain([]ToolInterceptor{first, second})

	final := func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		order = append(order, "final")
		return &ToolResult{Output: "done"}, nil
	}

	_, err := chain.Execute(context.Background(), &ToolCall{ToolName: "test"}, final)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "first-before,second-before,final,second-after,first-after"
	got := strings.Join(order, ",")
	if got != expected {
		t.Fatalf("execution order mismatch:\n  expected: %s\n  got:      %s", expected, got)
	}
}

func TestInterceptorChain_ShortCircuit(t *testing.T) {
	var order []string

	blocker := &testInterceptor{
		name: "blocker",
		fn: func(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
			order = append(order, "blocker")
			return &ToolResult{Output: "blocked", Error: "denied"}, nil
		},
	}

	downstream := &testInterceptor{
		name: "downstream",
		fn: func(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
			order = append(order, "downstream")
			return next(ctx, call)
		},
	}

	chain := NewInterceptorChain([]ToolInterceptor{blocker, downstream})

	finalCalled := false
	final := func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		finalCalled = true
		return &ToolResult{Output: "final"}, nil
	}

	result, err := chain.Execute(context.Background(), &ToolCall{ToolName: "test"}, final)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if finalCalled {
		t.Fatal("final function should not have been called")
	}
	if result.Output != "blocked" {
		t.Fatalf("expected output 'blocked', got %q", result.Output)
	}
	if result.Error != "denied" {
		t.Fatalf("expected error 'denied', got %q", result.Error)
	}

	expected := "blocker"
	got := strings.Join(order, ",")
	if got != expected {
		t.Fatalf("execution order mismatch:\n  expected: %s\n  got:      %s", expected, got)
	}
}

func TestInterceptorChain_MetadataPropagation(t *testing.T) {
	tagger := &testInterceptor{
		name: "tagger",
		fn: func(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
			result, err := next(ctx, call)
			if err != nil {
				return nil, err
			}
			if result.Metadata == nil {
				result.Metadata = make(map[string]string)
			}
			result.Metadata["tagged"] = "true"
			return result, nil
		},
	}

	chain := NewInterceptorChain([]ToolInterceptor{tagger})

	final := func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		return &ToolResult{Output: fmt.Sprintf("ran:%s", call.ToolName)}, nil
	}

	result, err := chain.Execute(context.Background(), &ToolCall{ToolName: "echo"}, final)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "ran:echo" {
		t.Fatalf("expected output 'ran:echo', got %q", result.Output)
	}
	if result.Metadata["tagged"] != "true" {
		t.Fatalf("expected metadata tagged=true, got %v", result.Metadata)
	}
}
