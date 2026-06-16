package action

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/Forest-Isle/daimon/internal/tool"
)

// fileUndoMaxBytes caps how much prior content is snapshotted for undo. Beyond
// this the snapshot is skipped (recorded as truncated): keeping multi-MB blobs in
// the undo journal is not worth the storage, and such writes are rare.
const fileUndoMaxBytes = 256 * 1024

// fileMutatingTools are the reversible file tools whose writes are snapshotted
// for undo. bash and world_edit have their own reversal stories and are excluded.
var fileMutatingTools = map[string]bool{
	"file_write": true,
	"file_edit":  true,
	"file_patch": true,
}

// fileUndoSnapshot is the JSON shape stored in undo_journal.undo_spec for a
// reversible file mutation. Existed=false means the action created the file, so
// undo deletes it. Truncated means prior content could not be captured (too
// large, or a non-regular file), so an undo executor must treat the action as
// non-restorable rather than blindly writing Prev.
type fileUndoSnapshot struct {
	Op        string `json:"op"` // always "restore"
	Path      string `json:"path"`
	Existed   bool   `json:"existed"`
	Prev      string `json:"prev,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func ExecuteUndo(ctx context.Context, root string, entry UndoEntry) error {
	var snap fileUndoSnapshot
	if err := json.Unmarshal([]byte(entry.UndoSpec), &snap); err != nil {
		return fmt.Errorf("decode undo spec: %w", err)
	}
	if snap.Op != "restore" {
		return fmt.Errorf("unknown undo operation %q", snap.Op)
	}
	resolved, err := tool.ResolveWorkPath(tool.WithWorkDir(ctx, root), snap.Path)
	if err != nil {
		return fmt.Errorf("resolve undo path: %w", err)
	}
	real, err := fencedRealPath(root, resolved)
	if err != nil {
		return fmt.Errorf("fence undo path: %w", err)
	}
	if snap.Truncated {
		return fmt.Errorf("undo for %q is not reversible: content not captured", snap.Path)
	}
	if !snap.Existed {
		if err := os.Remove(real); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete created file: %w", err)
		}
		return nil
	}

	info, err := os.Lstat(real)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat undo path: %w", err)
	}
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to restore through symlink %q", snap.Path)
	}
	if err := os.WriteFile(real, []byte(snap.Prev), 0o644); err != nil {
		return fmt.Errorf("restore previous content: %w", err)
	}
	return nil
}

// fencedRealPath resolves resolved's parent through symlinks and re-verifies it
// stays within root, defeating an intermediate-symlink escape (a symlink inside
// the work root pointing outside). It returns the real target path to operate on.
func fencedRealPath(root, resolved string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(resolved)
	// Fail closed if the parent directory no longer exists: undo cannot safely
	// restore into a disappeared directory tree.
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realRoot, realParent)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q escapes working directory after symlink resolution", resolved)
	}
	return filepath.Join(realParent, filepath.Base(resolved)), nil
}

// pathFromInput extracts the "path" argument shared by file_write/file_edit/
// file_patch.
func pathFromInput(input string) string {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return ""
	}
	return in.Path
}

// captureFileUndo snapshots a file's current state before a mutation so the
// action can be reversed later. It returns ok=false when there is nothing to
// capture (not a file tool, no path, or unresolvable) — capture is best-effort
// and must never block or alter the tool's own execution.
//
// Best-effort caveats (documented, deferred to later increments): the snapshot
// is taken before the tool runs, so an external process mutating the file during
// execution is not detected; file_patch is keyed only by its primary "path".
func captureFileUndo(ctx context.Context, toolName, input string) (UndoRecord, bool) {
	if !fileMutatingTools[toolName] {
		return UndoRecord{}, false
	}
	rawPath := pathFromInput(input)
	if rawPath == "" {
		return UndoRecord{}, false
	}
	// Resolve the path exactly as the file tools do (fenced to the working dir),
	// so the snapshot targets the same file the tool will write — not a same-named
	// file under the process cwd.
	path, err := tool.ResolveWorkPath(ctx, rawPath)
	if err != nil {
		return UndoRecord{}, false
	}

	snap := fileUndoSnapshot{Op: "restore", Path: path}
	// Lstat, not Stat: never follow a symlink, and detect non-regular files
	// (FIFO/device/dir) that could block a read or pull in out-of-tree content.
	info, err := os.Lstat(path)
	switch {
	case err != nil && os.IsNotExist(err):
		snap.Existed = false // undo = delete the created file
	case err != nil:
		return UndoRecord{}, false // can't stat → skip, never block the tool
	case !info.Mode().IsRegular():
		snap.Existed = true
		snap.Truncated = true // symlink/dir/device: present but not safely snapshottable
	default:
		snap.Existed = true
		if prev, ok := readPrev(path); ok {
			snap.Prev = prev
		} else {
			snap.Truncated = true // too large or unreadable → non-restorable
		}
	}

	spec, err := json.Marshal(snap)
	if err != nil {
		return UndoRecord{}, false
	}
	return UndoRecord{
		ReceiptID: "receipt_" + uuid.NewString(),
		ToolName:  toolName,
		UndoSpec:  string(spec),
	}, true
}

// readPrev reads up to fileUndoMaxBytes of a regular file for undo. It limits the
// read itself (rather than trusting a prior size check) so a file growing between
// stat and read cannot pull in an oversized blob. ok=false means too large or
// unreadable.
func readPrev(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, fileUndoMaxBytes+1))
	if err != nil {
		return "", false
	}
	if len(data) > fileUndoMaxBytes {
		return "", false
	}
	return string(data), true
}
