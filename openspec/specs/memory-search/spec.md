## MODIFIED Requirements

### Requirement: File-based search flow
The system SHALL search memories by: (1) parsing MEMORY.md index for quick filtering, (2) querying SQLite index (FTS5 + vector), (3) applying forgetting curve ranking, (4) reading Markdown file content for top-k results.

#### Scenario: Search with keyword query
- **WHEN** user searches for "database optimization"
- **THEN** system queries memory_fts for BM25 matches, memory_embeddings for vector matches, fuses with RRF, applies strength weighting, reads top-5 Markdown files

#### Scenario: Scope-filtered search
- **WHEN** searching with scope filter (e.g., user-only)
- **THEN** system queries memory_index WHERE scope='user' before FTS/vector search

#### Scenario: Fast index lookup
- **WHEN** MEMORY.md index is available
- **THEN** system parses index to get candidate file paths before SQL queries

### Requirement: Hybrid search with RRF fusion
The system SHALL maintain hybrid search (BM25 + vector) with Reciprocal Rank Fusion, but read final content from Markdown files instead of database.

#### Scenario: Fuse BM25 and vector results
- **WHEN** BM25 returns [file1, file2] and vector returns [file2, file3]
- **THEN** RRF fusion ranks: file2 (appears in both), file1, file3

#### Scenario: Read file content for results
- **WHEN** search returns top-5 file paths
- **THEN** system reads Markdown files and parses content into SearchResult structs

### Requirement: Strength-weighted ranking
Search results SHALL be re-ranked using forgetting curve strength: final_score = relevance_score × 0.7 + memory_strength × 0.3.

#### Scenario: Boost recent memories
- **WHEN** two results have equal relevance but different strengths (0.8 vs 0.3)
- **THEN** higher strength memory ranks first in final results

#### Scenario: Balance relevance and recency
- **WHEN** old highly-relevant memory competes with recent less-relevant memory
- **THEN** weighted formula balances both factors

### Requirement: Index-first optimization
The system SHALL use SQLite index for fast filtering before reading files, avoiding full directory scans.

#### Scenario: Query by user_id
- **WHEN** searching for specific user's memories
- **THEN** system queries memory_index WHERE user_id=? to get file paths, then reads only those files

#### Scenario: Query by date range
- **WHEN** searching memories from last 7 days
- **THEN** system queries memory_index WHERE created_at > ? to filter files
