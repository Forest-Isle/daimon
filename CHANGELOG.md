# Changelog

All notable changes to IronClaw are tracked here.

## Unreleased

### Fixed

- Fixed Knowledge Base embedding initialization so it honors `memory.embedding_base_url`. Memory, Codebase Index, and Knowledge Base now use the same OpenAI-compatible embedding endpoint configuration path.

### Changed

- Deleted the stale documentation set under `docs/` and rewrote the project documentation from current source.
- Replaced the root README, Chinese README, code health report, contribution guides, security guide, code of conduct, optimization roadmap, Claude handoff notes, and example README.
- Added a new numbered documentation tree covering architecture, Gateway lifecycle, CLI/config/userdir, Agent runtime, tools/security hooks, Memory/Knowledge/Graph, channels/dashboard/observability, store/session/task ledger/scheduler, evolution/eval/training, frontend apps, developer workflows, and package inventory.

### Verification

- `make build-bin`
- `make vet`
- `make test-short`
- `make test`
- `npm ci && npm run build` in `web/`
- `npm ci && npm run build` in `web/studio/`

## Historical Notes

Previous documentation contained many historical feature plans and references to removed or renamed modules. Those files were intentionally replaced with a source-derived documentation set. OpenSpec archives and agent/skill workflow assets remain in place because they are operational or specification artifacts rather than public architecture documentation.
