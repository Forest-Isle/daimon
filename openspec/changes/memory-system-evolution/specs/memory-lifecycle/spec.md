## MODIFIED Requirements

### Requirement: Lifecycle decision with conflict detection
The system SHALL decide ADD/UPDATE/DELETE/NOOP action for new facts by searching similar memories, calling LLM for decision, and integrating conflict detection logic into the decision prompt. After executing the lifecycle action, the system SHALL synchronize the operation with the knowledge graph (extract entities on ADD, update provenance on UPDATE, weaken edges on DELETE). After each Process() call, the system SHALL check reflection trigger conditions.

#### Scenario: Detect conflicting information
- **WHEN** new fact "User prefers light mode" conflicts with existing "User prefers dark mode"
- **THEN** LLM decides UPDATE with target_id of old fact, or DELETE if invalidated

#### Scenario: Detect complementary information
- **WHEN** new fact "User likes Python" is related to existing "User is a developer"
- **THEN** LLM decides ADD with metadata linking to related fact

#### Scenario: Detect duplicate information
- **WHEN** new fact is semantically identical to existing fact
- **THEN** LLM decides NOOP

#### Scenario: ADD triggers graph entity extraction
- **WHEN** lifecycle decision is ADD for a new fact
- **THEN** after saving the memory file, the system SHALL extract entities from the fact content and create graph nodes/edges with provenance `source_type: "memory"` and `source_id` set to the new fact's ID

#### Scenario: DELETE triggers graph edge weakening
- **WHEN** lifecycle decision is DELETE for memory "mem_123"
- **THEN** after deleting the memory file, the system SHALL weaken graph edges whose provenance references "mem_123"

#### Scenario: Process triggers reflection check
- **WHEN** a lifecycle Process() call completes
- **THEN** the system SHALL update the reflection tracker (increment counter, update topic embedding) and check trigger conditions

### Requirement: Enhanced decision prompt
The lifecycle decision prompt SHALL include conflict detection instructions: identify contradictions, temporal supersession, and relationship types (replaces/complements/duplicates).

#### Scenario: Prompt includes conflict context
- **WHEN** calling LLM for lifecycle decision
- **THEN** prompt includes: "Check if new fact contradicts, updates, or complements existing memories. Mark conflicting_ids if found."

#### Scenario: LLM returns conflict metadata
- **WHEN** LLM detects conflict
- **THEN** response JSON includes: {"action": "UPDATE", "target_id": "fact_123", "reason": "Contradicts previous preference", "conflicting_ids": ["fact_123"]}

### Requirement: Unified conflict resolution
Conflict resolution actions (update/keep_both/flag_review) SHALL be handled within lifecycle manager's execute methods, eliminating separate ConflictResolver.

#### Scenario: Execute UPDATE for conflict
- **WHEN** lifecycle decision is UPDATE due to conflict
- **THEN** system archives old file to archived/ subdirectory, creates new file, and updates graph provenance from old to new memory ID

#### Scenario: Execute keep_both for complementary facts
- **WHEN** lifecycle decision is ADD with related_to metadata
- **THEN** system creates new file with frontmatter field "related_to: fact_123"
