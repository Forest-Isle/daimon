package action

import (
	"context"
	"encoding/json"
	"io"
	"os"

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
