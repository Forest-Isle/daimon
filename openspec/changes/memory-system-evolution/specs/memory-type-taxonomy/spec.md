## ADDED Requirements

### Requirement: Memory type classification
The system SHALL classify each memory entry into one of three types: `episodic` (time-bound events and experiences), `semantic` (general knowledge, preferences, and facts), or `procedural` (behavioral patterns, workflows, and learned procedures). The type SHALL be stored in the YAML frontmatter `type` field of the memory file. Memory files without a `type` field SHALL default to `semantic`.

#### Scenario: New fact gets type classification
- **WHEN** the fact extractor processes a conversation exchange
- **THEN** each extracted fact SHALL include a `type` field with value `episodic`, `semantic`, or `procedural`

#### Scenario: Existing memory file without type field
- **WHEN** a memory file is loaded that has no `type` field in its frontmatter
- **THEN** the system SHALL treat it as `type: semantic` without modifying the file

### Requirement: Importance scoring
The system SHALL assign an importance score (integer 1-10) to each extracted fact during the fact extraction phase. The score SHALL be stored in the YAML frontmatter `importance` field. Memory files without an `importance` field SHALL default to importance `1`.

#### Scenario: Fact extraction includes importance
- **WHEN** the LLM fact extractor processes a conversation exchange
- **THEN** each extracted fact object SHALL include an `importance` field with an integer value from 1 to 10

#### Scenario: User-expressed preference gets high importance
- **WHEN** a user explicitly states a preference (e.g., "I prefer dark mode")
- **THEN** the extracted fact SHALL receive an importance score of 7 or higher

#### Scenario: Ephemeral mention gets low importance
- **WHEN** a fact is extracted from casual conversation without explicit emphasis
- **THEN** the extracted fact SHALL receive an importance score of 3 or lower

### Requirement: Emotion annotation
The system SHALL annotate each extracted fact with an emotion classification: `positive`, `negative`, or `neutral`. The emotion SHALL be stored in the YAML frontmatter `emotion` field. Memory files without an `emotion` field SHALL default to `neutral`.

#### Scenario: Complaint or correction gets negative emotion
- **WHEN** a user expresses dissatisfaction or corrects the agent's behavior
- **THEN** the extracted fact SHALL receive `emotion: negative`

#### Scenario: Neutral factual statement
- **WHEN** a user states objective information without emotional emphasis
- **THEN** the extracted fact SHALL receive `emotion: neutral`

### Requirement: Per-type forgetting curve parameters
The system SHALL apply different forgetting curve decay rates based on memory type:
- `episodic`: Fast decay (stability multiplier = 12 hours × importance)
- `semantic`: Standard decay (stability multiplier = 24 hours × importance, current behavior)
- `procedural`: Slow decay with reinforcement (stability multiplier = 48 hours × importance, access bonus multiplied by 1.2× instead of 1.0×)

#### Scenario: Episodic memory decays faster than semantic
- **WHEN** an episodic memory and a semantic memory are created at the same time with the same importance
- **THEN** the episodic memory SHALL have lower strength after 24 hours than the semantic memory

#### Scenario: Procedural memory strengthens with use
- **WHEN** a procedural memory is accessed multiple times
- **THEN** its access bonus SHALL grow 20% faster than an equivalent semantic memory with the same access count

### Requirement: Updated fact extraction prompt
The system SHALL use an updated LLM extraction prompt that outputs facts as JSON objects with fields: `content`, `category`, `type`, `importance`, and `emotion`.

#### Scenario: Extraction output format
- **WHEN** the fact extractor produces output
- **THEN** each fact object SHALL contain all five fields: `content` (string), `category` (string), `type` (episodic|semantic|procedural), `importance` (integer 1-10), `emotion` (positive|negative|neutral)
