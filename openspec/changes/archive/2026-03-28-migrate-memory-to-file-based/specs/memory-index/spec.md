## ADDED Requirements

### Requirement: SQLite as auxiliary index
The system SHALL maintain a SQLite database with tables for file metadata indexing, full-text search (FTS5), and vector embeddings, while Markdown files remain the source of truth.

#### Scenario: Index new file on creation
- **WHEN** a new memory file is created
- **THEN** system inserts metadata into memory_index table, content into memory_fts, and embedding into memory_embeddings

#### Scenario: Rebuild index from files
- **WHEN** index is corrupted or missing
- **THEN** system scans all Markdown files and rebuilds all index tables

### Requirement: memory_index table schema
The memory_index table SHALL store: file_path (primary key), memory_id, scope, category, user_id, session_id, created_at, updated_at, access_count, last_access, importance.

#### Scenario: Query files by scope
- **WHEN** searching for user-scoped memories
- **THEN** system queries memory_index WHERE scope='user'

#### Scenario: Query files by user_id
- **WHEN** retrieving memories for specific user
- **THEN** system queries memory_index WHERE user_id=?

### Requirement: FTS5 full-text search
The system SHALL use FTS5 virtual table (memory_fts) for BM25-based keyword search with porter stemming and unicode61 tokenization.

#### Scenario: Keyword search
- **WHEN** user searches for "database optimization"
- **THEN** system queries memory_fts and returns ranked results by BM25 score

#### Scenario: FTS5 unavailable fallback
- **WHEN** SQLite build lacks FTS5 support
- **THEN** system falls back to LIKE queries on memory_index

### Requirement: Vector embedding index
The system SHALL store embeddings in memory_embeddings table with optional HNSW indexing via sqlite-vss for fast similarity search.

#### Scenario: Vector similarity search
- **WHEN** searching with query embedding
- **THEN** system computes cosine similarity against all embeddings and returns top-k results

#### Scenario: HNSW acceleration
- **WHEN** sqlite-vss extension is available
- **THEN** system uses HNSW index for sub-linear search time

### Requirement: Index synchronization
The system SHALL keep SQLite index synchronized with file changes through automatic reindexing on file write/delete.

#### Scenario: Update index on file modification
- **WHEN** a memory file is updated
- **THEN** system updates corresponding rows in memory_index, memory_fts, and memory_embeddings

#### Scenario: Remove index entry on file deletion
- **WHEN** a memory file is deleted
- **THEN** system removes corresponding rows from all index tables
