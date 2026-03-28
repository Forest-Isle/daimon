## ADDED Requirements

### Requirement: Markdown files as primary storage
The system SHALL store memory entries as individual Markdown files with YAML frontmatter for metadata and human-readable content in the body.

#### Scenario: Save new memory to file
- **WHEN** a new memory fact is created
- **THEN** system writes a Markdown file with frontmatter (id, scope, category, created_at, updated_at, version) and content body

#### Scenario: Read memory from file
- **WHEN** system needs to retrieve a memory by ID
- **THEN** system parses the Markdown file, extracts frontmatter as metadata and body as content

### Requirement: YAML frontmatter structure
Each memory file SHALL include YAML frontmatter with required fields: id, scope, category, created_at, updated_at, version, and optional fields: expires_at, user_id, session_id, tags, importance.

#### Scenario: Parse valid frontmatter
- **WHEN** system reads a file with valid YAML frontmatter
- **THEN** system extracts all fields into MemoryFile struct

#### Scenario: Handle missing optional fields
- **WHEN** frontmatter lacks optional fields (e.g., expires_at)
- **THEN** system uses default values (nil for expires_at, empty string for user_id)

### Requirement: File naming convention
Memory files SHALL be named using the pattern: `{scope}_{category}_{timestamp}_{short_id}.md` where short_id is first 8 chars of the memory ID.

#### Scenario: Generate filename for user preference
- **WHEN** creating a user-scoped preference memory with ID "fact_abc123def456"
- **THEN** filename is "user_preferences_20260328_abc123de.md"

#### Scenario: Generate filename for session memory
- **WHEN** creating a session-scoped memory with ID "fact_xyz789"
- **THEN** filename includes session timestamp and short ID

### Requirement: Atomic file writes
File writes SHALL be atomic using write-to-temp-then-rename pattern to prevent corruption.

#### Scenario: Successful atomic write
- **WHEN** saving a memory file
- **THEN** system writes to temp file, then renames to final path

#### Scenario: Handle write failure
- **WHEN** disk write fails during save
- **THEN** system leaves original file intact and returns error
