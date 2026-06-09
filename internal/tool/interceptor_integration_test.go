package tool

import (
	"context"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/sandbox"
)

func TestInterceptorChain_FullPipeline(t *testing.T) {
	rules := []PermissionRule{
		{Tool: "bash", Pattern: "rm *", Action: "deny"},
		{Tool: "bash", Action: "none"},
		{Tool: "file_write", Action: "none"},
		{Tool: "file_patch", Action: "none"},
		{Tool: "http", Action: "none"},
	}
	pe := NewPermissionEngine(rules, "approve", nil)
	fg, _ := sandbox.NewFileGuard([]string{"/tmp"}, nil)
	np := sandbox.NewNetworkPolicy("blacklist", nil, []string{"evil.com"})

	chain := NewInterceptorChain([]ToolInterceptor{
		NewPermissionInterceptor(pe,
			WithNotifier(&mockNotifier{}),
			WithApprover(&mockApprover{approve: true})),
		NewHookInterceptor(nil),
		NewSandboxInterceptor(nil, fg, np, true),
	})

	exec := func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
		return &ToolResult{Output: "executed: " + call.ToolName}, nil
	}

	tests := []struct {
		name     string
		call     *ToolCall
		wantExec bool
	}{
		{"bash ls allowed", &ToolCall{ToolName: "bash", Input: `{"command":"ls -la"}`}, true},
		{"bash rm denied", &ToolCall{ToolName: "bash", Input: `{"command":"rm -rf /"}`}, false},
		{"file_write /tmp allowed", &ToolCall{ToolName: "file_write", Input: `{"path":"/tmp/test.txt","content":"ok"}`}, true},
		{"file_write /etc denied", &ToolCall{ToolName: "file_write", Input: `{"path":"/etc/test.txt","content":"ok"}`}, false},
		{"file_patch /tmp allowed", &ToolCall{ToolName: "file_patch", Input: `{"path":"/tmp/test.txt","patch":"@@ -1 +1 @@\n-a\n+b"}`}, true},
		{"file_patch /etc denied", &ToolCall{ToolName: "file_patch", Input: `{"path":"/etc/test.txt","patch":"@@ -1 +1 @@\n-a\n+b"}`}, false},
		{"http safe allowed", &ToolCall{ToolName: "http", Input: `{"method":"GET","url":"http://safe.com/"}`}, true},
		{"http evil denied", &ToolCall{ToolName: "http", Input: `{"method":"GET","url":"http://evil.com/"}`}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := chain.Execute(context.Background(), tt.call, exec)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			executed := res.Error == ""
			if executed != tt.wantExec {
				t.Errorf("executed=%v, want %v (error=%q)", executed, tt.wantExec, res.Error)
			}
		})
	}
}
