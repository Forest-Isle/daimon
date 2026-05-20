package hook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUserHookManagerDiscovery(t *testing.T) {
	dir := t.TempDir()
	writeHookScript(t, dir, "20_pre_tool_use_second.sh", "#!/bin/sh\nprintf second\n")
	writeHookScript(t, dir, "10_pre_tool_use_first.sh", "#!/bin/sh\nprintf first\n")
	writeHookScript(t, dir, "05_on_stop_cleanup.sh", "#!/bin/sh\nprintf stop\n")

	m := NewUserHookManager(dir, time.Second)
	hooks := m.ListHooks()

	if len(hooks) != 3 {
		t.Fatalf("expected 3 hooks, got %d", len(hooks))
	}
	if hooks[0].Name != "10_pre_tool_use_first.sh" {
		t.Fatalf("expected first hook sorted by priority, got %s", hooks[0].Name)
	}
	if !m.HasHooks(HookPreToolUse) {
		t.Fatal("expected pre_tool_use hooks to exist")
	}
	if !m.HasHooks(HookOnStop) {
		t.Fatal("expected on_stop hooks to exist")
	}
	if m.HasHooks(HookNotification) {
		t.Fatal("did not expect notification hooks")
	}
}

func TestUserHookManagerRunHooksJSONInput(t *testing.T) {
	dir := t.TempDir()
	writeHookScript(t, dir, "10_pre_tool_use_echo.sh", "#!/bin/sh\ncat\n")

	m := NewUserHookManager(dir, 10*time.Second)
	results := m.RunHooks(context.Background(), HookPreToolUse, map[string]any{
		"tool_name":  "bash",
		"tool_input": "git status",
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Success {
		t.Fatalf("expected hook to succeed: %+v", results[0])
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(results[0].Output), &payload); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if payload["tool_name"] != "bash" {
		t.Fatalf("expected tool_name bash, got %v", payload["tool_name"])
	}
	if payload["tool_input"] != "git status" {
		t.Fatalf("expected tool_input git status, got %v", payload["tool_input"])
	}
}

func TestUserHookManagerTimeoutHandling(t *testing.T) {
	dir := t.TempDir()
	writeHookScript(t, dir, "10_notification_sleep.sh", "#!/bin/sh\nsleep 2\nprintf done\n")

	m := NewUserHookManager(dir, 100*time.Millisecond)
	results := m.RunHooks(context.Background(), HookNotification, map[string]any{"message": "hello"})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Success {
		t.Fatalf("expected timeout failure, got success: %+v", results[0])
	}
	if results[0].ExitCode != -1 {
		t.Fatalf("expected exit code -1 for timeout, got %d", results[0].ExitCode)
	}
	if results[0].Error == "" {
		t.Fatal("expected timeout error message")
	}
}

func TestUserHookManagerPriorityOrdering(t *testing.T) {
	dir := t.TempDir()
	writeHookScript(t, dir, "20_post_tool_use_second.sh", "#!/bin/sh\nprintf second\n")
	writeHookScript(t, dir, "10_post_tool_use_first.sh", "#!/bin/sh\nprintf first\n")

	m := NewUserHookManager(dir, 10*time.Second)
	results := m.RunHooks(context.Background(), HookPostToolUse, map[string]any{
		"tool_name":   "bash",
		"tool_input":  "echo hi",
		"tool_output": "hi",
	})

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].HookName != "10_post_tool_use_first.sh" || results[0].Output != "first" {
		t.Fatalf("expected first hook to run first, got %+v", results[0])
	}
	if results[1].HookName != "20_post_tool_use_second.sh" || results[1].Output != "second" {
		t.Fatalf("expected second hook to run second, got %+v", results[1])
	}
}

func TestUserHookManagerReloadHooks(t *testing.T) {
	dir := t.TempDir()
	first := writeHookScript(t, dir, "10_on_stop_first.sh", "#!/bin/sh\nprintf one\n")

	m := NewUserHookManager(dir, time.Second)
	if len(m.ListHooks()) != 1 {
		t.Fatalf("expected 1 hook after initial load, got %d", len(m.ListHooks()))
	}

	second := writeHookScript(t, dir, "20_on_stop_second.sh", "#!/bin/sh\nprintf two\n")
	if err := m.ReloadHooks(); err != nil {
		t.Fatalf("reload after add: %v", err)
	}
	if len(m.ListHooks()) != 2 {
		t.Fatalf("expected 2 hooks after add, got %d", len(m.ListHooks()))
	}

	if err := os.Remove(first); err != nil {
		t.Fatalf("remove first hook: %v", err)
	}
	if err := m.ReloadHooks(); err != nil {
		t.Fatalf("reload after remove: %v", err)
	}

	hooks := m.ListHooks()
	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook after remove, got %d", len(hooks))
	}
	if hooks[0].Path != second {
		t.Fatalf("expected remaining hook %s, got %s", second, hooks[0].Path)
	}
}

func TestUserHookManagerEmptyDir(t *testing.T) {
	m := NewUserHookManager(t.TempDir(), time.Second)
	if got := m.ListHooks(); len(got) != 0 {
		t.Fatalf("expected no hooks, got %d", len(got))
	}
	if m.HasHooks(HookPreToolUse) {
		t.Fatal("expected no hooks for event")
	}
	if got := m.RunHooks(context.Background(), HookPreToolUse, map[string]any{"tool_name": "bash"}); len(got) != 0 {
		t.Fatalf("expected no results, got %d", len(got))
	}
}

func TestUserHookManagerSkipsNonExecutableFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "10_pre_tool_use_plain.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf skipped\n"), 0o644); err != nil {
		t.Fatalf("write non-executable file: %v", err)
	}

	m := NewUserHookManager(dir, time.Second)
	if got := m.ListHooks(); len(got) != 0 {
		t.Fatalf("expected non-executable hook to be skipped, got %d hooks", len(got))
	}
}

func writeHookScript(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write hook script %s: %v", name, err)
	}
	return path
}
