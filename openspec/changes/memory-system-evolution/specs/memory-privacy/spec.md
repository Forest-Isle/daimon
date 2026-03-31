## ADDED Requirements

### Requirement: Memory management tool
The system SHALL register a `memory_manage` tool in the tool registry that allows users to control their memories through natural language commands during conversations. The tool SHALL support subcommands: `forget`, `list`, `protect`, and `retention`.

#### Scenario: User requests forgetting
- **WHEN** a user says "forget my email address"
- **THEN** the `memory_manage` tool SHALL search for memories matching "email address", present matches to the user for confirmation, and delete confirmed memories

#### Scenario: User lists memories
- **WHEN** a user says "show me what you remember about me"
- **THEN** the `memory_manage` tool SHALL return a formatted list of the user's memories with their scopes, categories, types, and strength scores

#### Scenario: User protects sensitive information
- **WHEN** a user says "this is confidential" or "don't share this"
- **THEN** the `memory_manage` tool SHALL mark the most recent relevant memories with `sensitivity: secret`

### Requirement: PII detection during fact extraction
The system SHALL apply PII detection to extracted facts before storage. Detection SHALL use a dual-layer approach: (a) regex patterns for structured PII (email addresses, phone numbers, credit card numbers, social security numbers), and (b) optional LLM-based detection for unstructured PII (physical addresses, full names in sensitive context). Facts containing detected PII SHALL automatically receive `sensitivity: private` in their frontmatter.

#### Scenario: Email detected in fact
- **WHEN** a fact contains text matching an email address pattern (e.g., "user@example.com")
- **THEN** the fact SHALL be stored with `sensitivity: private` in its frontmatter

#### Scenario: No PII detected
- **WHEN** a fact contains no text matching any PII pattern
- **THEN** the fact SHALL be stored with `sensitivity: public` (default)

#### Scenario: LLM PII detection for addresses
- **WHEN** LLM-based PII detection is enabled and a fact contains "I live at 123 Main Street, Springfield"
- **THEN** the fact SHALL be stored with `sensitivity: private`

### Requirement: Sensitivity classification field
The system SHALL support a `sensitivity` field in memory file frontmatter with values: `public` (default, no restrictions), `private` (excluded from broad searches, included only in targeted user-specific queries), and `secret` (excluded from all automated retrieval, accessible only via explicit `memory_manage list` command). Memory files without a `sensitivity` field SHALL default to `public`.

#### Scenario: Secret memory excluded from search
- **WHEN** a memory has `sensitivity: secret` and a search query matches its content
- **THEN** the memory SHALL NOT appear in search results

#### Scenario: Private memory in user-specific query
- **WHEN** a memory has `sensitivity: private` and a search is scoped to the owning user
- **THEN** the memory SHALL appear in search results

#### Scenario: Private memory in broad query
- **WHEN** a memory has `sensitivity: private` and a search has no user scope filter
- **THEN** the memory SHALL NOT appear in search results

### Requirement: Configurable retention policies
The system SHALL support per-type retention policies that automatically delete memories older than a configured duration. Retention policies SHALL be configurable in `ironclaw.yaml` under `memory.retention` with keys by memory type (e.g., `episodic: 30d`, `semantic: 365d`, `procedural: never`). The value `never` SHALL disable automatic deletion for that type.

#### Scenario: Expired episodic memory
- **WHEN** an episodic memory is older than the configured retention period (e.g., 30 days)
- **THEN** the memory SHALL be automatically archived during the next forgetting curve background task

#### Scenario: No retention policy configured
- **WHEN** no retention policy is configured for a memory type
- **THEN** the memory SHALL only be subject to the standard forgetting curve archival (strength < 0.3)

### Requirement: Memory access audit logging
The system SHALL log memory access events to an `memory_audit_log` SQLite table with fields: `id`, `memory_id`, `action` (read|write|delete|search), `actor` (system|user|tool), `timestamp`, and `details` (JSON). Audit logs SHALL be retained for 90 days by default with configurable retention.

#### Scenario: Memory read logged
- **WHEN** a memory is read during search retrieval
- **THEN** an audit log entry SHALL be created with `action: read` and the search query as details

#### Scenario: Memory deletion logged
- **WHEN** a memory is deleted via the `memory_manage` tool
- **THEN** an audit log entry SHALL be created with `action: delete`, `actor: user`, and the original content summary as details

#### Scenario: Audit log retention
- **WHEN** audit log entries are older than the configured retention period (default 90 days)
- **THEN** the entries SHALL be automatically deleted during the next maintenance cycle
