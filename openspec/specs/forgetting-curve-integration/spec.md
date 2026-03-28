## ADDED Requirements

### Requirement: Memory strength computation
The system SHALL compute memory strength using forgetting curve formula: R(t) = e^(-t/S) × access_bonus, where t is elapsed time, S is stability (importance × 24 hours), and access_bonus is 1 + 0.1 × access_count with 1.5× boost for recent access (< 24h).

#### Scenario: Compute strength for important memory
- **WHEN** computing strength for memory with importance=2.0, created 48 hours ago, accessed 3 times
- **THEN** strength = exp(-48 / (2.0 × 24)) × (1 + 0.1 × 3) ≈ 0.48

#### Scenario: Recent access boost
- **WHEN** memory was accessed 12 hours ago
- **THEN** access_bonus includes 1.5× multiplier

### Requirement: Strength-based search ranking
Search results SHALL be ranked by combining relevance score (BM25 + vector) with memory strength, using weighted formula: final_score = relevance × 0.7 + strength × 0.3.

#### Scenario: Rank search results
- **WHEN** two memories have equal relevance but different strengths (0.8 vs 0.3)
- **THEN** higher strength memory ranks first

#### Scenario: Balance relevance and recency
- **WHEN** old highly-relevant memory (relevance=0.9, strength=0.2) competes with recent less-relevant memory (relevance=0.6, strength=0.8)
- **THEN** final scores are 0.69 vs 0.66, old memory wins

### Requirement: Automatic memory fading
The system SHALL run a background task every 24 hours to archive memories with strength < 0.3 by moving them to archived/ subdirectory.

#### Scenario: Fade weak session memories
- **WHEN** background task runs and finds session memory with strength=0.25
- **THEN** system moves file from session/ to session/archived/

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

### Requirement: Importance metadata
Memory files SHALL support importance field in frontmatter (0.0-5.0 scale) where 1.0=normal, 2.0=important, 5.0=critical, affecting decay rate.

#### Scenario: Set importance on creation
- **WHEN** creating memory with explicit importance=3.0
- **THEN** frontmatter includes "importance: 3.0"

#### Scenario: Default importance
- **WHEN** creating memory without importance field
- **THEN** system defaults to importance=1.0
