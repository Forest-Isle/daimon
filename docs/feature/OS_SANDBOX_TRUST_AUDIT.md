# OS-Level Sandbox, Progressive Trust & Audit Logging

## Overview

Adds kernel-enforced sandboxing (macOS Seatbelt, Linux Bubblewrap), a progressive trust model that reduces permission prompts over time, and comprehensive audit logging for all tool executions. Together these form the foundation of a defense-in-depth security stack.

## Problem

The previous security model had three gaps:

1. **Docker-only sandboxing** — Requires Docker runtime; no lightweight alternative for development machines
2. **Static permissions** — 4-level permission (none/notify/approve/deny) never adapts to user behavior
3. **No audit trail** — Tool execution decisions were not logged; no way to review what happened

## Architecture

### Unified Sandbox Interface

```go
// internal/sandbox/sandbox.go

type Sandbox interface {
    Exec(ctx context.Context, command string, workDir string, opts ExecOptions) (*ExecResult, error)
    Available() bool
    Name() string
}
```

**Auto-selection** via `NewAutoSandbox(cfg Config) Sandbox`:

```
darwin  → Seatbelt (sandbox-exec)  ← kernel-enforced, zero dependencies
linux   → Bubblewrap (bwrap)       ← user-namespace isolation
fallback → Docker                  ← container-based (requires Docker)
nil     → no sandbox available     ← FileGuard/NetworkPolicy still active
```

### macOS Seatbelt (`internal/sandbox/seatbelt.go`)

Uses `/usr/bin/sandbox-exec` with dynamically generated SBPL (Sandbox Profile Language) profiles.

**Default policy** (deny-first):
```scheme
(version 1)
(deny default)
;; System libraries — read-only
(allow file-read* (subpath "/usr/lib") (subpath "/usr/share") (subpath "/Library")
                  (subpath "/System") (subpath "/usr/local") (subpath "/opt/homebrew"))
;; Work directory — read-write
(allow file-read* file-write* (subpath "{WORK_DIR}"))
;; Temp — read-write
(allow file-read* file-write* (subpath "/tmp") (subpath "/private/tmp"))
;; Process execution — restricted
(allow process-exec (literal "/bin/bash") (literal "/bin/sh") (literal "/usr/bin/env")
                    (subpath "/usr/bin") (subpath "/usr/local/bin") (subpath "/opt/homebrew/bin"))
(allow process-fork)
(allow sysctl-read)
(allow mach-lookup)
```

**Network modes**:
- `NetworkAllowed=true` → full network access
- `ProxyPort > 0` → only `localhost:{port}` allowed (for proxy-based filtering)
- Default → all network denied

**Key properties**:
- Kernel-enforced: all child processes inherit restrictions
- Zero runtime dependencies
- Profile generated per-invocation (supports dynamic workDir and paths)
- SBPL strings properly escaped to prevent injection

### Linux Bubblewrap (`internal/sandbox/bubblewrap.go`)

Uses `bwrap` with user namespace isolation.

**Default configuration**:
```bash
bwrap --ro-bind / /              # Root filesystem read-only
      --bind {WORK_DIR} {WORK_DIR}  # Work directory read-write
      --tmpfs /tmp               # Fresh temp for each invocation
      --dev /dev                 # Minimal device access
      --proc /proc               # Process info
      --unshare-net              # Network namespace isolation
      --die-with-parent          # Clean up on parent exit
      --chdir {WORK_DIR}
      -- /bin/bash -c "{CMD}"
```

**Key properties**:
- User namespace isolation (no root required)
- Network fully removed (`--unshare-net`), not just filtered
- `--die-with-parent` prevents orphaned sandbox processes
- Additional paths configurable via `ExecOptions.AllowedPaths` / `ReadOnlyPaths`

### Docker Sandbox Adapter

`DockerSandbox` wraps the existing `DockerSessionManager` to implement the new `Sandbox` interface, preserving backward compatibility.

### Progressive Trust Tracker (`internal/tool/trust_tracker.go`)

Tracks per-tool approval/rejection history and auto-promotes trust level:

```
TrustApproveAll  →  TrustNotify  →  TrustAutoApprove
                 15 consecutive      30 consecutive
                 approvals           approvals
                 (0 rejections)      (0 rejections)
```

**Reset on rejection**: A single rejection drops the tool back to `TrustApproveAll` and resets `ConsecutiveOK` to 0. This ensures trust is earned, not inherited.

**Session isolation**: `Reset()` clears all stats — designed to be called at session start. Trust does not persist across sessions.

**Integration**: `TrustLevelToPermission()` maps trust levels to existing `PermissionAction` enum:
- `TrustAutoApprove` → `PermissionNone`
- `TrustNotify` → `PermissionNotify`
- `TrustApproveAll` → `PermissionApprove`

### Audit Log Interceptor (`internal/tool/interceptor_audit.go`)

Implements `ToolInterceptor` — sits in the interceptor chain and logs every tool execution.

**Log format**: JSONL (one JSON object per line), daily rotation.

```json
{
  "timestamp": "2026-04-23T14:30:00Z",
  "session_id": "sess_abc123",
  "tool_name": "bash",
  "input_hash": "a1b2c3d4e5f6g7h8",
  "decision": "allowed",
  "result_ok": true,
  "duration_ms": 342
}
```

**Security design**:
- Input is SHA-256 hashed (first 8 bytes), never stored raw — prevents sensitive data leakage
- Log directory `~/.ironclaw/audit/` created with `0700` permissions
- Log files created with `0600` permissions
- Writes are async (`go a.writeEntry(entry)`) to avoid blocking tool execution
- File handles managed with daily rotation (`audit-YYYY-MM-DD.jsonl`)

**Decision values**: `"allowed"`, `"denied"`, `"error"`

## Files

| File | Lines | Description |
|---|---|---|
| `internal/sandbox/sandbox.go` | 117 | Unified interface + Config + AutoSandbox factory + DockerSandbox adapter |
| `internal/sandbox/seatbelt.go` | 149 | macOS sandbox-exec implementation with SBPL profile generation |
| `internal/sandbox/bubblewrap.go` | 121 | Linux bwrap implementation with namespace isolation |
| `internal/tool/trust_tracker.go` | 155 | Progressive trust model with 3-level promotion |
| `internal/tool/interceptor_audit.go` | 141 | JSONL audit logging interceptor with daily rotation |
| `internal/sandbox/sandbox_test.go` | 47 | Interface compliance and auto-selection tests |
| `internal/sandbox/seatbelt_test.go` | 104 | Profile generation, network modes, escaping tests |
| `internal/sandbox/bubblewrap_test.go` | 93 | Arg construction, network modes, availability tests |
| `internal/tool/trust_tracker_test.go` | 178 | Promotion, rejection reset, copy safety, level mapping |
| `internal/tool/interceptor_audit_test.go` | 167 | Directory creation, log writing, denied calls, hash consistency |

## Testing

```bash
go test ./internal/sandbox/...
go test ./internal/tool/...
```

## Configuration

### Sandbox Config (in `ironclaw.yaml`)

```yaml
sandbox:
  work_dir: "/path/to/project"
  allowed_dirs: ["/home/user/data"]
  readonly_dirs: ["/etc"]
  network_mode: "none"         # "none" | "proxy" | "full"
  memory_limit: "512m"         # Docker only
  cpu_limit: "1.0"             # Docker only
  image: "ubuntu:22.04"        # Docker only
  idle_timeout: "10m"          # Docker only
```

### Audit Config

Audit directory defaults to `~/.ironclaw/audit/`. Override by passing a custom `logDir` to `NewAuditInterceptor()`.

## Security Model Comparison

| Layer | Before | After |
|---|---|---|
| Tool filtering | None | L1: ToolPreFilter (planned) |
| User hooks | Supported | L2: PreToolUseHook (existing) |
| Deny evaluation | Basic 4-level | L3: DenyFirstEvaluator (planned) |
| Permission | Static 4-level | L4: PermissionHandler + TrustTracker |
| Sandbox | Docker only | L5: Seatbelt / Bubblewrap / Docker |
| Post hooks | Supported | L6: PostToolUseHook (existing) |
| Audit | None | L7: AuditInterceptor |
