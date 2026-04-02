## ADDED Requirements

### Requirement: Hook manager with typed event handlers
The system SHALL provide a `HookManager` that dispatches lifecycle events to registered handlers. Each event type SHALL have a dedicated Go interface. Handlers SHALL be registered at gateway initialization time based on YAML configuration.

#### Scenario: PreToolUse handler denies execution
- **WHEN** a `PreToolUseHandler` returns `action: "deny"` with a reason
- **THEN** the tool SHALL NOT execute and the deny reason SHALL be returned as the tool result

#### Scenario: Multiple handlers for same event
- **WHEN** two `PreToolUseHandler` implementations are registered
- **THEN** they SHALL execute in registration order, and the first non-passthrough result SHALL take effect

#### Scenario: No handlers registered
- **WHEN** no handlers are registered for an event type
- **THEN** the system SHALL proceed as if the event was not fired (zero overhead)

### Requirement: PreToolUse event
The system SHALL fire a `PreToolUse` event before every tool execution. Handlers SHALL be able to: allow the execution, deny it with a reason, request user approval, or modify the tool input.

#### Scenario: Handler modifies tool input
- **WHEN** a `PreToolUseHandler` returns `action: "allow"` with a modified input
- **THEN** the tool SHALL execute with the modified input instead of the original

#### Scenario: Handler passthrough
- **WHEN** a `PreToolUseHandler` returns `action: "passthrough"`
- **THEN** the next handler in the chain SHALL be called, or the default behavior if no more handlers

### Requirement: PostToolUse event
The system SHALL fire a `PostToolUse` event after every tool execution completes. Handlers SHALL receive the tool name, input, result, duration, and status. Handlers SHALL be able to transform the result or perform side effects (logging, alerting).

#### Scenario: Audit logging handler
- **WHEN** a `PostToolUseHandler` for audit logging is registered
- **THEN** it SHALL be called after every tool execution with the full execution context

#### Scenario: Result transformation
- **WHEN** a `PostToolUseHandler` returns a modified result
- **THEN** the modified result SHALL be used in place of the original for session storage

### Requirement: OnUserMessage event
The system SHALL fire an `OnUserMessage` event after receiving a user message and before starting the agent loop. Handlers SHALL be able to inject additional context into the system prompt or modify the user message.

#### Scenario: Context injection
- **WHEN** an `OnUserMessageHandler` returns context strings (e.g., git status)
- **THEN** the injected context SHALL be appended to the system prompt for this agent loop iteration

### Requirement: PreCompact event
The system SHALL fire a `PreCompact` event before context compression begins. Handlers SHALL be able to mark specific messages or content as "must preserve" to prevent them from being compressed or dropped.

#### Scenario: Preserving critical context
- **WHEN** a `PreCompactHandler` marks a message as "must preserve"
- **THEN** the compression pipeline SHALL NOT summarize, truncate, or drop that message

### Requirement: YAML-configurable handler registration
Hook handlers SHALL be configurable via YAML. Each handler entry SHALL specify a `type` (matching a built-in handler name) and an optional `config` map for handler-specific settings.

#### Scenario: Configuring handlers
- **WHEN** the config contains `hooks.pre_tool_use: [{type: safety_analyzer, config: {block_patterns: ["rm -rf /"]}}]`
- **THEN** the `SafetyAnalyzer` handler SHALL be registered for `PreToolUse` events with the specified block patterns

#### Scenario: Unknown handler type
- **WHEN** the config references a handler type that is not registered in the system
- **THEN** the system SHALL log a warning at startup and skip that handler (non-fatal)
