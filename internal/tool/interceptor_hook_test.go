package tool

import (
	"context"
	"testing"
)

func TestHookInterceptor_NoHookManager(t *testing.T) {
	hi := NewHookInterceptor(nil)
	called := false
	_, err := hi.Intercept(context.Background(), &ToolCall{ToolName: "bash"}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("should pass through when no hook manager")
	}
}
