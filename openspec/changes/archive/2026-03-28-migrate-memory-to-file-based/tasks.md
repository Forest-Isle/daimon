## 1. Core File Storage Implementation

- [x] 1.1 Create `internal/memory/file_store.go` with FileMemoryStore struct implementing Store interface
- [x] 1.2 Implement Markdown parser with YAML frontmatter parsing (id, scope, user_id, session_id, created_at, updated_at, strength, related_to, promoted_from)
- [x] 1.3 Implement atomic file write helper (write to temp file + rename pattern)
- [x] 1.4 Implement file naming convention: `{scope}/{category}_{YYYYMMDD}_{id}.md`
- [x] 1.5 Create directory structure initialization (`~/.ironclaw/memory/user/`, `session/`, `feedback/`, `global/`, `archived/`)
- [x] 1.6 Implement MEMORY.md index file generation and synchronization

## 2. SQLite Auxiliary Index

- [x] 2.1 Create migration `006_file_memory_index.sql` with memory_index, memory_fts, memory_embeddings tables
- [x] 2.2 Implement index synchronization on file write (insert/update memory_index + memory_fts + memory_embeddings)
- [x] 2.3 Implement index synchronization on file delete (remove from all index tables)
- [x] 2.4 Implement index rebuild command logic (scan files → rebuild index)
- [x] 2.5 Add startup check for index staleness (compare index timestamp vs newest file mtime)

## 3. File-Based Search Implementation

- [x] 3.1 Update Search() to parse MEMORY.md for quick filtering
- [x] 3.2 Update Search() to query memory_index for metadata filtering (scope, user_id, date range)
- [x] 3.3 Update Search() to query memory_fts for BM25 matches
- [x] 3.4 Update Search() to query memory_embeddings for vector matches
- [x] 3.5 Implement RRF fusion with strength weighting (relevance × 0.7 + strength × 0.3)
- [x] 3.6 Update Search() to read Markdown files for top-k results and parse into SearchResult structs

## 4. Forgetting Curve Integration

- [x] 4.1 Update ComputeStrength() to read last_accessed_at from file frontmatter
- [x] 4.2 Implement access tracking: update last_accessed_at in frontmatter on memory retrieval
- [x] 4.3 Cache strength values in memory_index table (add strength column)
- [x] 4.4 Integrate strength into search ranking (already implemented in 3.5)
- [x] 4.5 Implement background fading task: scan files, move strength < 0.3 to archived/ subdirectory
- [x] 4.6 Add fading task to scheduler (run every 24h)

## 5. Unified Lifecycle Manager

- [x] 5.1 Enhance lifecycle decision prompt to include conflict detection instructions
- [x] 5.2 Add conflicting_ids field to LifecycleDecision struct
- [x] 5.3 Update executeAdd() to write Markdown file with frontmatter
- [x] 5.4 Update executeUpdate() to archive old file to archived/ and create new file
- [x] 5.5 Update executeDelete() to move file to archived/ subdirectory
- [x] 5.6 Handle related_to metadata in executeAdd() (write to frontmatter)
- [x] 5.7 Delete `internal/memory/conflict_resolver.go`

## 6. Simplified Caching

- [x] 6.1 Delete `internal/memory/cache.go` (EmbeddingCache + SearchResultCache)
- [x] 6.2 Create new `internal/memory/cached_embedder.go` with single CachedEmbedder implementation
- [x] 6.3 Update CachedEmbedder to use SHA256(text) → embedding map with sync.RWMutex
- [x] 6.4 Update gateway.go to use new CachedEmbedder

## 7. File-Based Consolidation

- [x] 7.1 Update consolidator.go to scan session/ directory for files older than 24h
- [x] 7.2 Update consolidator.go to check file strength from frontmatter (skip if < 0.5)
- [x] 7.3 Implement file move logic: session/ → user/ with atomic rename
- [x] 7.4 Update file frontmatter: set scope='user', add promoted_from and promoted_at fields
- [x] 7.5 Update memory_index on file move (SET file_path, scope WHERE memory_id)

## 8. Migration Tool

- [x] 8.1 Create `cmd/ironclaw/memory.go` with migrate subcommand
- [x] 8.2 Implement backup creation: copy ironclaw.db to ~/.ironclaw/backups/memory_backup_{timestamp}.db
- [x] 8.3 Implement SQLite → Markdown transformation (read memory_facts rows, write files)
- [x] 8.4 Implement embedding migration: copy from old tables to memory_embeddings
- [x] 8.5 Add --dry-run flag to show migration plan without writing
- [x] 8.6 Implement idempotent migration: check file existence before writing
- [x] 8.7 Add progress reporting (e.g., "Migrated 50/200 memories...")
- [x] 8.8 Create restore subcommand to revert from backup

## 9. Gateway Wiring Update

- [x] 9.1 Update gateway.go to initialize FileMemoryStore instead of SQLiteStore
- [x] 9.2 Update gateway.go to pass MemoryConfig to FileMemoryStore constructor
- [x] 9.3 Add migration prompt on startup if legacy data detected
- [x] 9.4 Update completerAdapter to work with FileMemoryStore

## 10. Cleanup & Testing

- [x] 10.1 Delete `internal/memory/sqlite_store.go`
- [x] 10.2 Delete obsolete migration files (001-003) or mark as legacy
- [x] 10.3 Update all memory package tests to use file-based storage
- [x] 10.4 Add integration test for migration tool (SQLite → files → verify)
- [x] 10.5 Add integration test for index rebuild command
- [x] 10.6 Add integration test for consolidation (session → user file move)
- [x] 10.7 Update CLAUDE.md with new file-based architecture
- [x] 10.8 Update README.md with migration instructions
