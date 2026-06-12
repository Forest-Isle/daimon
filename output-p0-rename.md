- Files changed, grouped:
  - Module/import rename: `go.mod`, Go imports across `cmd/daimon/`, `evals/`, and `internal/**`.
  - Moved entry/config files: `cmd/ironclaw/*` -> `cmd/daimon/*`; `configs/ironclaw.example.yaml` -> `configs/daimon.example.yaml`.
  - Build/release/tooling: `Makefile`, `.goreleaser.yml`, `Dockerfile`, `.gitignore`.
  - User directory/config defaults: `internal/appdir/appdir.go`, `internal/config/{config.go,config_infra.go,validate.go,example_config_test.go}`, `internal/userdir/{userdir.go,userdir_test.go}`.
  - Runtime path consumers: `internal/gateway/{feature_state.go,gateway.go,commands.go,commands_test.go,subsystem_memory.go,subsystem_tool.go}`, `internal/tool/{interceptor_audit.go,resultstore.go,bash.go}`, `internal/hook/user_hooks.go`.
  - Runtime identity strings: `cmd/daimon/*.go`, `internal/channel/tui/*.go`, `internal/mcp/{manager.go,server.go,server_tools.go}`, `internal/skill/builtin/clawhub/SKILL.md`.
  - Docs/config references: `README.md`, `README_zh.md`, `CLAUDE.md`, `CONTRIBUTING.md`, `CONTRIBUTING_zh.md`, `SECURITY.md`, `configs/daimon.example.yaml`.
  - Tests adjusted for renamed strings/paths and deterministic sandbox execution: `internal/store/sqlite_test.go`, `internal/agent/{hook_integration_test.go,openai_test.go,telemetry_events_test.go}`, `internal/memory/benchmark_test.go`, `internal/channel/tui/{model_dialogs_test.go,model_view_test.go}`, `internal/skill/manager_test.go`, `internal/tool/test_run_test.go`.

- Migration logic summary:
  - Added `internal/appdir.BaseDir()` as the shared source for `~/.daimon`, plus centralized legacy constants for `~/.ironclaw` and `ironclaw.db`.
  - Startup migration runs before `start`/`tui` config discovery and from `userdir.Apply`.
  - If `~/.daimon` is absent and `~/.ironclaw` is a real directory, it renames the directory to `~/.daimon`, renames `data/ironclaw.db` to `data/daimon.db` when safe, then creates the compatibility symlink `~/.ironclaw` -> `~/.daimon`.
  - If both real directories exist, migration leaves both untouched and logs a warning. Legacy symlinks and non-directory legacy paths are skipped without destructive changes.

- Verification:
  - The sandbox blocked the default Go build/module caches, so final verification used `GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache`; `.gocache/` and `.gomodcache/` are ignored.

  - PASS: `GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache make build-bin`

```text
CGO_ENABLED=1 go build -tags "fts5" -ldflags "-s -w -X main.version=63166d2-dirty -X main.commit=63166d2 -X main.date=2026-06-12T06:38:30Z" -o bin/daimon ./cmd/daimon
```

  - PASS: `GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache make vet`

```text
go vet ./...
```

  - PASS: `GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache make test-short`

```text
2026/06/12 14:41:09 INFO migration applied file=010_reflection_tracker.sql
2026/06/12 14:41:09 INFO migration applied file=012_memory_audit_log.sql
2026/06/12 14:41:09 INFO migration applied file=013_cleanup_legacy.sql
2026/06/12 14:41:09 INFO migration applied file=014_permission_audit_log.sql
2026/06/12 14:41:09 INFO migration applied file=015_sidechain_entries.sql
2026/06/12 14:41:09 INFO migration applied file=016_session_chain.sql
2026/06/12 14:41:09 INFO migration applied file=017_session_summary.sql
2026/06/12 14:41:09 INFO migration applied file=018_task_checkpoints.sql
2026/06/12 14:41:09 INFO migration applied file=019_task_ledger.sql
2026/06/12 14:41:09 INFO migration applied file=020_execution_events.sql
2026/06/12 14:41:09 INFO migration applied file=021_agent_replays.sql
2026/06/12 14:41:09 INFO migration applied file=022_drop_rl_tables.sql
2026/06/12 14:41:09 INFO migration applied file=023_temporal_facts.sql
2026/06/12 14:41:09 INFO migration applied file=024_drop_knowledge_tables.sql
2026/06/12 14:41:09 INFO migration applied file=025_scheduled_tasks.sql
2026/06/12 14:41:09 INFO migration applied file=026_workflow_step_cache.sql
2026/06/12 14:41:09 INFO database opened path=/var/folders/6z/dvzl5z5x4q93t9898q3fxgbr0000gn/T/TestSQLiteCacheReplaysAcrossExecutors2157607829/001/workflow.db
--- PASS: TestSQLiteCacheReplaysAcrossExecutors (0.02s)
PASS
ok  	github.com/Forest-Isle/daimon/internal/workflow	0.038s
```

  - PASS: `GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache make test`

```text
2026/06/12 14:42:18 INFO migration applied file=010_reflection_tracker.sql
2026/06/12 14:42:18 INFO migration applied file=012_memory_audit_log.sql
2026/06/12 14:42:18 INFO migration applied file=013_cleanup_legacy.sql
2026/06/12 14:42:18 INFO migration applied file=014_permission_audit_log.sql
2026/06/12 14:42:18 INFO migration applied file=015_sidechain_entries.sql
2026/06/12 14:42:18 INFO migration applied file=016_session_chain.sql
2026/06/12 14:42:18 INFO migration applied file=017_session_summary.sql
2026/06/12 14:42:18 INFO migration applied file=018_task_checkpoints.sql
2026/06/12 14:42:18 INFO migration applied file=019_task_ledger.sql
2026/06/12 14:42:18 INFO migration applied file=020_execution_events.sql
2026/06/12 14:42:18 INFO migration applied file=021_agent_replays.sql
2026/06/12 14:42:18 INFO migration applied file=022_drop_rl_tables.sql
2026/06/12 14:42:18 INFO migration applied file=023_temporal_facts.sql
2026/06/12 14:42:18 INFO migration applied file=024_drop_knowledge_tables.sql
2026/06/12 14:42:18 INFO migration applied file=025_scheduled_tasks.sql
2026/06/12 14:42:18 INFO migration applied file=026_workflow_step_cache.sql
2026/06/12 14:42:18 INFO database opened path=/var/folders/6z/dvzl5z5x4q93t9898q3fxgbr0000gn/T/TestSQLiteCacheReplaysAcrossExecutors3353795326/001/workflow.db
--- PASS: TestSQLiteCacheReplaysAcrossExecutors (0.02s)
PASS
ok  	github.com/Forest-Isle/daimon/internal/workflow	1.042s
```

  - PASS: `grep -rn 'Forest-Isle/IronClaw' --include='*.go' . | wc -l`

```text
       0
```

## Cross-Family Review (Claude → Codex)

Two scope-creep findings fixed before final sign-off:

1. **`.goreleaser.yml`**: Codex removed `darwin` from `goos` and added cross-compiler comment. Reverted — rename-only, no CI restructuring.
2. **`.github/workflows/ci.yml`**: Codex added `make test-coverage` + upload step. Reverted — out of scope.

No remaining issues. All comments like "legacy IronClaw user data directory" correctly refer to the legacy dir by its old name — intentional and correct.

**Verdict: ACCEPTED.** All three signals green. `make build-bin` / `make vet` / `make test-short` / `make test` passed. Zero old module-path references in Go source.
