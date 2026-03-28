## ADDED Requirements

### Requirement: Detect legacy data
The system SHALL detect existing SQLite data in memories or memory_facts tables on startup and prompt user to run migration.

#### Scenario: Detect legacy tables
- **WHEN** system starts and finds non-empty memories or memory_facts tables
- **THEN** system logs warning and suggests running "ironclaw memory migrate"

#### Scenario: Skip migration if already done
- **WHEN** system starts and memory directory exists with files
- **THEN** system skips migration check

### Requirement: CLI migration command
The system SHALL provide "ironclaw memory migrate" command to export all SQLite memory data to Markdown files with progress reporting.

#### Scenario: Run migration command
- **WHEN** user runs "ironclaw memory migrate --from-sqlite --to-files"
- **THEN** system exports all memories to ~/.ironclaw/memory/ and reports progress

#### Scenario: Dry-run mode
- **WHEN** user runs "ironclaw memory migrate --dry-run"
- **THEN** system shows what would be migrated without writing files

### Requirement: Data transformation
Migration SHALL transform each SQLite row into a Markdown file with frontmatter mapping: id→id, session_id→session_id, user_id→user_id, scope→scope, content→body, metadata JSON→frontmatter fields, created_at→created_at, updated_at→updated_at.

#### Scenario: Migrate memory_facts row
- **WHEN** migrating row with id="fact_123", scope="user", content="User prefers dark mode"
- **THEN** creates user/preferences_YYYYMMDD_fact_123.md with frontmatter and content

#### Scenario: Migrate legacy memories row
- **WHEN** migrating row from old memories table
- **THEN** infers scope from session_id presence (session if present, user otherwise)

### Requirement: Preserve embeddings
Migration SHALL copy embeddings from SQLite to memory_embeddings index table, maintaining vector search capability.

#### Scenario: Migrate with embeddings
- **WHEN** migrating memory with existing embedding vector
- **THEN** system inserts embedding into memory_embeddings table linked to new file path

#### Scenario: Regenerate missing embeddings
- **WHEN** migrating memory without embedding
- **THEN** system generates new embedding from content

### Requirement: Backup before migration
Migration SHALL create backup of SQLite database before starting, stored at ~/.ironclaw/backups/memory_backup_TIMESTAMP.db.

#### Scenario: Create backup
- **WHEN** migration starts
- **THEN** system copies ironclaw.db to backup location

#### Scenario: Restore from backup
- **WHEN** migration fails and user runs "ironclaw memory restore"
- **THEN** system restores from latest backup

### Requirement: Idempotent migration
Migration SHALL be idempotent, allowing re-runs without duplicating data by checking file existence before writing.

#### Scenario: Re-run migration
- **WHEN** migration is run twice
- **THEN** system skips files that already exist

#### Scenario: Resume interrupted migration
- **WHEN** migration is interrupted mid-process
- **THEN** system resumes from last successful file on re-run
