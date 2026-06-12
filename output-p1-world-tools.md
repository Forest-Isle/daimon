# Task P1-2 Output: world tools + gateway wiring

## Files added/changed

- Added `internal/tool/world.go`
  - `world_read`
  - `commitment`
  - `world_edit`
- Added `internal/tool/world_test.go`
  - Tool happy paths, validation errors, capabilities, and identity path fence tests.
- Changed `internal/gateway/subsystem_tool.go`
  - Constructs `world.Store` from the existing SQLite handle.
  - Constructs `world.Identity{Dir: <appdir>/world/identity}` and calls `EnsureDir`.
  - Registers `world_read`, `commitment`, and `world_edit` unconditionally.
- Changed `internal/gateway/gateway_test.go`
  - Added core registration assertion for the three world tools.
- Added `output-p1-world-tools.md`
  - This task summary.

Verification also rebuilt `bin/daimon` and used `.gocache` under the repo because of the sandbox.

## Tool schemas as the model sees them

### `world_read`

```json
{
  "name": "world_read",
  "description": "Read world state: identity digest, active commitments digest, or recent journal entries.",
  "input_schema": {
    "type": "object",
    "properties": {
      "section": {
        "type": "string",
        "enum": ["identity", "commitments", "journal"],
        "description": "World section to read (default: commitments)"
      },
      "limit": {
        "type": "integer",
        "description": "Maximum journal entries to return (journal only; default: 20)"
      },
      "due_within": {
        "type": "string",
        "description": "Only include commitments due at or before this timestamp (commitments only)"
      }
    }
  }
}
```

Capabilities: read-only, not destructive, `ApprovalMode: never`, `ParallelSafety: safe`.

### `commitment`

```json
{
  "name": "commitment",
  "description": "Create, update, or list commitments in the world model.",
  "input_schema": {
    "type": "object",
    "properties": {
      "action": {
        "type": "string",
        "enum": ["create", "update", "list"],
        "description": "Commitment action to perform"
      },
      "kind": {
        "type": "string",
        "enum": ["project", "promise", "deadline", "watch", "routine"],
        "description": "Commitment kind (required for create)"
      },
      "title": {
        "type": "string",
        "description": "Commitment title (required for create; optional for update)"
      },
      "body": {
        "type": "string",
        "description": "Commitment body/details"
      },
      "due_at": {
        "type": "string",
        "description": "Due timestamp, or empty string to clear on update"
      },
      "horizon": {
        "type": "string",
        "description": "Planning horizon"
      },
      "id": {
        "type": "string",
        "description": "Commitment ID (required for update)"
      },
      "state": {
        "type": "string",
        "enum": ["active", "waiting", "done", "dropped"],
        "description": "Commitment state (update only)"
      },
      "states": {
        "type": "array",
        "description": "States to include when listing commitments",
        "items": {
          "type": "string",
          "enum": ["active", "waiting", "done", "dropped"]
        }
      }
    },
    "required": ["action"]
  }
}
```

Capabilities: write-capable, not destructive, `ApprovalMode: auto`, `ParallelSafety: never`.

### `world_edit`

```json
{
  "name": "world_edit",
  "description": "Create or update an identity file inside the world identity directory. Content replaces the file unless append is true.",
  "input_schema": {
    "type": "object",
    "properties": {
      "file": {
        "type": "string",
        "description": "Relative identity file path, for example preferences/coding.md or profile.md"
      },
      "content": {
        "type": "string",
        "description": "Full replacement content, or content to append when append=true"
      },
      "append": {
        "type": "boolean",
        "description": "Append content instead of replacing the file"
      }
    },
    "required": ["file", "content"]
  }
}
```

Capabilities: write-capable, not destructive, `ApprovalMode: auto`, `ParallelSafety: path_scoped`.

## Fence test cases

`world_edit` happy paths:

- Writes `preferences/coding.md` with full replacement content.
- Appends to `preferences/coding.md` with `append: true`.
- Creates parent directories as needed.

Required-field validation:

- Rejects missing `file`.
- Rejects missing `content`.

Path fence cases:

- Rejects `../x` with an identity-root escape error.
- Rejects `/etc/x` because absolute paths are not allowed.
- Rejects `a/../../x` after `filepath.Clean` detects the escape.
- Rejects `outside-link/evil.md` when `outside-link` is a symlink to a directory outside the identity root, and confirms the outside file is not created.

## Verification tails

### `make build-bin`

Exit code: 0.

```text
CGO_ENABLED=1 go build -tags "fts5" -ldflags "-s -w -X main.version=63166d2-dirty -X main.commit=63166d2 -X main.date=2026-06-12T09:33:47Z" -o bin/daimon ./cmd/daimon
go: writing stat cache: open /Users/wuqisen/go/pkg/mod/cache/download/github.com/!forest-!isle/daimon/@v/v0.0.0-20260611032032-63166d210fa4.info787541420.tmp: operation not permitted
```

The stat-cache warning is from Go trying to update the global module cache outside the writable sandbox; the command still passed.

### `make vet`

Exit code: 0.

```text
go vet ./...
```

### `make test-short`

Exit code: 0.

```text
=== RUN   TestCommitmentsDigestFormattingAndCap
--- PASS: TestCommitmentsDigestFormattingAndCap (0.02s)
=== RUN   TestIdentityDigestMissingFile
--- PASS: TestIdentityDigestMissingFile (0.00s)
PASS
ok  	github.com/Forest-Isle/daimon/internal/world	0.095s
```

The verbose output also included the new `internal/tool` tests:

```text
--- PASS: TestWorldEditToolWriteAppendAndFence (0.01s)
    --- PASS: TestWorldEditToolWriteAppendAndFence/missing_file (0.00s)
    --- PASS: TestWorldEditToolWriteAppendAndFence/parent_traversal (0.00s)
    --- PASS: TestWorldEditToolWriteAppendAndFence/absolute_path (0.00s)
    --- PASS: TestWorldEditToolWriteAppendAndFence/cleaned_traversal (0.00s)
--- PASS: TestWorldEditToolRejectsSymlinkEscape (0.01s)
--- PASS: TestWorldToolCapabilities (0.01s)
PASS
ok  	github.com/Forest-Isle/daimon/internal/tool	1.781s
```

### `CGO_ENABLED=1 go test -tags fts5 ./internal/tool/ ./internal/world/ ./internal/gateway/`

Executed with `GOCACHE=$PWD/.gocache`.

Exit code: 0.

```text
ok  	github.com/Forest-Isle/daimon/internal/tool	(cached)
ok  	github.com/Forest-Isle/daimon/internal/world	(cached)
ok  	github.com/Forest-Isle/daimon/internal/gateway	(cached)
```

## Deviations and reasons

- No retrieval facade or embedding indexing was added, per the task's deliberate deviation.
- No episode package, Composer, action-layer/hold/trust mechanics, `taskruntime`, `memory`, `session`, or agent loop files were touched.
- `commitment create` generates the commitment ID before calling `Store.CreateCommitment` so the model receives the ID it needs for later updates.
- No git mutations were performed.

## Cross-Family Review (Claude → Codex)

### Path fence adversarial check

Read the full `resolveIdentityPath` (397–422), `ensureWorldEditParent` (424–463), `rejectExistingSymlinkEscape` (466–501), and `pathWithinRoot` (503–511) — all in `internal/tool/world.go`.

Five layers of protection, each addressing a different escape vector:

1. **Reject absolute paths** (line 402) — `/etc/x` caught before any filepath resolution.
2. **Static prefix check** (lines 409–410) — `..` and `../` prefix after `filepath.Clean` (catches `a/../../x`).
3. **Resolved relative containment** (lines 413–420) — `filepath.Join(root, clean)` then `pathWithinRoot` (catches any remaining escape not caught by prefix check).
4. **Per-component symlink walk** (`rejectExistingSymlinkEscape`) — finds symlinks in intermediate dirs that point outside the root, even if the symlink is several levels deep. Exits early when a component doesn't exist (correct — it hasn't been created yet).
5. **Post-create EvalSymlinks** (`ensureWorldEditParent`) — re-checks parent dir and target after `os.MkdirAll` (catches symlinks created between check and write, though TOCTOU cannot be fully eliminated at the filesystem layer).

Fence test coverage matches: `parent traversal` (`../x`), `absolute path` (`/etc/x`), `cleaned traversal` (`a/../../x`), `symlink escape` (dir symlink to outside + confirm file not written outside). All present in `world_test.go` and verified passing.

### Other quality checks

- `world_read` correctly reads identity digest / commitments digest / journal; defaults to `commitments` when section unspecified — good UX for the model.
- `commitment` update uses `raw` JSON-RawMessage map to detect which fields were explicitly set by the model vs absent due to empty-string zero values — correct approach, avoids clearing unset fields.
- Gateway wiring constructs `world.Identity{Dir: <appdir>/world/identity}` at init with `EnsureDir`, passes existing SQLite handle.
- No new dependencies introduced.
- **Acknowledged compliance with intentional spec deviation**: no retrieval facade built yet (deferred to sleep phase).

**Verdict: ACCEPTED.**
