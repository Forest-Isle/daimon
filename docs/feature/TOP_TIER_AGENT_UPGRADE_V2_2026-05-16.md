# Top-Tier Agent Upgrade v2 — 5 New Capabilities (2026-05-16)

## Summary

This upgrade adds 5 production-grade capabilities to IronClaw, building on the 7 capabilities from v1 (2026-05-15). The focus is on **precision editing**, **code intelligence**, **test automation**, and **verification** — closing the gap with Claude Code, Devin, and Cursor.

## New Capabilities

### 1. Surgical Diff Editing (`file_patch`)

A new tool that applies unified diff patches to files with verification and dry-run support.

**Tool**: `file_patch`
- Input: `{path, patch (unified diff), dry_run (optional)}`
- Parses unified diff hunks (`@@ -old +new @@` headers)
- Applies patches with ±1 line offset tolerance for robustness
- Dry-run mode previews changes without writing
- Post-application `git diff --stat` verification
- Handles: multi-hunk patches, append-at-end, empty files

**Why this matters**: Claude Code's core advantage is surgical text editing with exact string matching. IronClaw now has the same capability.

**Files**: `internal/tool/file_patch.go`, `internal/tool/file_patch_test.go`

### 2. Post-Edit Verification Interceptor

An interceptor that automatically verifies every file modification to prevent silent corruption.

**Interceptor**: `VerifyInterceptor`
- Fires after `file_write`, `file_edit`, `file_patch`, and write-capable `bash` commands
- Re-reads the modified file (up to 1MB) to confirm it's readable
- Runs `git diff --stat` (with fallbacks: `--cached --stat`, `status --porcelain`) to capture changes
- Appends verification metadata (`diff_summary`, `file_readable`, `file_size_bytes`) to tool results
- Never fails the tool — verification is advisory only
- Gated by `tools.verify.enabled` (default: true)

**Why this matters**: Top agents verify their work. Without verification, silent write failures or partial edits go undetected.

**Files**: `internal/tool/interceptor_verify.go`, `internal/tool/interceptor_verify_test.go`

### 3. Code Intelligence Tools

Three read-only tools that give agents programmatic codebase understanding — replacing ad-hoc `bash grep` commands.

**Tool: `grep_code`**
- Fast regex pattern search with file-type filtering (`--include`)
- Returns `file:line:content` format with match counts
- Configurable max results cap

**Tool: `find_symbol`**
- Language-aware symbol search: functions, types, variables
- Go: `func (receiver)? name(` / `type name `
- Python: `def name(` / `class name`
- JS/TS: `function name` / `const name.*=` / `class name`
- Auto-detects symbol kind from matched lines

**Tool: `list_imports`**
- Extract imports from Go, Python, JS/TS source files
- Go: single `import "..."` and block `import (...)` parsing
- Python: `import X` and `from X import Y`
- JS/TS: `import ... from "..."` and `require(...)`
- Returns structured JSON with line numbers and modules

**Why this matters**: Cursor and Devin understand code structure programmatically. Blind grep is error-prone. These tools provide semantic-level code navigation.

**Files**: `internal/tool/code_intel.go`, `internal/tool/code_intel_test.go`

### 4. Test Runner with Failure Parsing (`test_run`)

A tool that runs test commands and returns structured failure output for auto-fixing loops.

**Tool**: `test_run`
- Auto-detects test command: `go.mod` → `go test ./...`, `package.json` → `npm test`, etc.
- Parses Go test output for structured failures (test name, file:line, message)
- Generic fallback parsing for non-Go test frameworks
- Returns: pass/fail counts, failure list with file locations, exit code, duration
- Configurable timeout (default 120s) and output truncation
- Handles: timeouts, command-not-found, empty output

**Why this matters**: Test→fail→fix→retest is the core agent workflow for reliable code generation. Devin and Claude Code both do this. IronClaw now has the infrastructure.

**Files**: `internal/tool/test_run.go`, `internal/tool/test_run_test.go`

### 5. Enhanced Project Scanner with Dependency Graph

The project scanner now parses Go module dependencies from `go.mod`, providing agents with a complete dependency graph of the project.

**Enhancement**:
- Parses direct and indirect Go dependencies from `go.mod`
- Classifies dependencies as direct vs indirect
- Injects dependency list into project context for prompt injection
- New `ProjectDependency` type in cognitive types

**Why this matters**: Understanding the dependency graph helps agents make better decisions about which libraries to use, which versions are available, and whether a feature requires new dependencies.

**Files**: `internal/agent/project_scanner.go` (modified), `internal/agent/cognitive_types.go` (modified)

## Configuration

### New config fields in `configs/ironclaw.yaml`:

```yaml
tools:
  verify:
    enabled: true  # Post-edit verification (default: true)
```

All new tools are auto-registered under existing feature gates:
- `grep_code`, `find_symbol`, `list_imports`: gated by `tools.file.enabled`
- `file_patch`: gated by `tools.file.enabled`
- `test_run`: gated by `tools.bash.enabled`
- `VerifyInterceptor`: gated by `tools.verify.enabled`

## Interceptor Chain (updated)

```
permission → hook → user_hook → sandbox → [verify] → audit
```

The verify interceptor sits after sandbox (write operations are already allowed) and before audit (verification metadata is logged).

## Files Changed

### New files (8):
- `internal/tool/file_patch.go` — Surgical diff editing tool
- `internal/tool/file_patch_test.go` — Tests for diff editing
- `internal/tool/code_intel.go` — Code intelligence tools (grep_code, find_symbol, list_imports)
- `internal/tool/code_intel_test.go` — Tests for code intelligence
- `internal/tool/test_run.go` — Test runner with failure parsing
- `internal/tool/test_run_test.go` — Tests for test runner
- `internal/tool/interceptor_verify.go` — Post-edit verification interceptor
- `internal/tool/interceptor_verify_test.go` — Tests for verification

### Modified files (6):
- `internal/gateway/init_tools.go` — Register all 5 new tools + verify interceptor in chain
- `internal/gateway/headless.go` — Register tools in headless path
- `internal/config/config.go` — Add VerifyConfig with default
- `internal/agent/project_scanner.go` — Go module dependency parsing
- `internal/agent/cognitive_types.go` — Add ProjectDependency type
- `internal/tool/interceptor_sandbox.go` — Add file_patch to write-tool detection

### Test verification:
- `internal/tool/interceptor_sandbox_test.go` — Updated for file_patch
- `internal/tool/interceptor_integration_test.go` — Updated for new tools
- `internal/tool/tool_test.go` — Updated for new tools

## Architecture Decisions

1. **Diff parsing in Go, not exec**: The unified diff parser is implemented in pure Go rather than shelling out to `patch(1)`. This gives us fine-grained error reporting, ±1 line offset tolerance, and cross-platform compatibility.

2. **Verification is advisory**: The VerifyInterceptor never fails a tool call. Verification metadata is appended but errors are logged as warnings. This prevents false-positive failures from breaking agent workflows.

3. **Code intelligence uses grep, not LSP**: LSP integration would require language-specific server management (gopls, pyright, etc.). Grep-based pattern matching is universal, fast, and works across all languages. LSP integration is a future enhancement.

4. **Test runner parses Go output natively**: The test_run tool has first-class Go test output parsing (FAIL lines, file locations, assertion messages) with generic fallback for other frameworks. This matches how Devin and Claude Code handle test output.

## Comparison with Top-Tier Agents (Post-Upgrade)

| Capability | v1 (May 15) | v2 (May 16) | Claude Code | Devin | Cursor |
|---|---|---|---|---|---|
| Worktree isolation | ✅ | ✅ | ✅ | ✅ | ❌ |
| Plan→approve→execute | ✅ | ✅ | ✅ | ✅ | ✅ |
| Structured output | ✅ | ✅ | ✅ | ✅ | ✅ |
| Streaming tool outputs | ✅ | ✅ | ✅ | ✅ | ✅ |
| User hook system | ✅ | ✅ | ✅ | ❌ | ❌ |
| A2A protocol | ✅ | ✅ | ❌ | ✅ | ❌ |
| **Surgical diff editing** | ❌ | ✅ | ✅ | ✅ | ✅ |
| **Post-edit verification** | ❌ | ✅ | ✅ | ✅ | ✅ |
| **Code intelligence tools** | ❌ | ✅ | ✅ | ✅ | ✅ |
| **Test runner w/ parsing** | ❌ | ✅ | ✅ | ✅ | ❌ |
| **Dependency graph** | ❌ | ✅ | ✅ | ✅ | ✅ |
| Parallel tool execution | ✅ | ✅ | ✅ | ✅ | ✅ |
| Sub-agent spawning | ✅ | ✅ | ✅ | ✅ | ✅ |
| Team coordination | ✅ | ✅ | ❌ | ✅ | ❌ |
| Evolution engine | ✅ | ✅ | ❌ | ❌ | ❌ |
| MCP integration | ✅ | ✅ | ✅ | ✅ | ✅ |
| Web dashboard | ✅ | ✅ | ❌ | ✅ | ❌ |
| LSP integration | ❌ | ❌ | ❌ | ✅ | ✅ |

**Gap remaining**: LSP integration for go-to-definition and diagnostics. This requires language server management infrastructure and is planned for v3.

## Future Work (v3)

- LSP integration (gopls, pyright, typescript-language-server)
- A2A streaming/push notifications
- PDF ingestion (unstub pdf.go)
- Skill safety sandbox validation
- Dashboard search/filter and reasoning visualization
- Auto-generated SKILL.md from successful agent trajectories
