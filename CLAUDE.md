# IronClaw Maintainer Notes

This file is a compact handoff for coding assistants working in this repository.

## Project Shape

- Primary language: Go 1.25.11.
- Main binary: `cmd/daimon`.
- Runtime composition root: `internal/gateway`.
- Database: SQLite with embedded migrations in `internal/store/migrations`.

## Important Current Facts

- Feature Registry defaults these core systems on: memory, skills, multi-agent.
- Standalone admin server is opt-in.
- The agent has a single execution strategy (`LinearLoop`); there is no user-selectable `agent.mode` config or `/mode` command.
- Sub-agents run in-process only.
- Tools execute directly on the host. Agent file tools are fenced to the process working directory, but there is still no OS sandbox/Docker isolation or network policy. The tool interceptor chain lives on `gateway.toolSub.InterceptorChain` (permission → hook → user-hook → verify → audit) and is built in `internal/gateway/subsystem_tool.go`.
- No telemetry: there is no OpenTelemetry/metrics instrumentation in the agent or tool paths.
- Channels are TUI (default) and Telegram.

## Safe Verification Commands

```bash
make build-bin
make vet
make test-short
```

Broader checks:

```bash
make test
```

`make test` uses CGO, the `fts5` tag, and the race detector.

## Editing Guidance

- Keep Gateway wiring explicit and local to the relevant `init_*.go` file.
- Use `apply_patch` for manual file edits.
- Do not revert unrelated user changes.
- When adding config, update `configs/daimon.example.yaml`.
- When adding a feature, update `internal/gateway/subsystem_feature.go` and `internal/feature`.
- When adding a tool, define its schema, approval behavior, capabilities, and tests.
