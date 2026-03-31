## MODIFIED Requirements

### Requirement: File-based search flow
The system SHALL search memories using a layered retrieval strategy: (1) always load user profile file if it exists and include in system prompt, (2) query SQLite index for summaries first (type=summary), (3) backfill with raw facts if summary results are insufficient, (4) apply forgetting curve ranking, (5) apply graph-boosted reranking for top results, (6) read Markdown file content for final results. Memories with `sensitivity: secret` SHALL be excluded from all automated searches.

#### Scenario: Profile always included
- **WHEN** building a system prompt and user/profile_{user_id}.md exists
- **THEN** the profile content SHALL be included as a "User Context" section in the system prompt, regardless of query

#### Scenario: Summaries preferred
- **WHEN** searching for "deployment preferences" and a summary with matching content exists
- **THEN** the summary SHALL be returned instead of its constituent source facts

#### Scenario: Backfill with raw facts
- **WHEN** searching and only 2 summaries match but top-k is 5
- **THEN** system SHALL backfill with up to 3 raw fact results

#### Scenario: Secret memories excluded
- **WHEN** a memory with `sensitivity: secret` matches the search query
- **THEN** the memory SHALL NOT appear in search results

#### Scenario: Scope-filtered search
- **WHEN** searching with scope filter (e.g., user-only)
- **THEN** system queries memory_index WHERE scope='user' before FTS/vector search

### Requirement: Hybrid search with RRF fusion
The system SHALL maintain hybrid search (BM25 + vector) with Reciprocal Rank Fusion, but read final content from Markdown files instead of database. Search SHALL support filtering by `type` field to enable layered retrieval.

#### Scenario: Fuse BM25 and vector results
- **WHEN** BM25 returns [file1, file2] and vector returns [file2, file3]
- **THEN** RRF fusion ranks: file2 (appears in both), file1, file3

#### Scenario: Filter by memory type
- **WHEN** search is called with type filter "summary"
- **THEN** only memories with `type: summary` in their frontmatter/index SHALL be considered

#### Scenario: Read file content for results
- **WHEN** search returns top-5 file paths
- **THEN** system reads Markdown files and parses content into SearchResult structs

### Requirement: Strength-weighted ranking
Search results SHALL be re-ranked using forgetting curve strength: final_score = relevance_score × 0.7 + memory_strength × 0.3. Strength computation SHALL use type-dependent forgetting curve parameters.

#### Scenario: Boost recent memories
- **WHEN** two results have equal relevance but different strengths (0.8 vs 0.3)
- **THEN** higher strength memory ranks first in final results

#### Scenario: Balance relevance and recency
- **WHEN** old highly-relevant memory competes with recent less-relevant memory
- **THEN** weighted formula balances both factors

### Requirement: Graph-boosted reranking
For the top-3 search results, the system SHALL extract entity names from their content, query the knowledge graph for connections to entities mentioned in the original query, and boost scores of results whose entities have graph-path connectivity to query entities.

#### Scenario: Graph connectivity boosts score
- **WHEN** a result about "Alice" is in top-3 and query mentions "CompanyB" and graph has "Alice works_at CompanyB"
- **THEN** the result's score SHALL be boosted proportionally to graph connectivity

#### Scenario: No graph connections
- **WHEN** a result's entities have no graph connections to query entities
- **THEN** the result's score SHALL NOT be modified

### Requirement: Index-first optimization
The system SHALL use SQLite index for fast filtering before reading files, avoiding full directory scans. The index SHALL support filtering by `type` and `sensitivity` fields.

#### Scenario: Query by user_id
- **WHEN** searching for specific user's memories
- **THEN** system queries memory_index WHERE user_id=? to get file paths, then reads only those files

#### Scenario: Filter by type in index
- **WHEN** searching with type filter "summary"
- **THEN** system queries memory_index WHERE type='summary' to narrow candidates before FTS/vector search
