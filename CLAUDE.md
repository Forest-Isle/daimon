# IronClaw Maintainer Notes

This file is a compact handoff for coding assistants working in this repository.

## Project Shape

- Primary language: Go 1.25.9.
- Main binary: `cmd/ironclaw`.
- Runtime composition root: `internal/gateway`.
- Embedded dashboard: `web/` -> Vite/Preact -> `internal/dashboard/dist`.
- Standalone Studio prototype: `web/studio/` -> Vite/Vue.
- Database: SQLite with embedded migrations in `internal/store/migrations`.

## Important Current Facts

- Feature Registry defaults many core systems on: memory, skills, multi-agent, team, speculative, scheduler, knowledge, knowledge graph, reranker, worktree.
- Dashboard, standalone admin server, evolution, and model routing are opt-in.
- `agent.mode` accepts `simple`, `unified`, and legacy `cognitive`; `cognitive` maps to UnifiedLoop behavior.
- `internal/agent/spec.go` contains future A2A remote-agent fields. Local sub-agent backends are current; remote A2A execution is not.
- Vue Studio views contain prototype/demo state and should not be described as fully backend-persisted.
- Knowledge Base embedding now uses `memory.embedding_base_url`; keep future embedding call sites consistent.

## Safe Verification Commands

```bash
make build-bin
make vet
make test-short
```

Broader checks:

```bash
make test
cd web && npm ci && npm run build
cd web/studio && npm ci && npm run build
```

`make test` uses CGO, the `fts5` tag, and the race detector.

## Editing Guidance

- Read `docs/README.md` before changing architecture docs.
- Keep Gateway wiring explicit and local to the relevant `init_*.go` file.
- Use `apply_patch` for manual file edits.
- Do not revert unrelated user changes.
- Do not commit accidental generated files such as `web/studio/dist/`.
- When adding config, update `configs/ironclaw.example.yaml` and `docs/02-cli-config-userdir.md`.
- When adding a feature, update `internal/gateway/features.go` and `docs/03-gateway-feature-lifecycle.md`.
- When adding a tool, define schema, approval behavior, capabilities, tests, and docs in `docs/05-tools-permissions-sandbox-hooks.md`.
