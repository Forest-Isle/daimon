## ADDED Requirements

### Requirement: Configurable permission rules with glob matching
The permission system SHALL evaluate tool execution requests against a list of rules defined in YAML configuration. Each rule SHALL specify a tool name (or `*` wildcard), a pattern (glob-style), and an action (`allow`, `deny`, `ask`). Rules SHALL be evaluated top-to-bottom with first-match-wins semantics.

#### Scenario: Matching allow rule
- **WHEN** a bash tool call with command `git commit -m "fix"` is received and a rule `{tool: bash, pattern: "git *", action: allow}` exists
- **THEN** the tool SHALL execute without requesting user approval

#### Scenario: Matching deny rule
- **WHEN** a bash tool call with command `rm -rf /home` is received and a rule `{tool: bash, pattern: "rm -rf *", action: deny}` exists
- **THEN** the tool SHALL be blocked and a "denied by permission rule" result SHALL be returned

#### Scenario: No matching rule — default action
- **WHEN** a tool call matches no configured rule and `permissions.default` is set to `ask`
- **THEN** the system SHALL invoke the approval flow (same as current `RequiresApproval` behavior)

#### Scenario: Wildcard tool match
- **WHEN** a rule `{tool: "*", action: ask}` exists as the last rule
- **THEN** it SHALL match any tool that was not matched by earlier rules

### Requirement: Tool capability flags via optional interface
The tool system SHALL support a `CapableTool` optional interface that tools MAY implement to declare their capabilities. Capabilities SHALL include `IsReadOnly`, `IsDestructive`, `RequiresNetwork`, and `ApprovalMode`.

#### Scenario: Tool declares capabilities
- **WHEN** a tool implements `CapableTool` and returns `IsDestructive: true`
- **THEN** the permission engine SHALL factor this into rule evaluation (destructive tools default to `ask` if no explicit rule matches)

#### Scenario: Tool without capabilities
- **WHEN** a tool does not implement `CapableTool`
- **THEN** the system SHALL use safe defaults: `IsReadOnly: false`, `IsDestructive: false`, `ApprovalMode: "auto"`

### Requirement: Multi-source rule merging
Permission rules SHALL be loaded from multiple sources in priority order: (1) runtime/session overrides (highest), (2) project config (`configs/ironclaw.yaml`), (3) global user config (`~/.IronClaw/config.yaml`). Rules from higher-priority sources SHALL be prepended to the rule list.

#### Scenario: Project rule overrides global rule
- **WHEN** global config has `{tool: bash, pattern: "docker *", action: deny}` and project config has `{tool: bash, pattern: "docker build *", action: allow}`
- **THEN** `docker build myapp` SHALL be allowed (project rule evaluated first) while `docker rm container` SHALL be denied (falls through to global rule)

### Requirement: Backward compatibility with legacy Policy
When no `permissions.rules` are configured, the system SHALL fall back to the existing `Policy` blocklist behavior using `strings.Contains` matching.

#### Scenario: No rules configured
- **WHEN** the `permissions` section is absent from config and `tools.blocked_commands` is set
- **THEN** the system SHALL use the legacy `Policy.CheckBashCommand` behavior

### Requirement: File path pattern matching for file tools
Permission rules for file-related tools SHALL support a `path_pattern` field that matches against the file path argument using glob patterns.

#### Scenario: Path-based deny rule
- **WHEN** a `file_write` tool call targets `/etc/passwd` and a rule `{tool: file_write, path_pattern: "/etc/*", action: deny}` exists
- **THEN** the tool SHALL be blocked
