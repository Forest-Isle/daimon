## 1. Phase 1: Memory Type Taxonomy & Importance Scoring

- [x] 1.1 Update `ExtractedFact` struct in `internal/memory/facts.go` to add `Type`, `Importance`, and `Emotion` fields
- [x] 1.2 Update the fact extraction LLM prompt in `facts.go` to output `type` (episodic|semantic|procedural), `importance` (1-10), and `emotion` (positive|negative|neutral) for each fact
- [x] 1.3 Update `parseFacts()` in `facts.go` to parse the new fields from LLM JSON output, with defaults: type=semantic, importance=1, emotion=neutral
- [x] 1.4 Update `MemoryFile` struct and `ParseMemoryFile`/`WriteMemoryFile` in `file_store.go` to read/write `type`, `importance`, and `emotion` frontmatter fields
- [x] 1.5 Update `lifecycle.go` `executeAdd` and `executeUpdate` to pass `type`, `importance`, and `emotion` from `ExtractedFact` into memory file metadata
- [x] 1.6 Modify `ComputeStrength` and `ComputeStrengthFromFile` in `forgetting_curve.go` to use type-dependent stability multipliers (episodic=12, semantic=24, procedural=48) and type-dependent access factors (procedural=1.2×)
- [x] 1.7 Add `type` and `emotion` columns to `memory_index` SQLite table via new migration file, with default values for existing rows
- [x] 1.8 Write tests for type-dependent strength computation: verify episodic decays faster than semantic, procedural strengthens more with access
- [x] 1.9 Write tests for updated fact extraction: verify LLM output parsing with all five fields, verify defaults for missing fields

## 2. Phase 2: Memory Reflection System

- [x] 2.1 Create `internal/memory/reflector.go` with `ReflectionTracker` struct holding: unreflected fact count, running topic embedding, last reflection topic embedding, L1 count since last L2
- [x] 2.2 Implement running topic embedding update as exponential moving average (α=0.3) using existing `CachedEmbedder` for fact embedding lookup
- [x] 2.3 Implement hybrid trigger check: count >= N (default 10) OR cosine similarity < threshold (default 0.7)
- [x] 2.4 Implement Level-1 reflection generation: collect unreflected facts, call LLM to synthesize patterns, save as memory file with `type: reflection`, `level: 1`, `source_facts: [...]`
- [x] 2.5 Implement Level-2 meta-reflection: trigger after 5 L1 reflections, synthesize across L1 outputs, save with `type: reflection`, `level: 2`, `source_reflections: [...]`
- [x] 2.6 Add reflection tracker state persistence to SQLite (new table or key-value in existing table) for cross-restart continuity
- [x] 2.7 Integrate reflection trigger check into `lifecycle.go` `Process()` method: after each fact processing, call `reflectionTracker.Check()` and trigger reflection if conditions met
- [x] 2.8 Wire `ReflectionTracker` into gateway initialization in `gateway.go`, injecting `CachedEmbedder` and `Completer`
- [x] 2.9 Add configuration fields to `MemoryConfig`: `reflection_count_threshold` (default 10), `reflection_drift_threshold` (default 0.7), `reflection_l2_trigger` (default 5)
- [x] 2.10 Write tests for trigger logic: count-based trigger, drift-based trigger, combined, state persistence across restart

## 3. Phase 3: Hierarchical Memory Compression

- [x] 3.1 Create `internal/memory/compactor.go` with `Compactor` struct: background task that scans user/ scope facts grouped by category
- [x] 3.2 Implement category counting: query `memory_index` for facts grouped by category in user/ scope, identify categories exceeding threshold K (default 8)
- [x] 3.3 Implement summary generation: collect oldest K facts in a category, call LLM to merge into structured summary, save as memory file with `type: summary`, `source_facts: [...]`, `category: <category>`
- [x] 3.4 Implement background compaction task with configurable interval (default 6h), following the same pattern as `consolidator.go` (ticker + done channel + ctx)
- [x] 3.5 Create `internal/memory/profiler.go` with `Profiler` struct: generates/updates `user/profile_{user_id}.md` from Level-1 reflections
- [x] 3.6 Implement profile generation: collect all L1/L2 reflections + existing profile, call LLM to produce structured profile (Identity, Preferences, Current Focus sections), save with `type: profile`, `user_id: <user_id>`
- [x] 3.7 Implement profile trigger: after every 5 new L1 reflections, regenerate profile (hook into reflection completion callback)
- [x] 3.8 Modify `buildSystemPrompt` in `internal/agent/runtime.go` to load and inject `user/profile_{user_id}.md` as "User Context" section
- [x] 3.9 Modify search in `embeddings_db.go` to support `type` filter parameter and implement layered retrieval: summaries first, backfill with raw facts, deduplicate against source_facts
- [x] 3.10 Wire `Compactor` and `Profiler` into gateway initialization in `gateway.go` with background task startup
- [ ] 3.11 Write tests for compaction: category threshold detection, summary generation, source fact preservation
- [ ] 3.12 Write tests for layered retrieval: profile injection, summary preference over raw facts, deduplication, backfill logic

## 4. Phase 4: Temporal Knowledge Graph & Memory-Graph Integration

- [x] 4.1 Create new SQLite migration adding `valid_from DATETIME` and `valid_to DATETIME` columns to `kg_edges` table, with defaults: `valid_from = created_at`, `valid_to = NULL`
- [x] 4.2 Update `Edge` struct in `internal/knowledge/graph/graph.go` to add `ValidFrom` and `ValidTo` fields
- [x] 4.3 Update `UpsertEdge` in `sqlite_graph.go` to set `valid_from` on creation and implement relationship versioning: when same (source, target, type) edge exists, set old edge's `valid_to` and create new edge
- [x] 4.4 Update `Neighbors` and `Traverse` queries in `sqlite_graph.go` to filter by `valid_to IS NULL` for current state (default), and accept optional `asOf` timestamp for historical queries
- [x] 4.5 Update recursive CTE in `Traverse` to include temporal predicate: `WHERE valid_from <= ? AND (valid_to IS NULL OR valid_to > ?)`
- [x] 4.6 Create `internal/knowledge/graph/graph_sync.go` with functions: `SyncOnAdd(factID, content)`, `SyncOnUpdate(oldID, newID, content)`, `SyncOnDelete(factID)` implementing the write-path graph sync
- [x] 4.7 Implement `SyncOnDelete`: find edges with provenance for factID, remove provenance entry, recalculate weight based on remaining provenance count, set weight=0.1 if no provenance remains
- [x] 4.8 Inject `GraphSync` into `LifecycleManager` and call sync functions from `executeAdd`, `executeUpdate`, `executeDelete`
- [x] 4.9 Create `internal/knowledge/graph/graph_decay.go` with background task: validate provenance entries, decay unsupported edge weights (×0.9), delete edges with weight < 0.1
- [ ] 4.10 Update `perceive.go` to implement graph-expanded retrieval: for top-3 memory results, extract entities, traverse graph for connected context, boost scores based on graph connectivity
- [x] 4.11 Wire `GraphSync` and `GraphDecayTask` into gateway initialization
- [ ] 4.12 Write tests for temporal edges: versioning, point-in-time queries, current-state filtering
- [ ] 4.13 Write tests for memory-graph sync: ADD creates provenance, DELETE weakens edges, orphan cleanup
- [ ] 4.14 Write tests for graph-boosted reranking: connected entities get score boost, no connection = no change

## 5. Phase 5: Memory Privacy & Selective Forgetting

- [x] 5.1 Add `sensitivity` field support to `MemoryFile` frontmatter parsing (public|private|secret, default: public)
- [x] 5.2 Add `sensitivity` column to `memory_index` SQLite table via new migration, default 'public'
- [x] 5.3 Create new migration for `memory_audit_log` table with columns: id, memory_id, action, actor, timestamp, details
- [x] 5.4 Create `internal/memory/privacy.go` with PII detection: regex patterns for email, phone, SSN, credit card number formats
- [x] 5.5 Integrate PII detection into `facts.go` extraction pipeline: after LLM extraction, scan each fact for PII patterns, auto-set `sensitivity: private` if detected
- [x] 5.6 Modify search queries in `embeddings_db.go` to exclude `sensitivity: secret` memories from all automated searches, and exclude `sensitivity: private` from non-user-scoped searches
- [x] 5.7 Create `internal/tool/memory_manage.go` implementing the Tool interface with subcommands: `forget`, `list`, `protect`, `retention`
- [x] 5.8 Implement `forget` subcommand: search matching memories, return candidates for user confirmation, delete confirmed memories with audit logging
- [x] 5.9 Implement `list` subcommand: return formatted list of user's memories with scope, category, type, strength, sensitivity
- [x] 5.10 Implement `protect` subcommand: search matching memories, update `sensitivity` field in frontmatter and index
- [x] 5.11 Implement `retention` subcommand: update retention policy configuration for specified memory type
- [x] 5.12 Add retention policy configuration to `MemoryConfig` in config parsing: `memory.retention.episodic`, `memory.retention.semantic`, `memory.retention.procedural` with duration values
- [x] 5.13 Integrate retention policy enforcement into `forgetting_curve.go` `FadeWeakMemories`: archive memories exceeding their type-specific retention period regardless of strength
- [x] 5.14 Implement audit logging: wrap memory read/write/delete operations to log to `memory_audit_log` table, with configurable retention (default 90 days)
- [x] 5.15 Register `memory_manage` tool in the tool registry in `gateway.go`
- [x] 5.16 Write tests for PII detection: email, phone, SSN patterns, false positive handling
- [ ] 5.17 Write tests for sensitivity-based search filtering: secret excluded, private scoping
- [ ] 5.18 Write tests for memory_manage tool: forget flow, list formatting, protect updates
