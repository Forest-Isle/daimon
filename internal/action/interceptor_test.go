package action

import (
	"context"
	"errors"
	"testing"

	"github.com/Forest-Isle/daimon/internal/tool"
)

func TestClassifyReadOnlyNotGoverned(t *testing.T) {
	c := NewClassifier()
	call := &tool.ToolCall{ToolName: "world_read", Capabilities: tool.ToolCapabilities{IsReadOnly: true}}
	_, governed := c.Classify(call)
	if governed {
		t.Fatal("read-only tool should not be governed")
	}
}

func TestClassifyBashByCommand(t *testing.T) {
	c := NewClassifier()
	cases := []struct {
		cmd  string
		want Class
	}{
		{`{"command":"ls -la"}`, Reversible},
		{`{"command":"go test ./..."}`, Reversible},
		{`{"command":"rm -rf build"}`, Irreversible},
		{`{"command":"git push --force origin main"}`, Irreversible},
		{`{"command":"dd if=/dev/zero of=disk"}`, Irreversible},
	}
	for _, tc := range cases {
		call := &tool.ToolCall{ToolName: "bash", Input: tc.cmd}
		got, governed := c.Classify(call)
		if !governed {
			t.Fatalf("bash %q should be governed", tc.cmd)
		}
		if got != tc.want {
			t.Fatalf("bash %q classified %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestClassifyDestructiveCapability(t *testing.T) {
	c := NewClassifier()
	call := &tool.ToolCall{ToolName: "danger_tool", Capabilities: tool.ToolCapabilities{IsDestructive: true}}
	got, governed := c.Classify(call)
	if !governed || got != Irreversible {
		t.Fatalf("destructive tool classified %v governed=%v, want Irreversible/true", got, governed)
	}
}

func TestClassifyDefaultReversible(t *testing.T) {
	c := NewClassifier()
	call := &tool.ToolCall{ToolName: "world_edit"}
	got, governed := c.Classify(call)
	if !governed || got != Reversible {
		t.Fatalf("plain mutating tool classified %v governed=%v, want Reversible/true", got, governed)
	}
}

func TestInterceptorRecordsReversibleAndStamps(t *testing.T) {
	store := openActionTestStore(t)
	ic := NewInterceptor(store, nil)
	ctx := context.Background()

	call := &tool.ToolCall{ToolName: "world_edit"}
	final := func(_ context.Context, _ *tool.ToolCall) (*tool.ToolResult, error) {
		return &tool.ToolResult{Output: "done"}, nil
	}
	res, err := ic.Intercept(ctx, call, final)
	if err != nil {
		t.Fatalf("Intercept() error = %v", err)
	}
	if res.Metadata["action_class"] != "reversible" {
		t.Fatalf("metadata action_class = %q, want reversible", res.Metadata["action_class"])
	}
	// reversible success earns a verified attempt → level promoted to AskFirst
	lvl, err := store.TrustLevel(ctx, Reversible, "world_edit")
	if err != nil {
		t.Fatalf("TrustLevel() error = %v", err)
	}
	if lvl != AskFirst {
		t.Fatalf("level = %v, want AskFirst", lvl)
	}
}

func TestInterceptorReadOnlyNotRecorded(t *testing.T) {
	store := openActionTestStore(t)
	ic := NewInterceptor(store, nil)
	ctx := context.Background()

	call := &tool.ToolCall{ToolName: "world_read", Capabilities: tool.ToolCapabilities{IsReadOnly: true}}
	final := func(_ context.Context, _ *tool.ToolCall) (*tool.ToolResult, error) {
		return &tool.ToolResult{Output: "data"}, nil
	}
	if _, err := ic.Intercept(ctx, call, final); err != nil {
		t.Fatalf("Intercept() error = %v", err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trust_ledger`).Scan(&count); err != nil {
		t.Fatalf("count error = %v", err)
	}
	if count != 0 {
		t.Fatalf("trust_ledger rows = %d, want 0 for read-only", count)
	}
}

func TestInterceptorIrreversibleStaysAskEvery(t *testing.T) {
	store := openActionTestStore(t)
	ic := NewInterceptor(store, nil)
	ctx := context.Background()

	final := func(_ context.Context, _ *tool.ToolCall) (*tool.ToolResult, error) {
		return &tool.ToolResult{Output: "ok"}, nil
	}
	// many successful destructive bash runs must NOT auto-promote: irreversible
	// actions don't earn autonomy from mere execution success.
	for i := 0; i < 15; i++ {
		call := &tool.ToolCall{ToolName: "bash", Input: `{"command":"rm -rf tmp"}`}
		if _, err := ic.Intercept(ctx, call, final); err != nil {
			t.Fatalf("Intercept() error = %v", err)
		}
	}
	lvl, err := store.TrustLevel(ctx, Irreversible, "bash")
	if err != nil {
		t.Fatalf("TrustLevel() error = %v", err)
	}
	if lvl != AskEvery {
		t.Fatalf("level = %v, want AskEvery (irreversible never auto-promotes)", lvl)
	}
}

func TestInterceptorFailureNotVerified(t *testing.T) {
	store := openActionTestStore(t)
	ic := NewInterceptor(store, nil)
	ctx := context.Background()

	call := &tool.ToolCall{ToolName: "world_edit"}
	final := func(_ context.Context, _ *tool.ToolCall) (*tool.ToolResult, error) {
		return nil, errors.New("boom")
	}
	if _, err := ic.Intercept(ctx, call, final); err == nil {
		t.Fatal("Intercept() error = nil, want propagated error")
	}
	// failed execution records an attempt but not a verified one → no promotion
	lvl, err := store.TrustLevel(ctx, Reversible, "world_edit")
	if err != nil {
		t.Fatalf("TrustLevel() error = %v", err)
	}
	if lvl != AskEvery {
		t.Fatalf("level = %v, want AskEvery after a failed attempt", lvl)
	}
}
