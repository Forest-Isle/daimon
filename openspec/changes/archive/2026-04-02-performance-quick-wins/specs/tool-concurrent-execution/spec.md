## ADDED Requirements

### Requirement: Read-only tool identification via optional interface
The tool system SHALL support an optional `ReadOnlyTool` interface that tools MAY implement to declare themselves as read-only (no side effects). Tools that do not implement this interface SHALL be treated as write-capable by default.

#### Scenario: Tool implements ReadOnlyTool interface
- **WHEN** a tool implements `ReadOnlyTool` and returns `true` from `IsReadOnly()`
- **THEN** the tool registry SHALL identify it as eligible for concurrent execution

#### Scenario: Tool does not implement ReadOnlyTool interface
- **WHEN** a tool does not implement the `ReadOnlyTool` interface
- **THEN** the system SHALL treat it as write-capable and execute it sequentially

### Requirement: Concurrent execution of read-only tools
When the LLM returns multiple tool calls in a single response, all tools identified as read-only SHALL execute concurrently, bounded by a configurable maximum concurrency limit. Write-capable tools SHALL execute sequentially only after all concurrent read-only tools have completed.

#### Scenario: Multiple read-only tools in one response
- **WHEN** the LLM returns 3 tool calls: `file_read(a)`, `grep(b)`, `file_read(c)` and all implement `ReadOnlyTool` returning `true`
- **THEN** all 3 tools SHALL execute concurrently (up to `max_concurrency` limit) and their results SHALL be collected before proceeding

#### Scenario: Mix of read-only and write tools
- **WHEN** the LLM returns tool calls: `file_read(a)`, `bash_write(b)`, `grep(c)`
- **THEN** `file_read(a)` and `grep(c)` SHALL execute concurrently first, and `bash_write(b)` SHALL execute only after both complete

#### Scenario: Single tool call
- **WHEN** the LLM returns exactly one tool call
- **THEN** the tool SHALL execute directly without concurrency overhead

#### Scenario: All write tools
- **WHEN** the LLM returns multiple tool calls and none implement `ReadOnlyTool` or all return `false`
- **THEN** all tools SHALL execute sequentially in the order returned by the LLM

### Requirement: Concurrency limit configuration
The maximum number of concurrent read-only tool executions SHALL be configurable via `tools.concurrent_execution.max_concurrency` in the YAML config, with a default value of 4.

#### Scenario: Concurrency limit reached
- **WHEN** 6 read-only tools are called concurrently and `max_concurrency` is set to 4
- **THEN** at most 4 tools SHALL execute simultaneously, with remaining tools queued

#### Scenario: Feature disabled
- **WHEN** `tools.concurrent_execution.enabled` is set to `false`
- **THEN** all tools SHALL execute sequentially regardless of their `ReadOnlyTool` status

### Requirement: Tool approval preserved in concurrent mode
Tools that require approval SHALL still go through the approval flow before execution, even when running concurrently. Approval requests SHALL be serialized (one at a time) to avoid confusing the user.

#### Scenario: Read-only tool with approval in concurrent batch
- **WHEN** a read-only tool that requires approval is part of a concurrent batch
- **THEN** the approval request SHALL be sent before that tool executes, and other concurrent tools MAY proceed while awaiting approval
