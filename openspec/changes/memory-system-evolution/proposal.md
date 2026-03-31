## Why

IronClaw's memory system has a solid file-first foundation with hybrid search, forgetting curve, and lifecycle management. However, compared to state-of-the-art agent memory systems (Mem0, Graphiti/Zep, EverMemOS), it lacks three critical capabilities: (1) memories are undifferentiated flat facts with no type taxonomy or importance scoring, limiting retrieval precision; (2) there is no cross-memory reflection — the system records individual facts but never synthesizes patterns or builds user profiles; (3) the knowledge graph and memory system operate independently with no bidirectional sync, and the graph lacks temporal reasoning. These gaps become increasingly impactful as memory volume grows and users expect personalized, context-aware interactions.

## What Changes

- **Memory Type Taxonomy**: Classify memories as `episodic`, `semantic`, or `procedural` with per-type decay rates in the forgetting curve. Add `importance` (1-10) and `emotion` scoring during fact extraction.
- **Reflection System**: New background process that synthesizes patterns across accumulated facts using a hybrid trigger (count-based N=10 + topic drift detection via embedding similarity). Produces multi-level reflections (L0 raw facts → L1 patterns → L2 strategic insights).
- **Hierarchical Compression**: New `compactor` merges same-category facts into structured summaries when count exceeds threshold. New `profiler` generates/updates a persistent user profile from reflections. Search becomes profile-first, summary-second, facts-third.
- **Temporal Knowledge Graph**: Add `valid_from`/`valid_to` timestamps to graph edges. Sync memory lifecycle operations (ADD/UPDATE/DELETE) with graph entity extraction and edge weight management. Add graph decay maintenance task.
- **Memory Privacy Controls**: User-facing `memory_manage` tool for selective forgetting, PII detection (regex + LLM), sensitivity classification, configurable retention policies, and access audit logging.

## Capabilities

### New Capabilities
- `memory-type-taxonomy`: Episodic/semantic/procedural classification with importance and emotion scoring, per-type forgetting curve parameters
- `memory-reflection`: Hybrid-triggered cross-memory pattern synthesis producing multi-level reflection memories
- `memory-compression`: Hierarchical fact-to-summary-to-profile compression pipeline with layered retrieval strategy
- `temporal-knowledge-graph`: Time-aware graph edges, relationship versioning, memory-graph write/read sync, graph decay maintenance
- `memory-privacy`: User-facing memory management tool, PII detection, sensitivity classification, retention policies, audit logging

### Modified Capabilities
- `forgetting-curve-integration`: Decay parameters become type-dependent (episodic decays fast, procedural strengthens with use); importance scoring feeds directly into stability computation
- `memory-lifecycle`: Write path extended to sync with knowledge graph (ADD→extract entities, UPDATE→update provenance, DELETE→weaken edges); reflection trigger check added after each Process() call
- `memory-search`: Retrieval strategy changes to layered approach — always load user profile, prefer summaries over raw facts, graph-boosted reranking for connected entities
- `memory-consolidation`: Consolidator preserved as-is; compaction pipeline built on top as a separate stage that runs after promotion

## Impact

**Code changes by package:**
- `internal/memory/` — Modified: `facts.go` (type+importance+emotion extraction), `lifecycle.go` (graph sync + reflection trigger), `forgetting_curve.go` (per-type parameters), `file_store.go` (type field filtering), `embeddings_db.go` (layered search). New: `reflector.go`, `compactor.go`, `profiler.go`, `privacy.go`
- `internal/knowledge/graph/` — Modified: `sqlite_graph.go` (temporal edge fields), `query.go` (time-aware queries). New: `graph_decay.go`, migration for `valid_from`/`valid_to` columns
- `internal/agent/` — Modified: `perceive.go` (graph-expanded retrieval), `runtime.go` (profile injection into system prompt)
- `internal/tool/` — New: `memory_manage.go` (user-facing memory control tool)
- `internal/store/migrations/` — New migration(s) for graph temporal fields and memory audit log table
- `internal/gateway/gateway.go` — Extended wiring for reflector, compactor, profiler, privacy components

**Backward compatibility:** All changes are additive. Existing memory files without `type`/`importance`/`emotion` fields default to `semantic`/`1.0`/`neutral`. No migration required for existing memories. Graph edges without temporal fields default to `valid_from=created_at, valid_to=NULL` (currently valid).

**Dependencies:** No new external dependencies. All features use existing LLM provider (completerAdapter), existing CachedEmbedder, and existing SQLite infrastructure.
