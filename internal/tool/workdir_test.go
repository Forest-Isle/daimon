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
	if got := ResolveWorkPath(context.Background(), "foo/bar.txt"); got != "foo/bar.txt" {
		t.Errorf("empty workdir: got %q, want verbatim", got)
	}

	ctx := WithWorkDir(context.Background(), "/work/dir")
	// Relative path joins under the workdir.
	if got := ResolveWorkPath(ctx, "sub/file.txt"); got != "/work/dir/sub/file.txt" {
		t.Errorf("relative path: got %q, want /work/dir/sub/file.txt", got)
	}
	// Absolute path passes through unchanged (no jail — deliberate).
	if got := ResolveWorkPath(ctx, "/etc/hosts"); got != "/etc/hosts" {
		t.Errorf("absolute path: got %q, want verbatim", got)
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
