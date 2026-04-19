package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/sandbox"
)

func passthrough(_ context.Context, _ *ToolCall) (*ToolResult, error) {
	return &ToolResult{Output: "passthrough"}, nil
}

func TestSandboxInterceptor_FileBlocked(t *testing.T) {
	dir := t.TempDir()
	guard, err := sandbox.NewFileGuard([]string{dir}, nil)
	if err != nil {
		t.Fatalf("NewFileGuard: %v", err)
	}
	interceptor := NewSandboxInterceptor(nil, guard, nil, true)

	call := &ToolCall{
		ToolName: "file_write",
		Input:    `{"path":"/etc/passwd","content":"hacked"}`,
	}
	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "should not reach"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), call, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextCalled {
		t.Fatal("next() should NOT be called for blocked file path")
	}
	if res.Error == "" {
		t.Fatal("expected error in result for blocked file")
	}
	if !strings.Contains(res.Error, "sandbox") {
		t.Fatalf("expected sandbox error, got: %s", res.Error)
	}
}

func TestSandboxInterceptor_FileAllowed(t *testing.T) {
	dir := t.TempDir()
	guard, err := sandbox.NewFileGuard([]string{dir}, nil)
	if err != nil {
		t.Fatalf("NewFileGuard: %v", err)
	}
	interceptor := NewSandboxInterceptor(nil, guard, nil, true)

	target := dir + "/test.txt"
	call := &ToolCall{
		ToolName: "file_write",
		Input:    `{"path":"` + target + `","content":"hello"}`,
	}
	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "written"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), call, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nextCalled {
		t.Fatal("expected next() to be called for allowed path")
	}
	if res.Output != "written" {
		t.Fatalf("expected output 'written', got %q", res.Output)
	}
}

func TestSandboxInterceptor_HTTPBlocked(t *testing.T) {
	policy := sandbox.NewNetworkPolicy("blacklist", nil, []string{"evil.com"})
	interceptor := NewSandboxInterceptor(nil, nil, policy, true)

	call := &ToolCall{
		ToolName: "http",
		Input:    `{"url":"https://evil.com/steal"}`,
	}
	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "should not reach"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), call, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextCalled {
		t.Fatal("next() should NOT be called for blacklisted host")
	}
	if res.Error == "" {
		t.Fatal("expected error in result for blocked URL")
	}
	if !strings.Contains(res.Error, "sandbox") {
		t.Fatalf("expected sandbox error, got: %s", res.Error)
	}
}

func TestSandboxInterceptor_Disabled(t *testing.T) {
	guard, err := sandbox.NewFileGuard([]string{t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("NewFileGuard: %v", err)
	}
	policy := sandbox.NewNetworkPolicy("blacklist", nil, []string{"evil.com"})
	interceptor := NewSandboxInterceptor(nil, guard, policy, false)

	call := &ToolCall{
		ToolName: "bash",
		Input:    `{"command":"rm -rf /"}`,
	}
	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "passed through"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), call, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nextCalled {
		t.Fatal("expected next() to be called when sandbox disabled")
	}
	if res.Output != "passed through" {
		t.Fatalf("expected output 'passed through', got %q", res.Output)
	}
}

func TestSandboxInterceptor_UnknownTool(t *testing.T) {
	guard, err := sandbox.NewFileGuard([]string{t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("NewFileGuard: %v", err)
	}
	policy := sandbox.NewNetworkPolicy("blacklist", nil, []string{"evil.com"})
	interceptor := NewSandboxInterceptor(nil, guard, policy, true)

	call := &ToolCall{
		ToolName: "memory_manage",
		Input:    `{"action":"add","content":"remember this"}`,
	}
	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "managed"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), call, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nextCalled {
		t.Fatal("expected next() to be called for unknown tool")
	}
	if res.Output != "managed" {
		t.Fatalf("expected output 'managed', got %q", res.Output)
	}
}
