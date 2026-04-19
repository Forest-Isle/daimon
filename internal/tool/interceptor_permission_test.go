package tool

import (
	"context"
	"testing"
)

type mockNotifier struct {
	called   bool
	lastCall *ToolCall
}

func (n *mockNotifier) NotifyToolExecution(_ context.Context, call *ToolCall) error {
	n.called = true
	n.lastCall = call
	return nil
}

type mockApprover struct{ approve bool }

func (a *mockApprover) RequestApproval(_ context.Context, _ *ToolCall) (bool, error) {
	return a.approve, nil
}

func makeEngine(action string) *PermissionEngine {
	return NewPermissionEngine([]PermissionRule{
		{Tool: "*", Action: action},
	}, "none", nil)
}

func TestPermissionInterceptor_None(t *testing.T) {
	notifier := &mockNotifier{}
	interceptor := NewPermissionInterceptor(makeEngine("none"), notifier, nil)

	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "ok"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), &ToolCall{ToolName: "bash", Input: "ls"}, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nextCalled {
		t.Fatal("expected next() to be called")
	}
	if res.Output != "ok" {
		t.Fatalf("expected output 'ok', got %q", res.Output)
	}
	if notifier.called {
		t.Fatal("notifier should NOT be called for action=none")
	}
}

func TestPermissionInterceptor_Notify(t *testing.T) {
	notifier := &mockNotifier{}
	interceptor := NewPermissionInterceptor(makeEngine("notify"), notifier, nil)

	nextCalled := false
	call := &ToolCall{ToolName: "bash", Input: "echo hello"}
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "hello"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), call, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nextCalled {
		t.Fatal("expected next() to be called")
	}
	if res.Output != "hello" {
		t.Fatalf("expected output 'hello', got %q", res.Output)
	}
	if !notifier.called {
		t.Fatal("notifier should be called for action=notify")
	}
	if notifier.lastCall != call {
		t.Fatal("notifier should receive the original ToolCall")
	}
}

func TestPermissionInterceptor_Deny(t *testing.T) {
	interceptor := NewPermissionInterceptor(makeEngine("deny"), nil, nil)

	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "should not reach"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), &ToolCall{ToolName: "bash", Input: "rm -rf /"}, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextCalled {
		t.Fatal("next() should NOT be called for action=deny")
	}
	if res.Error == "" {
		t.Fatal("expected error in result for denied tool")
	}
}

func TestPermissionInterceptor_ApproveGranted(t *testing.T) {
	approver := &mockApprover{approve: true}
	interceptor := NewPermissionInterceptor(makeEngine("approve"), nil, approver)

	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "approved"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), &ToolCall{ToolName: "bash", Input: "deploy"}, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nextCalled {
		t.Fatal("expected next() to be called when approval granted")
	}
	if res.Output != "approved" {
		t.Fatalf("expected output 'approved', got %q", res.Output)
	}
}

func TestPermissionInterceptor_ApproveDenied(t *testing.T) {
	approver := &mockApprover{approve: false}
	interceptor := NewPermissionInterceptor(makeEngine("approve"), nil, approver)

	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "should not reach"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), &ToolCall{ToolName: "bash", Input: "deploy"}, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextCalled {
		t.Fatal("next() should NOT be called when approval denied")
	}
	if res.Error == "" {
		t.Fatal("expected error in result when approval denied")
	}
}
