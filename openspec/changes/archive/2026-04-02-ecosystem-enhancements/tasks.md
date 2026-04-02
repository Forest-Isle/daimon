## 1. Context Injector Handlers

- [x] 1.1 Create `internal/hook/injector_git.go` implementing `OnUserMessageHandler` interface
- [x] 1.2 Implement `GitContextInjector`: run `git branch --show-current` and `git status --short` with context timeout
- [x] 1.3 Handle non-git directory gracefully (detect via `git rev-parse --git-dir`)
- [x] 1.4 Add configurable timeout (default 2000ms) and `include_diff_stat` option
- [x] 1.5 Create `internal/hook/injector_workdir.go` implementing `OnUserMessageHandler`
- [x] 1.6 Implement `WorkdirContextInjector`: current directory path + optional file listing (sorted by mtime, limited to `max_files`)
- [x] 1.7 Register both injectors in the handler factory from Phase 2 (types: `git_context`, `workdir_context`)
- [x] 1.8 Write unit tests for GitContextInjector (git repo, non-git dir, timeout scenarios)
- [x] 1.9 Write unit tests for WorkdirContextInjector (with/without file listing)

## 2. System Prompt Integration

- [x] 2.1 Update `runtime.go:buildSystemPrompt()` to accept and include injected context from OnUserMessage hooks
- [x] 2.2 Format injected context under a `## Environment Context` section in the system prompt
- [x] 2.3 Ensure cognitive agent's PERCEIVE phase benefits from injected context (verify via system prompt flow)
- [x] 2.4 Write integration test: git context appears in system prompt when configured

## 3. Permission Audit Logging

- [x] 3.1 Create migration `internal/store/migrations/007_permission_audit_log.sql` with table, indexes
- [x] 3.2 Add `InsertAuditLog` and `QueryAuditLogs` methods to `internal/store/db.go`
- [x] 3.3 Create `internal/hook/audit_logger.go` implementing `PostToolUseHandler`
- [x] 3.4 Implement `PermissionAuditHandler`: extract decision context from PostToolUseEvent, write to DB
- [x] 3.5 Implement input summary truncation (first 200 chars + "...")
- [x] 3.6 Register in handler factory (type: `audit_logger`)
- [x] 3.7 Pass permission decision metadata through the tool execution flow (from PreToolUse result → PostToolUse event)
- [x] 3.8 Write unit tests for audit handler (various action/reason combinations)
- [x] 3.9 Write test for input truncation edge cases

## 4. Configuration & Testing

- [x] 4.1 Add context injector and audit logger examples to `ironclaw.example.yaml` under `hooks` section
- [x] 4.2 Verify migration 007 applies cleanly alongside existing migrations (001-006)
- [x] 4.3 Run full test suite (`make test`) and fix regressions
- [x] 4.4 Run linter (`make lint`) and fix issues
