## MODIFIED Requirements

### Requirement: Memory strength computation
The system SHALL compute memory strength using a type-dependent forgetting curve formula: R(t) = e^(-t/S) × access_bonus, where t is elapsed time, S is stability computed as importance × type_multiplier hours, and access_bonus is 1 + (0.1 × type_access_factor) × access_count with 1.5× boost for recent access (< 24h). Type multipliers: `episodic` = 12, `semantic` = 24, `procedural` = 48. Type access factors: `episodic` = 1.0, `semantic` = 1.0, `procedural` = 1.2.

#### Scenario: Compute strength for episodic memory
- **WHEN** computing strength for episodic memory with importance=2, created 24 hours ago, accessed 0 times
- **THEN** strength = exp(-24 / (2 × 12)) × 1.0 ≈ 0.37

#### Scenario: Compute strength for semantic memory (same parameters)
- **WHEN** computing strength for semantic memory with importance=2, created 24 hours ago, accessed 0 times
- **THEN** strength = exp(-24 / (2 × 24)) × 1.0 ≈ 0.61

#### Scenario: Compute strength for procedural memory with access
- **WHEN** computing strength for procedural memory with importance=1, created 48 hours ago, accessed 5 times
- **THEN** strength = exp(-48 / (1 × 48)) × (1 + 0.12 × 5) ≈ 0.59

#### Scenario: Recent access boost
- **WHEN** memory was accessed 12 hours ago
- **THEN** access_bonus includes 1.5× multiplier

#### Scenario: Default type for untyped memories
- **WHEN** computing strength for a memory without a type field
- **THEN** the system SHALL use `semantic` type multiplier (24) and access factor (1.0)

### Requirement: Importance metadata
Memory files SHALL support importance field in frontmatter (integer 1-10 scale) where 1=normal, 5=important, 10=critical, affecting decay rate via the stability multiplier. The importance value SHALL be set by the fact extractor during extraction.

#### Scenario: Set importance on creation
- **WHEN** creating memory with importance=7 from fact extraction
- **THEN** frontmatter includes "importance: 7"

#### Scenario: Default importance
- **WHEN** creating memory without importance field or loading a legacy memory file
- **THEN** system defaults to importance=1

#### Scenario: High importance slows decay
- **WHEN** two semantic memories are created at the same time, one with importance=1 and one with importance=8
- **THEN** after 48 hours the importance=8 memory SHALL have significantly higher strength than the importance=1 memory

### Requirement: Strength-based search ranking
Search results SHALL be ranked by combining relevance score (BM25 + vector) with memory strength, using weighted formula: final_score = relevance × 0.7 + strength × 0.3.

#### Scenario: Rank search results
- **WHEN** two memories have equal relevance but different strengths (0.8 vs 0.3)
- **THEN** higher strength memory ranks first

#### Scenario: Balance relevance and recency
- **WHEN** old highly-relevant memory (relevance=0.9, strength=0.2) competes with recent less-relevant memory (relevance=0.6, strength=0.8)
- **THEN** final scores are 0.69 vs 0.66, old memory wins

### Requirement: Automatic memory fading
The system SHALL run a background task every 24 hours to archive memories with strength < 0.3 by moving them to archived/ subdirectory. Additionally, memories that exceed their type-specific retention policy (if configured) SHALL be archived regardless of strength.

#### Scenario: Fade weak session memories
- **WHEN** background task runs and finds session memory with strength=0.25
- **THEN** system moves file from session/ to archived/

#### Scenario: Preserve important memories
- **WHEN** background task runs and finds user memory with strength=0.5
- **THEN** system keeps file in user/ directory

### Requirement: Access tracking for strength
The system SHALL record every memory access in fact_access_log table and update fact_access_stats (access_count, last_access, first_access) for strength computation.

#### Scenario: Record memory access
- **WHEN** memory is retrieved during search
- **THEN** system inserts row into fact_access_log with memory_id, session_id, accessed_at

#### Scenario: Update access stats
- **WHEN** memory is accessed
- **THEN** system increments access_count and updates last_access in fact_access_stats
