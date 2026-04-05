# File Tools Refinement Design — Split into 4 Focused Tools

**Date:** 2026-04-05
**Scope:** Replace monolithic `file` tool with `file_read`, `file_write`, `file_edit`, `file_list`

---

## Problem

The current `FileTool` is a single tool with an `action` parameter for read/write/list. This causes:
1. **No concurrent reads** — The tool is marked `IsReadOnly: false` because it CAN write. So read operations can't participate in parallel execution.
2. **No diff editing** — Only full-file write exists. LLM must rewrite entire files for small changes, wasting tokens and risking data loss.
3. **Imprecise LLM invocation** — A single tool with multiple actions is harder for the LLM to call correctly than focused single-purpose tools.

## Design: 4 Independent Tools

### file_read (read-only, concurrent-safe)

**File:** `internal/tool/file_read.go`

```go
type FileReadTool struct{}

func (t *FileReadTool) Name() string { return "file_read" }
func (t *FileReadTool) IsReadOnly() bool { return true }
func (t *FileReadTool) RequiresApproval() bool { return false }
```

**Input schema:**
```json
{
  "path": "string (required) — absolute or relative file path",
  "offset": "int (optional) — start reading from this line number (1-based)",
  "limit": "int (optional) — max lines to return"
}
```

**Behavior:**
- Read entire file by default
- With offset/limit: read specific line range (useful for large files)
- Truncate output at `maxOutputSize` (64KB) with "[truncated]" marker
- Return line numbers in output (cat -n format): `  1\tcontent`
- Return `Result{Type: ResultFile, FilePath: path}`

### file_write (write, requires approval)

**File:** `internal/tool/file_write.go`

```go
type FileWriteTool struct{ approval bool }

func (t *FileWriteTool) Name() string { return "file_write" }
func (t *FileWriteTool) IsReadOnly() bool { return false }
func (t *FileWriteTool) RequiresApproval() bool { return t.approval }
```

**Input schema:**
```json
{
  "path": "string (required) — file path to write",
  "content": "string (required) — full file content"
}
```

**Behavior:**
- Create parent directories if needed (`os.MkdirAll`)
- Write full content with `os.WriteFile` (0644 perms)
- Return confirmation message with path

### file_edit (write, requires approval) — NEW CAPABILITY

**File:** `internal/tool/file_edit.go`

```go
type FileEditTool struct{ approval bool }

func (t *FileEditTool) Name() string { return "file_edit" }
func (t *FileEditTool) IsReadOnly() bool { return false }
func (t *FileEditTool) RequiresApproval() bool { return t.approval }
```

**Input schema:**
```json
{
  "path": "string (required) — file to edit",
  "old_string": "string (required) — exact text to find and replace",
  "new_string": "string (required) — replacement text",
  "replace_all": "bool (optional, default false) — replace all occurrences"
}
```

**Behavior:**
1. Read current file content
2. Count occurrences of `old_string`
3. If count == 0: return error "old_string not found in file"
4. If count > 1 AND `replace_all` is false: return error "old_string matches N times — provide more context to make it unique, or set replace_all=true"
5. If count == 1 OR `replace_all` is true: perform replacement
6. Write modified content back
7. Return confirmation with number of replacements made

**Why this matters:** The LLM sends only the changed portion, not the entire file. For a 500-line file where 2 lines change, this saves ~498 lines of tokens.

### file_list (read-only, concurrent-safe)

**File:** `internal/tool/file_list.go`

```go
type FileListTool struct{}

func (t *FileListTool) Name() string { return "file_list" }
func (t *FileListTool) IsReadOnly() bool { return true }
func (t *FileListTool) RequiresApproval() bool { return false }
```

**Input schema:**
```json
{
  "path": "string (required) — directory path"
}
```

**Behavior:**
- Same as current list logic: `os.ReadDir`, prefix dirs with "d ", files with "  "
- Return `Result{Type: ResultText}`

## Config Changes

No new config fields needed. The existing `FileToolConfig.Enabled` controls whether all file tools are registered. `RequiresApproval` applies to write/edit tools only.

## Gateway Registration

In `internal/gateway/init_tools.go`, replace:
```go
if gw.cfg.Tools.File.Enabled {
    gw.tools.Register(tool.NewFileTool(gw.cfg.Tools.File.RequiresApproval))
}
```
With:
```go
if gw.cfg.Tools.File.Enabled {
    gw.tools.Register(tool.NewFileReadTool())
    gw.tools.Register(tool.NewFileWriteTool(gw.cfg.Tools.File.RequiresApproval))
    gw.tools.Register(tool.NewFileEditTool(gw.cfg.Tools.File.RequiresApproval))
    gw.tools.Register(tool.NewFileListTool())
}
```

## Files Changed

| Action | File |
|--------|------|
| Delete | `internal/tool/file.go` |
| Create | `internal/tool/file_read.go` |
| Create | `internal/tool/file_read_test.go` |
| Create | `internal/tool/file_write.go` |
| Create | `internal/tool/file_write_test.go` |
| Create | `internal/tool/file_edit.go` |
| Create | `internal/tool/file_edit_test.go` |
| Create | `internal/tool/file_list.go` |
| Create | `internal/tool/file_list_test.go` |
| Modify | `internal/gateway/init_tools.go` |

## Testing Strategy

Each tool gets its own test file using `t.TempDir()` for file operations:
- **file_read:** read normal file, read with offset/limit, read nonexistent file, large file truncation
- **file_write:** write new file, overwrite existing, create parent dirs, invalid path
- **file_edit:** single match replace, no match error, multiple match error, replace_all, old==new error
- **file_list:** list directory, list nonexistent path, empty directory

## Concurrent Execution Impact

After this change, `file_read` and `file_list` will be marked `IsReadOnly: true`, allowing them to participate in the existing concurrent tool execution system (from `concurrent.go`). This means when the LLM calls both `file_read` and `file_list` in the same turn, they execute in parallel instead of sequentially.
