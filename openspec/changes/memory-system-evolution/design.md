## Context

IronClaw's memory system uses a file-first architecture: Markdown files with YAML frontmatter at `~/.ironclaw/memory/` are the source of truth, with SQLite providing auxiliary indexing (FTS5 + vector embeddings) for fast search. The current pipeline is: `factExtractor → lifecycle (ADD/UPDATE/DELETE/NOOP) → file_store → consolidator (session→user promotion) → forgetting_curve (archival)`.

The knowledge graph (`internal/knowledge/graph/`) stores entity-relation triples in SQLite (`kg_nodes`, `kg_edges`, `kg_provenance`) with recursive CTE traversal. Currently it operates independently from the memory system — both are read during the cognitive agent's PERCEIVE phase and written during REFLECT, but there is no bidirectional sync between them.

Key constraints:
- **Local-first**: No cloud dependencies. SQLite + local files only.
- **CGO required**: SQLite via `mattn/go-sqlite3` with FTS5 tag.
- **LLM budget**: Each LLM call costs latency (~500ms-2s). Minimize calls on hot paths.
- **Backward compatible**: Existing memory files must work without migration.

## Goals / Non-Goals

**Goals:**
- Improve memory retrieval precision through type-based classification and importance scoring
- Enable cross-memory pattern synthesis via automated reflection
- Reduce search noise as memory volume grows via hierarchical compression
- Unify memory and knowledge graph into a coherent read/write pipeline
- Give users control over what the agent remembers

**Non-Goals:**
- Multi-user access control or authentication (single-user local agent)
- Cloud-based memory synchronization or MaaS (Memory-as-a-Service)
- Multimodal memory (images, audio) — text only for now
- Real-time collaborative memory editing
- Custom embedding model support (OpenAI embeddings only, with noop fallback)

## Decisions

### Decision 1: Memory Type System — Frontmatter Extension with Defaults

**Choice**: Add `type`, `importance`, and `emotion` fields to YAML frontmatter. Existing files without these fields default to `type: semantic`, `importance: 1.0`, `emotion: neutral`.

**Alternatives considered**:
- *Separate type-specific directories* (e.g., `episodic/`, `semantic/`, `procedural/`): Rejected because it conflicts with the existing scope-based directory structure (`session/`, `user/`). Two orthogonal taxonomies in directory layout creates confusion.
- *SQL-only classification*: Rejected because it violates the file-first principle — the file must be self-describing.

**Rationale**: Frontmatter is already the metadata mechanism. Adding fields is zero-migration — the `ParseMemoryFile` function already handles arbitrary frontmatter fields via a map. Type inference happens at extraction time via the LLM prompt, so no retroactive classification is needed.

### Decision 2: Reflection Trigger — Hybrid Count + Drift

**Choice**: Trigger reflection when either (a) unreflected fact count reaches N=10, or (b) topic drift is detected (cosine similarity between current topic embedding and last reflection topic embedding drops below 0.7).

**Alternatives considered**:
- *Time-based only* (every 4h): Rejected — wastes LLM calls during idle periods, insufficient during active periods.
- *Topic drift only*: Rejected — requires embedding computation for every fact (already done by CachedEmbedder, so cost is low, but the risk is that within a single long topic, reflection never triggers).
- *Per-conversation reflection*: Rejected — too fine-grained, produces shallow reflections.

**Rationale**: Count threshold guarantees eventual reflection. Drift detection accelerates it at natural topic boundaries. The CachedEmbedder already caches embeddings by content SHA256, so drift detection adds ~0 LLM cost (only embedding lookup + cosine computation). The reflection LLM call itself only happens on trigger.

**Implementation**: Add a `ReflectionTracker` struct embedded in `LifecycleManager`. On each `Process()` call, increment counter and update running topic embedding (exponential moving average of fact embeddings). Check trigger conditions after counter update.

### Decision 3: Compression Pipeline — Layered On Top of Consolidator

**Choice**: Keep the existing `Consolidator` unchanged. Add a new `Compactor` that runs as a separate background task after consolidation, and a `Profiler` that builds on compaction output.

**Alternatives considered**:
- *Replace consolidator with unified pipeline*: Rejected — consolidator's file-move logic is simple and reliable. Mixing it with LLM-based summarization creates unnecessary coupling and regression risk.
- *Inline compression during lifecycle.Process()*: Rejected — adds latency to the hot path. Compression is a background optimization.

**Rationale**: Separation of concerns. Consolidator = file promotion (mechanical). Compactor = content synthesis (LLM-driven). Profiler = high-level abstraction (LLM-driven). Each runs independently on its own schedule.

**Pipeline**:
```
consolidator (24h) → compactor (triggered by fact count per category) → profiler (triggered by reflection count)
```

**Compactor trigger**: When facts with the same `category` in `user/` scope exceed K=8, merge the oldest 8 into one summary file. Summary gets `type: summary` and `source_facts: [id1..id8]` in frontmatter. Source facts are NOT deleted — their strength continues to decay naturally via the forgetting curve.

**Profiler trigger**: After every 5 Level-1 reflections, regenerate `user/profile_{user_id}.md`. This file has a fixed structure (identity, preferences, current focus) and is always injected into the system prompt.

### Decision 4: Graph Sync Strategy — Lifecycle Hook with Weaken-Not-Delete

**Choice**: Extend `lifecycle.go`'s `executeAdd`, `executeUpdate`, and `executeDelete` to synchronously trigger graph operations. On DELETE, weaken edge weights rather than removing edges.

**Alternatives considered**:
- *Async graph sync via event queue*: Rejected — adds complexity (need event system) for marginal latency benefit. Graph operations are fast SQLite writes.
- *Direct edge deletion on memory DELETE*: Rejected — an edge may be supported by multiple provenance sources. Deleting one source doesn't invalidate the relationship, only reduces confidence.
- *Separate graph sync background task*: Rejected — leads to consistency drift between memory and graph states.

**Rationale**: The `kg_provenance` table already tracks which sources support each edge. The weaken strategy uses this: when a memory is deleted, find edges with that memory's provenance, reduce weight proportionally to remaining provenance count. An edge with weight < 0.1 is eligible for cleanup by the new `GraphDecayTask`.

**Weight update formula**:
```
on DELETE memory_id:
  for each edge where provenance includes memory_id:
    remove provenance entry
    remaining = count(provenance entries for this edge)
    if remaining == 0:
      edge.weight = 0.1  (zombie — eligible for decay)
    else:
      edge.weight = original_weight * (remaining / (remaining + 1))
```

### Decision 5: Temporal Graph Edges — Column Addition with Default

**Choice**: Add `valid_from DATETIME` and `valid_to DATETIME NULL` columns to `kg_edges` table via new migration. `valid_to = NULL` means "currently valid".

**Alternatives considered**:
- *Separate history table*: Rejected — complicates queries that need current + historical data.
- *JSON properties field*: Already exists but not indexed. Temporal queries need indexed columns for performance.

**Rationale**: Simple column addition. Existing edges get `valid_from = created_at, valid_to = NULL`. New temporal queries use `WHERE valid_to IS NULL` for current state and `WHERE valid_from <= ? AND (valid_to IS NULL OR valid_to > ?)` for point-in-time queries. The recursive CTE traversal gains a `valid_to IS NULL` predicate for current-state traversal.

### Decision 6: Layered Search Strategy

**Choice**: Restructure the search path to: (1) always load user profile, (2) search summaries first, (3) fall back to raw facts, (4) apply graph expansion for reranking.

**Implementation in `buildSystemPrompt` (agent/runtime.go)**:
```
1. Load profile_{user_id}.md if exists → inject as "User Context" section
2. Search(query) with type filter: summaries first (type=summary)
3. If summary hits < desired count, backfill with raw facts (type!=summary)
4. Deduplicate: if a summary's source_facts overlap with raw fact hits, prefer summary
5. For top-3 results, extract entities → graph.Traverse(depth=2) → attach as context
```

### Decision 7: Privacy — Tool-Based User Control

**Choice**: Register a new `memory_manage` tool in the tool registry that accepts natural language commands and translates them to memory operations.

**Subcommands**:
- `forget <description>` — Search matching memories, confirm with user, then delete
- `list` — Show recent/relevant memories with scores
- `protect <description>` — Mark matching memories as `sensitivity: secret`
- `retention <type> <days>` — Set retention policy

**PII Detection**: Regex patterns for common PII (email, phone, SSN, credit card) applied during `factExtractor.Extract()`. Detected PII triggers automatic `sensitivity: private` classification. Optional LLM-based detection for non-pattern PII (e.g., home addresses mentioned in prose).

## Risks / Trade-offs

**[LLM cost increase]** → Reflection and compression add LLM calls. Mitigation: both are background tasks with configurable thresholds. Default settings add ~1 reflection call per 10 conversation turns and ~1 compression call per 8 accumulated same-category facts. Profiling runs even less frequently.

**[Reflection quality]** → LLM may produce shallow or incorrect reflections. Mitigation: reflections are stored as regular memories subject to the same lifecycle (can be updated/deleted). Bad reflections naturally decay via the forgetting curve. Level-2 reflections require 5 Level-1 inputs, providing natural quality filtering.

**[Graph consistency during crashes]** → Synchronous graph sync in lifecycle.Process() means a crash between memory write and graph write leaves inconsistent state. Mitigation: graph operations are "best effort" — missing graph entries degrade search quality but don't corrupt memory data. The GraphDecayTask can detect orphaned provenance entries during its sweep.

**[Search latency increase]** → Layered search (profile + summaries + facts + graph expansion) adds query stages. Mitigation: profile is a single file read (cached). Summary search reduces total facts to scan. Graph expansion only applies to top-3 results. Net effect should be neutral or positive for large memory stores.

**[Privacy feature scope creep]** → Full GDPR compliance is complex. Mitigation: Phase 5 focuses on user-facing controls and basic PII detection. Audit logging provides the foundation for compliance but doesn't implement full data subject access requests or cross-system deletion.

## Migration Plan

**Phase 1 (Type Taxonomy)**: No migration. Existing files default to `type: semantic, importance: 1.0, emotion: neutral`. New facts get classified by updated extraction prompt.

**Phase 2 (Reflection)**: No migration. New `ReflectionTracker` starts with zero state. Reflections begin accumulating from first new fact after deployment.

**Phase 3 (Compression)**: No migration. Compactor begins processing when existing fact categories exceed threshold. Profiler generates profile after first 5 reflections.

**Phase 4 (Temporal Graph)**: New SQLite migration adds columns with defaults: `ALTER TABLE kg_edges ADD COLUMN valid_from DATETIME DEFAULT (created_at)` and `ADD COLUMN valid_to DATETIME DEFAULT NULL`. All existing edges become "currently valid".

**Phase 5 (Privacy)**: New migration adds `memory_audit_log` table. Sensitivity field added to frontmatter — existing files default to `sensitivity: public`.

**Rollback**: Each phase is independently reversible. Removing new frontmatter fields is safe (code defaults handle missing fields). New SQLite columns can be dropped. New files (summaries, profiles, reflections) can be deleted without affecting base facts.

## Open Questions

1. **Reflection prompt tuning**: What instructions produce the best Level-1 pattern synthesis? Needs experimentation with different prompt strategies (structured JSON output vs. free-form markdown).
2. **Compaction threshold**: Is K=8 the right threshold for merging facts into summaries? May need to be category-dependent (preferences might merge at K=5, facts at K=10).
3. **Graph expansion depth**: Should graph-boosted reranking use depth=1 (direct neighbors only) or depth=2 (2-hop)? Depth=2 may introduce noise for densely connected graphs.
4. **PII detection accuracy**: Regex-based PII detection will miss some patterns and may false-positive on technical content (e.g., UUIDs that match phone number patterns). What's the acceptable false-positive rate before enabling auto-classification?
