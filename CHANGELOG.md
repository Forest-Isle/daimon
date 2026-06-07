# Changelog

All notable changes to IronClaw are tracked here.

## Unreleased

### Removed

- Removed the Knowledge Base and Knowledge Graph subsystems: deleted the `internal/knowledge` package (document ingestion, chunking, BM25+vector hybrid retrieval, reranker) and the `internal/knowledge/graph` package (entity/relation extraction, graph traversal, decay, sync).
- Removed the `knowledge`, `knowledge_graph`, and `reranker` features from the Feature Registry, the `knowledge:` and `graph:` config sections, and the `ironclaw_knowledge_query` MCP tool.
- The memory unified retriever now fuses only memory-store and procedural sources; `FusionWeights` drops `KnowledgeWeight` and `GraphWeight`.
- Dropped the `kb_*` and `kg_*` tables via migration `024_drop_knowledge_tables.sql`; removed migrations `004_knowledge_base.sql`, `005_knowledge_graph.sql`, and `011_temporal_graph.sql`.
- Removed the `knowledge` eval dimension and suite (11 dimensions remain).

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
