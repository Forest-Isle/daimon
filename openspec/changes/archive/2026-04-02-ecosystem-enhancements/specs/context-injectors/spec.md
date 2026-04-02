## ADDED Requirements

### Requirement: Git context injector
The system SHALL provide a built-in `GitContextInjector` that injects the current git branch and working tree status into the system prompt when the user is in a git repository.

#### Scenario: User is in a git repository
- **WHEN** a user message is received and the working directory is inside a git repository
- **THEN** the system prompt SHALL include a "## Environment Context" section with the current branch name and `git status --short` output

#### Scenario: User is not in a git repository
- **WHEN** a user message is received and the working directory is not inside a git repository
- **THEN** the git context injector SHALL return empty string and SHALL NOT add any section to the system prompt

#### Scenario: Git command times out
- **WHEN** the `git` command does not complete within the configured timeout (default 2000ms)
- **THEN** the injector SHALL return empty string, log a warning, and SHALL NOT block the agent loop

### Requirement: Working directory context injector
The system SHALL provide a built-in `WorkdirContextInjector` that injects the current working directory path and optionally a file listing into the system prompt.

#### Scenario: Basic directory context
- **WHEN** a user message is received and the workdir injector is enabled
- **THEN** the system prompt SHALL include the absolute path of the current working directory

#### Scenario: File listing enabled
- **WHEN** `include_ls` is set to `true` in the injector config
- **THEN** the injector SHALL include a list of up to `max_files` (default 20) files in the current directory, sorted by modification time

### Requirement: Context injectors as OnUserMessage handlers
Context injectors SHALL be implemented as `OnUserMessageHandler` implementations from the Phase 2 hook system. They SHALL return context strings that are appended to the system prompt.

#### Scenario: Multiple injectors configured
- **WHEN** both git and workdir context injectors are enabled
- **THEN** both SHALL execute independently and their outputs SHALL be concatenated in the "## Environment Context" section

#### Scenario: Injector disabled
- **WHEN** no context injector handlers are configured in `hooks.on_user_message`
- **THEN** no environment context SHALL be injected (current behavior preserved)

### Requirement: Injector configuration
Each context injector SHALL be configurable via YAML under `hooks.on_user_message` with type-specific config options.

#### Scenario: Custom git timeout
- **WHEN** `{type: git_context, config: {timeout_ms: 5000}}` is configured
- **THEN** the git injector SHALL wait up to 5000ms for git commands to complete
