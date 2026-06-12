package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveWorkPath(t *testing.T) {
	// No workdir in context: path returned verbatim (the no-op default that
	// keeps all existing callers byte-for-byte identical).
	if got, err := ResolveWorkPath(context.Background(), "foo/bar.txt"); err != nil || got != "foo/bar.txt" {
		t.Errorf("empty workdir: got %q, want verbatim", got)
	}

	ctx := WithWorkDir(context.Background(), "/work/dir")
	// Relative path joins under the workdir.
	if got, err := ResolveWorkPath(ctx, "sub/file.txt"); err != nil || got != "/work/dir/sub/file.txt" {
		t.Errorf("relative path: got %q, want /work/dir/sub/file.txt", got)
	}
	// Absolute paths are allowed only when they remain inside the workdir.
	if got, err := ResolveWorkPath(ctx, "/work/dir/sub/file.txt"); err != nil || got != "/work/dir/sub/file.txt" {
		t.Errorf("absolute in workdir: got %q err %v", got, err)
	}
	if _, err := ResolveWorkPath(ctx, "/etc/hosts"); err == nil {
		t.Error("absolute path outside workdir should be rejected")
	}
	if _, err := ResolveWorkPath(ctx, "../outside.txt"); err == nil {
		t.Error("relative path outside workdir should be rejected")
	}
}

func TestWorkDirFromContext_AbsentIsEmpty(t *testing.T) {
	if got := WorkDirFromContext(context.Background()); got != "" {
		t.Errorf("absent workdir should be empty, got %q", got)
	}
}

// TestFileToolsHonorWorkDir is the integration guard: with a workdir in context,
// a relative write lands under the workdir and a relative read finds it. This
// would fail if file_write/file_read resolved paths inconsistently.
func TestFileToolsHonorWorkDir(t *testing.T) {
	workdir := t.TempDir()
	ctx := WithWorkDir(context.Background(), workdir)

	w := NewFileWriteTool(false)
	if res, err := w.Execute(ctx, []byte(`{"path":"out/note.txt","content":"hello"}`)); err != nil || res.Error != "" {
		t.Fatalf("write failed: err=%v res=%+v", err, res)
	}

	// The file must physically exist under the workdir, not the process cwd.
	got, err := os.ReadFile(filepath.Join(workdir, "out", "note.txt"))
	if err != nil {
		t.Fatalf("file not created under workdir: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want hello", got)
	}

	// And the read tool, given the same relative path + workdir, must find it.
	r := NewFileReadTool()
	res, err := r.Execute(ctx, []byte(`{"path":"out/note.txt"}`))
	if err != nil || res.Error != "" {
		t.Fatalf("read failed: err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Output, "hello") {
		t.Errorf("read output = %q, want it to contain hello", res.Output)
	}
}

// TestFileToolsDefaultUnchanged verifies the no-op path: with no workdir, the
// write tool uses the path as given (here an absolute temp path).
func TestFileToolsDefaultUnchanged(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "direct.txt")

	w := NewFileWriteTool(false)
	if res, err := w.Execute(context.Background(), []byte(`{"path":"`+abs+`","content":"x"}`)); err != nil || res.Error != "" {
		t.Fatalf("write failed: err=%v res=%+v", err, res)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Errorf("file not at expected absolute path: %v", err)
	}
}
