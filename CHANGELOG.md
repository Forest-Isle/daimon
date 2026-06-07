# Changelog

All notable changes to IronClaw are tracked here.

## Unreleased

### Removed

- Removed the web dashboard subsystem entirely: the `internal/dashboard` package (HTTP/WebSocket server, event bus, agent state tracker, evolution bridge, embedded Preact frontend), the Preact app under `web/`, the `dashboard` Feature Registry entry, and the `dashboard:` config block (`DashboardConfig`). The standalone Vue Studio prototype (`web/studio/`), the standalone HTTP admin server, the always-on health server, OpenTelemetry observability, and cognitive metrics are unaffected.
- Removed the Prometheus `/metrics` endpoint, which was served only by the dashboard server. No custom collectors were registered on it.
- Removed the Makefile `web` frontend build target; `make build` no longer builds an embedded frontend.
- Removed the TUI dashboard-URL header display (`SetDashboardURL`).

### Fixed

- Fixed Knowledge Base embedding initialization so it honors `memory.embedding_base_url`. Memory, Codebase Index, and Knowledge Base now use the same OpenAI-compatible embedding endpoint configuration path.

### Changed

- The agent observability emitter is now owned directly by the Gateway (`gw.emitter`) instead of the removed dashboard subsystem; it degrades to a no-op discard emitter when no consumer (e.g. the TUI status bar) is attached.
- Deleted the stale documentation set under `docs/` and rewrote the project documentation from current source.
- Replaced the root README, Chinese README, code health report, contribution guides, security guide, code of conduct, optimization roadmap, Claude handoff notes, and example README.
- Added a new numbered documentation tree covering architecture, Gateway lifecycle, CLI/config/userdir, Agent runtime, tools/security hooks, Memory/Knowledge/Graph, channels/observability, store/session/task ledger/scheduler, evolution, frontend apps, developer workflows, and package inventory.

### Verification

- `make build-bin`
- `make vet`
- `make test` (CGO, `fts5` tag, race detector — all packages pass)
- `npm ci && npm run build` in `web/studio/`

## Historical Notes

Previous documentation contained many historical feature plans and references to removed or renamed modules. Those files were intentionally replaced with a source-derived documentation set. OpenSpec archives and agent/skill workflow assets remain in place because they are operational or specification artifacts rather than public architecture documentation.
