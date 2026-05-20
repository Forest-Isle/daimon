package core_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/core"
)

func TestVerifierMiddlewareBashExitCode(t *testing.T) {
	reg := core.NewToolRegistry()
	reg.Register(&core.ToolFunc{
		S: core.ToolSchema{Name: "bash", Description: "bash", InputSchema: map[string]any{"type": "object"}},
		Fn: func(_ context.Context, _ json.RawMessage) (core.ToolResult, error) {
			return core.ToolResult{
				Output:   "command failed",
				Metadata: map[string]any{"exit_code": float64(1)},
			}, nil
		},
	})

	mw := core.VerifierMiddleware(core.BashVerifier{})
	ag := core.New(&fakeProvider{turns: []core.LLMResponse{
		{ToolCalls: []core.ToolCall{{ID: "u1", Name: "bash", Input: json.RawMessage(`{"cmd":"false"}`)}}, StopReason: core.StopToolUse},
		{Text: "the command failed", StopReason: core.StopEndTurn},
	}}, reg, nil, core.Config{ToolMiddleware: []core.ToolMiddleware{mw}})

	out, _, err := ag.Run(context.Background(), "run false")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "the command failed" {
		t.Fatalf("got %q", out)
	}

	// Re-run with injected memory for post-mortem verification.
	mem2 := core.NewInMemory()
	ag2 := core.New(&fakeProvider{turns: []core.LLMResponse{
		{ToolCalls: []core.ToolCall{{ID: "u1", Name: "bash", Input: json.RawMessage(`{"cmd":"false"}`)}}, StopReason: core.StopToolUse},
		{Text: "fixed", StopReason: core.StopEndTurn},
	}}, reg, mem2, core.Config{ToolMiddleware: []core.ToolMiddleware{mw}})
	ag2.Run(context.Background(), "run false")
	snap, _ := mem2.Snapshot(context.Background())
	for _, m := range snap {
		if m.Role == core.RoleTool && strings.Contains(m.Content, "VERIFY FAIL") {
			return // success
		}
	}
	t.Fatal("expected VERIFY FAIL in tool result")
}

func TestVerifierMiddlewarePassThrough(t *testing.T) {
	reg := core.NewToolRegistry()
	reg.Register(newEcho())

	mw := core.VerifierMiddleware(core.BashVerifier{}) // bash verifier, but we call echo
	mem := core.NewInMemory()
	ag := core.New(&fakeProvider{turns: []core.LLMResponse{
		{ToolCalls: []core.ToolCall{{ID: "u1", Name: "echo", Input: json.RawMessage(`{"message":"hi"}`)}}, StopReason: core.StopToolUse},
		{Text: "ok", StopReason: core.StopEndTurn},
	}}, reg, mem, core.Config{ToolMiddleware: []core.ToolMiddleware{mw}})

	if _, _, err := ag.Run(context.Background(), "echo"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snap, _ := mem.Snapshot(context.Background())
	for _, m := range snap {
		if m.Role == core.RoleTool && strings.Contains(m.Content, "VERIFY FAIL") {
			t.Fatal("echo should not trigger bash verifier")
		}
	}
}
