package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadBeforeEditBlocksExistingFileWithoutRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	interceptor := NewReadBeforeEditInterceptor(nil)
	ctx := readBeforeEditTestContext(dir, "sess-1")
	called := false

	result, err := interceptor.Intercept(ctx, &ToolCall{
		ToolName:  "file_edit",
		SessionID: "sess-1",
		Input:     `{"path":"target.txt","old_string":"before","new_string":"after"}`,
	}, func(context.Context, *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "edited"}, nil
	})
	if err != nil {
		t.Fatalf("Intercept() error = %v", err)
	}
	if called {
		t.Fatal("next should not be called for unread existing file")
	}
	if result == nil || !strings.Contains(result.Error, "read-before-edit required") {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestReadBeforeEditAllowsAfterSuccessfulRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	interceptor := NewReadBeforeEditInterceptor(nil)
	ctx := readBeforeEditTestContext(dir, "sess-1")

	if _, err := interceptor.Intercept(ctx, &ToolCall{
		ToolName:  "file_read",
		SessionID: "sess-1",
		Input:     `{"path":"target.txt"}`,
	}, func(context.Context, *ToolCall) (*ToolResult, error) {
		return &ToolResult{Output: "before"}, nil
	}); err != nil {
		t.Fatalf("file_read intercept error = %v", err)
	}

	called := false
	result, err := interceptor.Intercept(ctx, &ToolCall{
		ToolName:  "file_edit",
		SessionID: "sess-1",
		Input:     `{"path":"target.txt","old_string":"before","new_string":"after"}`,
	}, func(context.Context, *ToolCall) (*ToolResult, error) {
		called = true
		if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return &ToolResult{Output: "edited"}, nil
	})
	if err != nil {
		t.Fatalf("file_edit intercept error = %v", err)
	}
	if !called {
		t.Fatal("next should be called after read")
	}
	if result == nil || result.Error != "" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestReadBeforeEditDetectsStaleRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	interceptor := NewReadBeforeEditInterceptor(nil)
	ctx := readBeforeEditTestContext(dir, "sess-1")
	if _, err := interceptor.Intercept(ctx, &ToolCall{
		ToolName:  "file_read",
		SessionID: "sess-1",
		Input:     `{"path":"target.txt"}`,
	}, func(context.Context, *ToolCall) (*ToolResult, error) {
		return &ToolResult{Output: "before"}, nil
	}); err != nil {
		t.Fatalf("file_read intercept error = %v", err)
	}

	if err := os.WriteFile(path, []byte("changed outside session\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	result, err := interceptor.Intercept(ctx, &ToolCall{
		ToolName:  "file_patch",
		SessionID: "sess-1",
		Input:     `{"path":"target.txt","patch":"@@ -1 +1 @@\n-changed outside session\n+after\n"}`,
	}, func(context.Context, *ToolCall) (*ToolResult, error) {
		called = true
		return &ToolResult{Output: "patched"}, nil
	})
	if err != nil {
		t.Fatalf("file_patch intercept error = %v", err)
	}
	if called {
		t.Fatal("next should not be called for stale read")
	}
	if result == nil || !strings.Contains(result.Error, "read-before-edit stale") {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestReadBeforeEditAllowsNewFileWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	interceptor := NewReadBeforeEditInterceptor(nil)
	ctx := readBeforeEditTestContext(dir, "sess-1")
	called := false

	result, err := interceptor.Intercept(ctx, &ToolCall{
		ToolName:  "file_write",
		SessionID: "sess-1",
		Input:     `{"path":"new.txt","content":"created\n"}`,
	}, func(context.Context, *ToolCall) (*ToolResult, error) {
		called = true
		if err := os.WriteFile(path, []byte("created\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return &ToolResult{Output: "written"}, nil
	})
	if err != nil {
		t.Fatalf("file_write intercept error = %v", err)
	}
	if !called {
		t.Fatal("new file write should be allowed")
	}
	if result == nil || result.Error != "" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func readBeforeEditTestContext(dir, sessionID string) context.Context {
	ctx := context.Background()
	ctx = WithWorkDir(ctx, dir)
	ctx = WithSessionID(ctx, sessionID)
	return ctx
}
