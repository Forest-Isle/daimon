## MODIFIED Requirements

### Requirement: File-based consolidation
The system SHALL promote session memories to user scope by moving files from session/ subdirectory to user/ subdirectory and updating frontmatter scope field, instead of database record updates. The consolidator SHALL remain unchanged in its role; hierarchical compression (fact-to-summary compaction) is handled by a separate Compactor component that runs after consolidation.

#### Scenario: Promote session file to user scope
- **WHEN** consolidation runs and finds session file older than 24 hours
- **THEN** system moves file from session/ to user/, updates frontmatter scope='user', and updates memory_index

#### Scenario: Preserve file history
- **WHEN** moving file during consolidation
- **THEN** system updates frontmatter with "promoted_from: session/conversation_20260327_abc123.md" and "promoted_at: 2026-03-28T10:00:00Z"

### Requirement: Consolidation criteria
Consolidation SHALL promote session files that are: (1) older than consolidation interval (default 24h), (2) have user_id set, (3) have strength > 0.5 (using type-dependent forgetting curve).

#### Scenario: Skip weak session memories
- **WHEN** session file has strength < 0.5
- **THEN** system skips promotion and leaves file in session/

#### Scenario: Skip recent session memories
- **WHEN** session file is less than 24 hours old
- **THEN** system skips promotion regardless of strength

#### Scenario: Type-dependent strength evaluation
- **WHEN** evaluating an episodic session memory for promotion
- **THEN** strength SHALL be computed using the episodic type multiplier (12 hours) rather than the default semantic multiplier (24 hours)

### Requirement: Background consolidation task
The system SHALL run consolidation as a background task every 24 hours (configurable via memory.consolidation_interval).

#### Scenario: Scheduled consolidation
- **WHEN** 24 hours pass since last consolidation
- **THEN** system scans session/ directory and promotes eligible files

#### Scenario: Manual consolidation trigger
- **WHEN** user runs "ironclaw memory consolidate"
- **THEN** system immediately runs consolidation process

### Requirement: Index synchronization during consolidation
When moving files, the system SHALL update memory_index table to reflect new file paths, scope changes, and any new frontmatter fields (type, importance, emotion, sensitivity).

#### Scenario: Update index on file move
- **WHEN** file is moved from session/ to user/
- **THEN** system updates memory_index SET file_path='user/...', scope='user' WHERE memory_id=?

#### Scenario: Maintain search continuity
- **WHEN** file is moved during consolidation
- **THEN** searches continue to work using updated index without reindexing
