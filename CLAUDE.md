# IronClaw Maintainer Notes

This file is a compact handoff for coding assistants working in this repository.

## Project Shape

- Primary language: Go 1.25.9.
- Main binary: `cmd/ironclaw`.
- Runtime composition root: `internal/gateway`.
- Database: SQLite with embedded migrations in `internal/store/migrations`.

## Important Current Facts

- Feature Registry defaults these core systems on: memory, skills, multi-agent.
- Standalone admin server is opt-in.
- `agent.mode` accepts `simple`, `unified`, and legacy `cognitive`; `cognitive` maps to UnifiedLoop behavior.
- Sub-agents run in-process only. `internal/agent/spec.go` contains future A2A remote-agent fields; remote A2A execution is not implemented.

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
- When adding config, update `configs/ironclaw.example.yaml`.
- When adding a feature, update `internal/gateway/features.go`.
- When adding a tool, define its schema, approval behavior, capabilities, and tests.
