## Requirements

### Requirement: Large tool results persisted to disk
Tool results exceeding a configurable size threshold SHALL be written to disk and replaced in the session context with a truncated preview and a reference to the full output file.

#### Scenario: Tool result exceeds threshold
- **WHEN** a tool execution produces output larger than `tools.result_persistence.threshold_bytes` (default 8192)
- **THEN** the full output SHALL be written to `{cache_dir}/{session_id}/{tool_use_id}.txt` and the session message SHALL contain only a preview of `preview_chars` characters plus a `[TRUNCATED — full output: {path}]` reference

#### Scenario: Tool result within threshold
- **WHEN** a tool execution produces output smaller than or equal to the threshold
- **THEN** the full output SHALL remain inline in the session message with no disk persistence

#### Scenario: Tool result is an error
- **WHEN** a tool execution produces an error
- **THEN** the error message SHALL always remain inline regardless of size (errors are typically short and critical for LLM reasoning)

### Requirement: Preview truncation at line boundaries
The preview of a persisted tool result SHALL be truncated at the nearest line boundary at or before the configured `preview_chars` limit to avoid cutting mid-line.

#### Scenario: Preview truncation
- **WHEN** a 20KB tool result is persisted with `preview_chars` set to 2000
- **THEN** the preview SHALL contain complete lines totaling at most 2000 characters, ending at the last newline before the 2000th character

### Requirement: Automatic cache cleanup
Persisted tool results older than the configured TTL SHALL be automatically cleaned up by a background goroutine.

#### Scenario: TTL expiration
- **WHEN** a persisted tool result file is older than `tools.result_persistence.ttl_hours` (default 24)
- **THEN** the file SHALL be deleted during the next cleanup sweep

#### Scenario: Startup cleanup
- **WHEN** the IronClaw process starts
- **THEN** it SHALL perform a cleanup sweep of the cache directory, removing files older than the TTL

### Requirement: Cache directory configuration
The cache directory for persisted tool results SHALL be configurable via `tools.result_persistence.cache_dir`, defaulting to `~/.ironclaw/cache/tool-results/`.

#### Scenario: Custom cache directory
- **WHEN** `cache_dir` is set to `/tmp/ironclaw-cache/`
- **THEN** all persisted tool results SHALL be written under that directory

#### Scenario: Default cache directory
- **WHEN** `cache_dir` is empty or not set
- **THEN** persisted results SHALL be written to `~/.ironclaw/cache/tool-results/`

### Requirement: Feature toggle
Tool result persistence SHALL be independently enabled/disabled via `tools.result_persistence.enabled` configuration.

#### Scenario: Feature disabled
- **WHEN** `tools.result_persistence.enabled` is `false`
- **THEN** all tool results SHALL remain inline regardless of size (current behavior preserved)
