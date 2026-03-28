## Context

IronClaw's memory system currently uses SQLite as the primary storage with 17 Go files implementing a complex dual-table architecture (`memories` + `memory_facts`). This design migrates to a file-first approach inspired by Claude Code, where Markdown files become the source of truth and SQLite serves as an auxiliary index for search performance.

**Current State:**
- Primary storage: SQLite tables (memories, memory_facts)
- 17 Go files with overlapping responsibilities
- 3-layer caching system
- Forgetting curve implemented but not activated
- Separate conflict resolver duplicating lifecycle logic

**Constraints:**
- Must maintain search performance (BM25 + vector hybrid)
- Must preserve existing data through migration
- Must support Git-friendly version control
- Must remain embeddable (no external dependencies)

**Stakeholders:**
- Users: Want human-readable, editable memory files
- Developers: Want simpler codebase and clearer architecture
- System: Needs fast search and efficient storage

## Goals / Non-Goals

**Goals:**
- Migrate primary storage from SQLite to Markdown files with YAML frontmatter
- Reduce codebase from 17 files to 9 core files
- Enable Git-friendly version control of memories
- Activate forgetting curve for intelligent memory decay
- Unify conflict detection into lifecycle manager
- Maintain or improve search performance

**Non-Goals:**
- Changing the Store interface API (keep backward compatibility at interface level)
- Supporting real-time collaborative editing (single-user focus)
- Implementing distributed memory synchronization
- Adding new memory types beyond existing scopes (session/user/global)

## Decisions

### Decision 1: Markdown as Primary Storage

**Choice:** Use Markdown files with YAML frontmatter as the source of truth, SQLite as auxiliary index.

**Rationale:**
- Human-readable: Users can inspect and edit memories directly
- Git-friendly: Each memory is a separate file, enabling version control and diffs
- Durability: Files are more resilient than database corruption
- Portability: Easy to backup, share, and migrate

**Alternatives Considered:**
- Pure SQLite: Rejected due to lack of human readability and Git integration
- JSON files: Rejected because Markdown is more readable for content-heavy memories
- Hybrid with SQLite primary: Rejected because it doesn't solve the readability problem

**Trade-offs:**
- (+) Human-editable, Git-friendly, portable
- (-) Slightly slower writes (file I/O + index update vs single SQL transaction)
- Mitigation: Use atomic writes (temp file + rename) and batch index updates

### Decision 2: Directory Structure by Scope

**Choice:** Organize files into subdirectories: `user/`, `session/`, `feedback/`, `global/`.

**Rationale:**
- Clear organization: Scope is immediately visible from file path
- Easy cleanup: Delete entire `session/` directory to clear temporary memories
- Git-friendly: Users can selectively commit different scopes

**Alternatives Considered:**
- Flat directory with scope in filename: Rejected due to poor scalability (thousands of files in one directory)
- Database-style partitioning: Rejected because it's not human-friendly

**Trade-offs:**
- (+) Clear organization, easy navigation
- (-) Requires directory traversal for cross-scope searches
- Mitigation: SQLite index provides fast cross-scope queries

### Decision 3: SQLite as Auxiliary Index

**Choice:** Maintain SQLite with three tables: `memory_index` (metadata), `memory_fts` (FTS5), `memory_embeddings` (vectors).

**Rationale:**
- Performance: FTS5 and vector search are much faster than scanning files
- Consistency: Single source of truth (files) with derived index
- Graceful degradation: If index is corrupted, rebuild from files

**Alternatives Considered:**
- No index (scan files): Rejected due to poor search performance (O(n) vs O(log n))
- External search engine (Elasticsearch): Rejected due to complexity and external dependency

**Trade-offs:**
- (+) Fast search, embeddable, no external deps
- (-) Index must stay synchronized with files
- Mitigation: Update index atomically with file writes, provide rebuild command

### Decision 4: Atomic File Writes

**Choice:** Use write-to-temp-then-rename pattern for all file operations.

**Rationale:**
- Atomicity: Prevents partial writes and corruption
- Crash safety: Either old file or new file exists, never half-written
- Standard practice: Used by Git, databases, and other reliable systems

**Implementation:**
```go
// Write to temp file
tmpPath := filepath.Join(dir, ".tmp_"+filename)
ioutil.WriteFile(tmpPath, data, 0644)

// Atomic rename
os.Rename(tmpPath, finalPath)
```

**Trade-offs:**
- (+) Crash-safe, prevents corruption
- (-) Slightly more disk I/O (write + rename vs single write)
- Mitigation: Negligible performance impact for memory-sized files

### Decision 5: Unified Lifecycle Manager

**Choice:** Merge conflict detection from `conflict_resolver.go` into `lifecycle.go`.

**Rationale:**
- Single responsibility: Lifecycle manager already handles ADD/UPDATE/DELETE decisions
- Conflict detection is part of lifecycle: Deciding whether to update vs add is conflict resolution
- Code reduction: Eliminates duplicate similarity search and LLM calls

**Implementation:**
- Enhance lifecycle decision prompt to include conflict detection instructions
- Add `conflicting_ids` field to decision response
- Handle conflict actions (archive old file, link related facts) in execute methods

**Trade-offs:**
- (+) Simpler architecture, no duplicate logic
- (-) Lifecycle manager becomes slightly more complex
- Mitigation: Well-defined prompt structure keeps logic clear

### Decision 6: Activate Forgetting Curve

**Choice:** Integrate forgetting curve into search ranking and background cleanup.

**Rationale:**
- Already implemented: Code exists but unused
- Improves relevance: Recent and frequently-accessed memories rank higher
- Automatic cleanup: Weak memories fade to archive automatically

**Implementation:**
- Search ranking: `final_score = relevance × 0.7 + strength × 0.3`
- Background task: Run every 24h, move files with strength < 0.3 to `archived/` subdirectory
- Access tracking: Record every memory retrieval in `fact_access_log`

**Trade-offs:**
- (+) Smarter ranking, automatic cleanup
- (-) Additional computation for strength calculation
- Mitigation: Cache strength values in memory_index, recompute only on access

### Decision 7: Simplified Caching

**Choice:** Merge 3-layer cache (EmbeddingCache + CachedEmbedder + SearchResultCache) into single CachedEmbedder.

**Rationale:**
- Redundancy: CachedEmbedder wraps EmbeddingCache unnecessarily
- Search cache issues: 5min TTL can serve stale results after writes
- Simplicity: Single cache is easier to reason about

**Implementation:**
```go
type CachedEmbedder struct {
    provider EmbeddingProvider
    cache    map[string][]float32  // SHA256(text) → embedding
    mu       sync.RWMutex
}
```

**Trade-offs:**
- (+) Simpler code, no stale search results
- (-) No search result caching (must recompute)
- Mitigation: HNSW index makes vector search fast enough (<10ms)

### Decision 8: MEMORY.md Index File

**Choice:** Maintain a human-readable `MEMORY.md` index at root with links to all memory files.

**Rationale:**
- Discoverability: Users can browse memories without tools
- Git-friendly: Index shows high-level changes in diffs
- Fast filtering: Parse index for quick scope/category filtering before SQL queries

**Format:**
```markdown
# Memory Index

## User Memories
- [Preferences](user/preferences_20260328_abc123.md) — User prefers dark mode
- [Identity](user/identity_20260327_def456.md) — User is a Python developer

## Session Memories
- [Conversation 2026-03-28](session/conversation_20260328_xyz789.md) — Discussion about databases
```

**Trade-offs:**
- (+) Human-readable, fast filtering
- (-) Must keep synchronized with files
- Mitigation: Rebuild index on startup if missing/stale

## Risks / Trade-offs

### Risk 1: Index Desynchronization
**Risk:** SQLite index becomes out of sync with Markdown files if writes fail partially.

**Mitigation:**
- Atomic operations: Update file and index in single transaction-like sequence
- Rebuild command: `ironclaw memory reindex` scans files and rebuilds index
- Startup check: Detect missing index entries and auto-rebuild

### Risk 2: Migration Data Loss
**Risk:** Migration from SQLite to files could lose data if interrupted or buggy.

**Mitigation:**
- Automatic backup: Create `~/.ironclaw/backups/memory_backup_TIMESTAMP.db` before migration
- Idempotent migration: Check file existence before writing, allow re-runs
- Dry-run mode: `--dry-run` flag shows what would be migrated without writing
- Restore command: `ironclaw memory restore` reverts to backup

### Risk 3: Performance Regression
**Risk:** File I/O could be slower than pure SQLite for writes.

**Mitigation:**
- Benchmark: Measure write latency (target: <10ms per memory)
- Batch operations: Group multiple writes when possible
- Async indexing: Update SQLite index asynchronously after file write succeeds

### Risk 4: Large File Count
**Risk:** Thousands of memory files could slow down directory operations.

**Mitigation:**
- Index-first queries: Use SQLite index to get file paths, avoid directory scans
- Archived subdirectories: Move old memories to `archived/` to reduce active file count
- Periodic cleanup: Background task archives weak memories

### Risk 5: Concurrent Access
**Risk:** Multiple processes writing to same memory file could cause corruption.

**Mitigation:**
- File locking: Use `flock` or OS-level locks during writes
- Single-process assumption: IronClaw is designed for single-user, single-process use
- Future: Add lock file (`.ironclaw.lock`) if multi-process support needed

## Migration Plan

### Phase 1: Implement Core (Week 1)
1. Create `file_memory.go` with FileMemoryStore implementation
2. Implement Markdown parsing with YAML frontmatter
3. Implement atomic file writes (temp + rename)
4. Create SQLite index tables (memory_index, memory_fts, memory_embeddings)
5. Implement index synchronization on file write/delete

### Phase 2: Implement Search (Week 1)
1. Update search flow: parse MEMORY.md → query index → read files
2. Integrate forgetting curve into ranking
3. Simplify cache to single CachedEmbedder
4. Update lifecycle manager with conflict detection

### Phase 3: Migration Tool (Week 2)
1. Implement `ironclaw memory migrate` command
2. Add backup creation before migration
3. Implement SQLite → Markdown transformation
4. Add dry-run and resume capabilities
5. Test migration with production-like data

### Phase 4: Cleanup & Testing (Week 2)
1. Delete obsolete files (sqlite_store.go, conflict_resolver.go, etc.)
2. Update gateway.go wiring to use FileMemoryStore
3. Update all tests to use file-based storage
4. Add integration tests for migration
5. Update documentation

### Rollback Strategy
If critical issues arise post-deployment:
1. Restore SQLite backup: `ironclaw memory restore`
2. Revert code to previous version
3. Restart with old SQLite-based storage
4. Investigate and fix issues before re-attempting migration

### Deployment
1. Release with migration tool but keep SQLite support
2. Prompt users to run migration on startup
3. Monitor for issues during migration period
4. Remove SQLite storage code in next major version

## Open Questions

1. **File size limits:** Should we split large memories (>1MB) into chunks?
   - Proposal: Add chunking for files >100KB, link chunks in frontmatter
   - Decision: Defer until we see real-world file sizes

2. **Concurrent writes:** Do we need multi-process support?
   - Current assumption: Single-process use case
   - Decision: Add file locking if users report issues

3. **Index rebuild frequency:** How often should we auto-rebuild index?
   - Proposal: On startup if index is >24h older than newest file
   - Decision: Implement and monitor performance impact

4. **Archive retention:** How long to keep archived memories?
   - Proposal: Keep indefinitely, let users manually delete
   - Alternative: Add TTL for archived files (e.g., 90 days)
   - Decision: Start with indefinite retention, add TTL if storage becomes issue
