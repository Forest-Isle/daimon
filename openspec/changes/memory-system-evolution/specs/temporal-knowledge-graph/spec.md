## ADDED Requirements

### Requirement: Temporal edge validity
The system SHALL store temporal validity on knowledge graph edges via `valid_from` (DATETIME, NOT NULL) and `valid_to` (DATETIME, NULL) columns on the `kg_edges` table. An edge with `valid_to = NULL` SHALL be considered currently valid. Existing edges without temporal data SHALL default to `valid_from = created_at` and `valid_to = NULL`.

#### Scenario: New edge with temporal data
- **WHEN** an entity extraction produces a relationship "Alice works_at CompanyB" with temporal context
- **THEN** the edge SHALL be created with `valid_from` set to the current timestamp and `valid_to = NULL`

#### Scenario: Existing edges after migration
- **WHEN** the database migration runs on an existing database with edges
- **THEN** all existing edges SHALL have `valid_from` set to their `created_at` value and `valid_to` set to NULL

### Requirement: Relationship versioning
The system SHALL support relationship versioning: when a new edge supersedes an existing edge of the same type between the same nodes, the old edge SHALL be invalidated by setting its `valid_to` to the current timestamp, and a new edge SHALL be created with the updated properties. The old edge SHALL NOT be deleted.

#### Scenario: Relationship update
- **WHEN** the system detects that "Alice works_at CompanyB" supersedes "Alice works_at CompanyA"
- **THEN** the "Alice works_at CompanyA" edge SHALL have `valid_to` set to the current timestamp AND a new "Alice works_at CompanyB" edge SHALL be created with `valid_from` set to the current timestamp

#### Scenario: Non-conflicting edges preserved
- **WHEN** the system detects "Alice knows Bob" and an existing "Alice works_at CompanyA" edge exists
- **THEN** the existing edge SHALL NOT be modified because the edge types are different

### Requirement: Point-in-time graph queries
The system SHALL support querying the graph state at a specific point in time. The `Neighbors` and `Traverse` methods SHALL accept an optional `asOf` timestamp parameter. When provided, only edges where `valid_from <= asOf AND (valid_to IS NULL OR valid_to > asOf)` SHALL be included.

#### Scenario: Current state query
- **WHEN** a graph traversal is executed without an `asOf` parameter
- **THEN** only edges with `valid_to IS NULL` SHALL be included (current state)

#### Scenario: Historical state query
- **WHEN** a graph traversal is executed with `asOf = "2025-06-15"`
- **THEN** edges valid at that date SHALL be included, including edges that have since been invalidated

### Requirement: Memory-graph write sync
The system SHALL synchronize memory lifecycle operations with the knowledge graph:
- On `ADD`: extract entities from the new fact content and create graph nodes/edges with provenance linking to the memory ID
- On `UPDATE`: update provenance references from old memory ID to new memory ID, then extract entities from new content
- On `DELETE`: weaken edges whose provenance includes the deleted memory ID (reduce weight proportionally to remaining provenance count; set weight to 0.1 if no provenance remains)

#### Scenario: ADD triggers entity extraction
- **WHEN** the lifecycle manager executes an ADD action for a new fact
- **THEN** the graph entity extractor SHALL process the fact content and create nodes/edges with `source_type: "memory"` and `source_id` set to the new fact's ID

#### Scenario: DELETE weakens graph edges
- **WHEN** the lifecycle manager executes a DELETE action for memory "mem_123"
- **THEN** edges with provenance `source_id: "mem_123"` SHALL have that provenance entry removed AND their weight SHALL be recalculated based on remaining provenance count

#### Scenario: DELETE with single provenance
- **WHEN** an edge has only one provenance entry (the deleted memory) and that memory is deleted
- **THEN** the edge weight SHALL be set to 0.1 (zombie state, eligible for decay cleanup)

### Requirement: Graph decay maintenance
The system SHALL run a background graph decay task that: (a) checks provenance validity for all edges (removes provenance entries pointing to non-existent sources), (b) applies gradual weight decay (multiply by 0.9) to edges with no provenance entries, and (c) deletes edges with weight below 0.1.

#### Scenario: Orphaned provenance cleanup
- **WHEN** the graph decay task finds an edge with provenance `source_id: "mem_456"` but no memory with ID "mem_456" exists
- **THEN** the provenance entry SHALL be removed from `kg_provenance`

#### Scenario: Low-weight edge deletion
- **WHEN** the graph decay task finds an edge with weight 0.08 and no provenance entries
- **THEN** the edge SHALL be deleted from `kg_edges`

#### Scenario: Weight decay for unsupported edges
- **WHEN** the graph decay task finds an edge with weight 0.5 and zero provenance entries
- **THEN** the edge weight SHALL be updated to 0.45 (0.5 × 0.9)

### Requirement: Graph-boosted memory reranking
The system SHALL enhance memory search results using knowledge graph connectivity. For the top-3 memory search results, the system SHALL extract entity names, query the graph for connected entities, and boost the scores of memories whose entities share graph connections with the query's entities.

#### Scenario: Connected entities get score boost
- **WHEN** a memory about "Alice" is in the top-3 results AND the query mentions "CompanyB" AND the graph contains "Alice works_at CompanyB"
- **THEN** that memory's search score SHALL be boosted by a factor proportional to the graph path connectivity

#### Scenario: No graph connections
- **WHEN** a memory's entities have no graph connections to the query's entities
- **THEN** that memory's search score SHALL NOT be modified
