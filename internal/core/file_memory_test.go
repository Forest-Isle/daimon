package core_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/core"
)

func TestFileMemoryAppendSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conversation.jsonl")

	fm, err := core.NewFileMemory(path)
	if err != nil {
		t.Fatalf("NewFileMemory: %v", err)
	}

	msgs := []core.Message{
		{Role: core.RoleUser, Content: "hello"},
		{Role: core.RoleAssistant, Content: "hi there"},
		{Role: core.RoleUser, Content: "do a thing"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "u1", Name: "echo"}}},
		{Role: core.RoleTool, ToolUseID: "u1", Content: "result"},
		{Role: core.RoleAssistant, Content: "done"},
	}
	for i, m := range msgs {
		if err := fm.Append(context.Background(), m); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	snap, err := fm.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap) != len(msgs) {
		t.Fatalf("expected %d messages, got %d", len(msgs), len(snap))
	}
	for i, m := range msgs {
		if snap[i].Role != m.Role {
			t.Fatalf("[%d] role: got %s want %s", i, snap[i].Role, m.Role)
		}
		if snap[i].Content != m.Content {
			t.Fatalf("[%d] content: got %q want %q", i, snap[i].Content, m.Content)
		}
	}
}

func TestFileMemoryReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reload.jsonl")

	// First instance writes.
	fm1, err := core.NewFileMemory(path)
	if err != nil {
		t.Fatalf("NewFileMemory 1: %v", err)
	}
	if err := fm1.Append(context.Background(), core.Message{Role: core.RoleUser, Content: "first"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Second instance reads back the same file.
	fm2, err := core.NewFileMemory(path)
	if err != nil {
		t.Fatalf("NewFileMemory 2: %v", err)
	}
	snap, err := fm2.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap) != 1 || snap[0].Content != "first" {
		t.Fatalf("reload mismatch: %+v", snap)
	}
}

func TestFileMemoryEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	// Create empty file.
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	fm, err := core.NewFileMemory(path)
	if err != nil {
		t.Fatalf("NewFileMemory: %v", err)
	}
	snap, _ := fm.Snapshot(context.Background())
	if len(snap) != 0 {
		t.Fatalf("expected empty, got %d", len(snap))
	}
}

func TestFileMemoryStats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.jsonl")

	fm, err := core.NewFileMemory(path)
	if err != nil {
		t.Fatalf("NewFileMemory: %v", err)
	}
	_ = fm.Append(context.Background(), core.Message{Role: core.RoleUser, Content: "a"})
	_ = fm.Append(context.Background(), core.Message{Role: core.RoleAssistant, Content: "b"})
	_ = fm.Append(context.Background(), core.Message{Role: core.RoleUser, Content: "c"})
	_ = fm.Append(context.Background(), core.Message{Role: core.RoleTool, ToolUseID: "x", Content: "d"})

	msgCount, userTurns, toolCalls, _ := fm.Stats()
	if msgCount != 4 {
		t.Fatalf("msgCount=%d", msgCount)
	}
	if userTurns != 2 {
		t.Fatalf("userTurns=%d", userTurns)
	}
	if toolCalls != 1 {
		t.Fatalf("toolCalls=%d", toolCalls)
	}
}
