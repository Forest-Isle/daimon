package action

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/tool"
)

func okFinal(_ context.Context, _ *tool.ToolCall) (*tool.ToolResult, error) {
	return &tool.ToolResult{Output: "done"}, nil
}

// loadUndoSpec returns the single undo_journal row's spec, failing if the row
// count is not as expected.
func loadUndoSpec(t *testing.T, store *Store, wantRows int) string {
	t.Helper()
	ctx := context.Background()
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM undo_journal`).Scan(&count); err != nil {
		t.Fatalf("count undo_journal: %v", err)
	}
	if count != wantRows {
		t.Fatalf("undo_journal rows = %d, want %d", count, wantRows)
	}
	if wantRows == 0 {
		return ""
	}
	var spec string
	if err := store.db.QueryRowContext(ctx, `SELECT undo_spec FROM undo_journal LIMIT 1`).Scan(&spec); err != nil {
		t.Fatalf("read undo_spec: %v", err)
	}
	return spec
}

func TestInterceptorRecordsFileUndoNewFile(t *testing.T) {
	store := openActionTestStore(t)
	ic := NewInterceptor(store, nil)
	path := filepath.Join(t.TempDir(), "new.txt") // does not exist yet
	input, _ := json.Marshal(map[string]any{"path": path, "content": "hello"})

	call := &tool.ToolCall{ToolName: "file_write", Input: string(input)}
	res, err := ic.Intercept(context.Background(), call, okFinal)
	if err != nil {
		t.Fatalf("Intercept() error = %v", err)
	}
	if res.Metadata["receipt_id"] == "" {
		t.Fatal("expected receipt_id stamped on result")
	}
	var snap fileUndoSnapshot
	if err := json.Unmarshal([]byte(loadUndoSpec(t, store, 1)), &snap); err != nil {
		t.Fatalf("decode undo spec: %v", err)
	}
	if snap.Op != "restore" || snap.Path != path || snap.Existed {
		t.Fatalf("snapshot = %#v, want restore/new-file (existed=false)", snap)
	}
}

func TestInterceptorRecordsFileUndoOverwrite(t *testing.T) {
	store := openActionTestStore(t)
	ic := NewInterceptor(store, nil)
	path := filepath.Join(t.TempDir(), "exists.txt")
	if err := os.WriteFile(path, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"path": path, "old_string": "old", "new_string": "new"})

	call := &tool.ToolCall{ToolName: "file_edit", Input: string(input)}
	if _, err := ic.Intercept(context.Background(), call, okFinal); err != nil {
		t.Fatalf("Intercept() error = %v", err)
	}
	var snap fileUndoSnapshot
	if err := json.Unmarshal([]byte(loadUndoSpec(t, store, 1)), &snap); err != nil {
		t.Fatalf("decode undo spec: %v", err)
	}
	if !snap.Existed || snap.Prev != "old content" || snap.Truncated {
		t.Fatalf("snapshot = %#v, want existed with prev content", snap)
	}
}

func TestInterceptorTruncatesLargeFileUndo(t *testing.T) {
	store := openActionTestStore(t)
	ic := NewInterceptor(store, nil)
	path := filepath.Join(t.TempDir(), "big.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", fileUndoMaxBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"path": path, "content": "small"})

	call := &tool.ToolCall{ToolName: "file_write", Input: string(input)}
	if _, err := ic.Intercept(context.Background(), call, okFinal); err != nil {
		t.Fatalf("Intercept() error = %v", err)
	}
	var snap fileUndoSnapshot
	if err := json.Unmarshal([]byte(loadUndoSpec(t, store, 1)), &snap); err != nil {
		t.Fatalf("decode undo spec: %v", err)
	}
	if !snap.Existed || !snap.Truncated || snap.Prev != "" {
		t.Fatalf("snapshot = %#v, want truncated without prev", snap)
	}
}

func TestInterceptorSymlinkNotSnapshotted(t *testing.T) {
	store := openActionTestStore(t)
	ic := NewInterceptor(store, nil)
	dir := t.TempDir()
	target := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(target, []byte("out of tree content"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	input, _ := json.Marshal(map[string]any{"path": link, "content": "x"})

	call := &tool.ToolCall{ToolName: "file_write", Input: string(input)}
	if _, err := ic.Intercept(context.Background(), call, okFinal); err != nil {
		t.Fatalf("Intercept() error = %v", err)
	}
	var snap fileUndoSnapshot
	if err := json.Unmarshal([]byte(loadUndoSpec(t, store, 1)), &snap); err != nil {
		t.Fatalf("decode undo spec: %v", err)
	}
	// A symlink must not have its target content captured into the journal.
	if !snap.Truncated || snap.Prev != "" {
		t.Fatalf("snapshot = %#v, want truncated symlink without prev", snap)
	}
}

func TestInterceptorNonFileToolNoUndo(t *testing.T) {
	store := openActionTestStore(t)
	ic := NewInterceptor(store, nil)

	// world_edit is reversible and governed, but not a file-snapshot tool: it
	// records a trust attempt but no undo row.
	call := &tool.ToolCall{ToolName: "world_edit"}
	res, err := ic.Intercept(context.Background(), call, okFinal)
	if err != nil {
		t.Fatalf("Intercept() error = %v", err)
	}
	if res.Metadata["receipt_id"] != "" {
		t.Fatalf("non-file tool should not stamp receipt_id, got %q", res.Metadata["receipt_id"])
	}
	loadUndoSpec(t, store, 0)
}

func TestInterceptorFailedFileWriteNoUndo(t *testing.T) {
	store := openActionTestStore(t)
	ic := NewInterceptor(store, nil)
	path := filepath.Join(t.TempDir(), "fail.txt")
	input, _ := json.Marshal(map[string]any{"path": path, "content": "x"})

	failFinal := func(_ context.Context, _ *tool.ToolCall) (*tool.ToolResult, error) {
		return &tool.ToolResult{Error: "disk full"}, nil
	}
	call := &tool.ToolCall{ToolName: "file_write", Input: string(input)}
	if _, err := ic.Intercept(context.Background(), call, failFinal); err != nil {
		t.Fatalf("Intercept() error = %v", err)
	}
	// A failed execution must not record an undo entry.
	loadUndoSpec(t, store, 0)
}

func TestExecuteUndoRestoresPrev(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "f.txt")
	if err := os.WriteFile(path, []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := undoEntryForTest(t, fileUndoSnapshot{Op: "restore", Path: path, Existed: true, Prev: "previous"})

	if err := ExecuteUndo(context.Background(), root, entry); err != nil {
		t.Fatalf("ExecuteUndo() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "previous" {
		t.Fatalf("content = %q, want previous", got)
	}
}

func TestExecuteUndoDeletesCreatedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "f.txt")
	if err := os.WriteFile(path, []byte("created"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := undoEntryForTest(t, fileUndoSnapshot{Op: "restore", Path: path, Existed: false})

	if err := ExecuteUndo(context.Background(), root, entry); err != nil {
		t.Fatalf("ExecuteUndo() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Stat() error = %v, want not exist", err)
	}
}

func TestExecuteUndoDeleteIdempotent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "missing.txt")
	entry := undoEntryForTest(t, fileUndoSnapshot{Op: "restore", Path: path, Existed: false})

	if err := ExecuteUndo(context.Background(), root, entry); err != nil {
		t.Fatalf("ExecuteUndo() error = %v", err)
	}
}

func TestExecuteUndoRefusesTruncated(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "f.txt")
	if err := os.WriteFile(path, []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := undoEntryForTest(t, fileUndoSnapshot{Op: "restore", Path: path, Existed: true, Prev: "previous", Truncated: true})

	if err := ExecuteUndo(context.Background(), root, entry); err == nil {
		t.Fatal("ExecuteUndo() error = nil, want error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "current" {
		t.Fatalf("content = %q, want current", got)
	}
}

func TestExecuteUndoRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("target"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	entry := undoEntryForTest(t, fileUndoSnapshot{Op: "restore", Path: link, Existed: true, Prev: "previous"})

	if err := ExecuteUndo(context.Background(), root, entry); err == nil {
		t.Fatal("ExecuteUndo() error = nil, want error")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "target" {
		t.Fatalf("target content = %q, want target", got)
	}
}

func TestExecuteUndoRefusesEscapeRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := undoEntryForTest(t, fileUndoSnapshot{Op: "restore", Path: outside, Existed: true, Prev: "previous"})

	if err := ExecuteUndo(context.Background(), root, entry); err == nil {
		t.Fatal("ExecuteUndo() error = nil, want error")
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "outside" {
		t.Fatalf("outside content = %q, want outside", got)
	}
}

func TestExecuteUndoRefusesIntermediateSymlinkRestore(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "f.txt")
	if err := os.WriteFile(outsidePath, []byte("orig"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	entry := undoEntryForTest(t, fileUndoSnapshot{Op: "restore", Path: filepath.Join(link, "f.txt"), Existed: true, Prev: "x"})

	if err := ExecuteUndo(context.Background(), root, entry); err == nil {
		t.Fatal("ExecuteUndo() error = nil, want error")
	}
	got, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "orig" {
		t.Fatalf("outside content = %q, want orig", got)
	}
}

func TestExecuteUndoRefusesIntermediateSymlinkDelete(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "f.txt")
	if err := os.WriteFile(outsidePath, []byte("orig"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	entry := undoEntryForTest(t, fileUndoSnapshot{Op: "restore", Path: filepath.Join(link, "f.txt"), Existed: false})

	if err := ExecuteUndo(context.Background(), root, entry); err == nil {
		t.Fatal("ExecuteUndo() error = nil, want error")
	}
	if _, err := os.Stat(outsidePath); err != nil {
		t.Fatalf("outside file stat error = %v, want still exists", err)
	}
}

func TestStoreUndoMarksUndone(t *testing.T) {
	store := openActionTestStore(t)
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "f.txt")
	if err := os.WriteFile(path, []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := undoSpecForTest(t, fileUndoSnapshot{Op: "restore", Path: path, Existed: true, Prev: "previous"})
	if err := store.RecordUndo(ctx, UndoRecord{ReceiptID: "r1", ToolName: "file_write", UndoSpec: spec}); err != nil {
		t.Fatalf("RecordUndo() error = %v", err)
	}

	if err := store.Undo(ctx, root, "r1"); err != nil {
		t.Fatalf("Undo() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "previous" {
		t.Fatalf("content = %q, want previous", got)
	}
	entry, err := store.GetUndo(ctx, "r1")
	if err != nil {
		t.Fatalf("GetUndo() error = %v", err)
	}
	if entry.UndoneAt == "" {
		t.Fatal("UndoneAt empty, want mark")
	}
	if err := store.Undo(ctx, root, "r1"); !errors.Is(err, ErrUndoAlreadyDone) {
		t.Fatalf("Undo() error = %v, want ErrUndoAlreadyDone", err)
	}
}

func TestGetUndoNotFound(t *testing.T) {
	store := openActionTestStore(t)
	if _, err := store.GetUndo(context.Background(), "missing"); !errors.Is(err, ErrUndoNotFound) {
		t.Fatalf("GetUndo() error = %v, want ErrUndoNotFound", err)
	}
}

func undoEntryForTest(t *testing.T, snap fileUndoSnapshot) UndoEntry {
	t.Helper()
	return UndoEntry{ReceiptID: "r1", ToolName: "file_write", UndoSpec: undoSpecForTest(t, snap)}
}

func undoSpecForTest(t *testing.T, snap fileUndoSnapshot) string {
	t.Helper()
	spec, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	return string(spec)
}
