package agent

import (
	"testing"

	"github.com/punkopunko/ironclaw/internal/session"
)

func TestBuildForkMessages(t *testing.T) {
	parent := []session.Message{
		{ID: "1", Role: "user", Content: "hello"},
		{ID: "2", Role: "assistant", Content: "hi there"},
	}

	msgs := BuildForkMessages(parent, "fix the bug")

	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	last := msgs[2]
	if last.Role != "user" {
		t.Errorf("expected role 'user', got %q", last.Role)
	}
	if last.Content == "" {
		t.Error("fork directive content should not be empty")
	}

	if len(parent) != 2 {
		t.Errorf("original parent messages were mutated: len=%d", len(parent))
	}
}

func TestBuildForkMessages_EmptyParent(t *testing.T) {
	msgs := BuildForkMessages(nil, "do something")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", msgs[0].Role)
	}
}

func TestIsForkDirective(t *testing.T) {
	msgs := BuildForkMessages(nil, "test task")
	if !IsForkDirective(msgs[0]) {
		t.Error("expected fork directive to be detected")
	}

	normal := session.Message{Role: "user", Content: "normal message"}
	if IsForkDirective(normal) {
		t.Error("normal message should not be detected as fork directive")
	}
}

func TestCheckForkDepth(t *testing.T) {
	subCtx := &SubagentContext{Depth: MaxForkDepth}
	err := CheckForkDepth(subCtx)
	if err == nil {
		t.Error("expected error when depth equals MaxForkDepth")
	}

	subCtx.Depth = MaxForkDepth - 1
	err = CheckForkDepth(subCtx)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	subCtx.Depth = 0
	err = CheckForkDepth(subCtx)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheckForkDepth_NilContext(t *testing.T) {
	err := CheckForkDepth(nil)
	if err != nil {
		t.Errorf("nil context should be treated as depth 0: %v", err)
	}
}
