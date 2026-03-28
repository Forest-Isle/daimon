## ADDED Requirements

### Requirement: Base directory structure
The system SHALL organize memory files under `~/.ironclaw/memory/` with subdirectories: user/, session/, feedback/, global/, and a root-level MEMORY.md index file.

#### Scenario: Initialize memory directory
- **WHEN** system starts for the first time
- **THEN** system creates ~/.ironclaw/memory/ with subdirectories user/, session/, feedback/, global/

#### Scenario: Locate memory directory
- **WHEN** system needs to access memory files
- **THEN** system resolves path to ~/.ironclaw/memory/ (or custom path from config)

### Requirement: MEMORY.md index file
The system SHALL maintain a MEMORY.md file at the root of memory directory containing a human-readable index of all memory files with links and brief descriptions.

#### Scenario: Update index on new file
- **WHEN** a new memory file is created
- **THEN** system appends an entry to MEMORY.md with format: `- [Title](path/to/file.md) — brief description`

#### Scenario: Rebuild index from files
- **WHEN** MEMORY.md is missing or corrupted
- **THEN** system scans all subdirectories and regenerates MEMORY.md

### Requirement: Scope-based subdirectories
Memory files SHALL be organized into subdirectories by scope: user/ for long-term user memories, session/ for conversation-specific memories, feedback/ for user corrections and preferences, global/ for system-wide facts.

#### Scenario: Save user preference
- **WHEN** saving a user-scoped preference memory
- **THEN** system writes file to user/ subdirectory

#### Scenario: Save session memory
- **WHEN** saving a session-scoped memory
- **THEN** system writes file to session/ subdirectory with session ID in filename

### Requirement: File naming pattern
Files SHALL follow naming pattern: `{category}_{date}_{short_id}.md` where category is the memory category (preferences, identity, context, etc.), date is YYYYMMDD, and short_id is first 8 chars of memory ID.

#### Scenario: User preference filename
- **WHEN** creating user preference on 2026-03-28 with ID "fact_abc123"
- **THEN** filename is "preferences_20260328_abc123.md" in user/ directory

#### Scenario: Session conversation filename
- **WHEN** creating session memory on 2026-03-28 with ID "fact_xyz789"
- **THEN** filename is "conversation_20260328_xyz789.md" in session/ directory

### Requirement: Git-friendly structure
The directory structure SHALL be optimized for Git version control with human-readable filenames, stable paths, and meaningful commit diffs.

#### Scenario: Track memory changes in Git
- **WHEN** user commits memory directory to Git
- **THEN** diffs show readable Markdown changes with clear file paths

#### Scenario: Merge memory changes
- **WHEN** multiple users modify different memory files
- **THEN** Git can merge changes without conflicts (separate files)
