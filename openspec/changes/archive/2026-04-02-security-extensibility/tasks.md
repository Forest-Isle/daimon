## 1. Structured Tool Results

- [x] 1.1 Extend `tool.Result` struct in `internal/tool/tool.go` with `Type ResultType`, `FilePath string`, `IsPartial bool`, `Metadata map[string]any` fields
- [x] 1.2 Define `ResultType` string type with constants: `ResultText`, `ResultImage`, `ResultFile`, `ResultReference`
- [x] 1.3 Verify backward compatibility: run full test suite to confirm no regressions from zero-value defaults
- [x] 1.4 Update `http.go` tool to set `Metadata` with `status_code` and `content_type`
- [x] 1.5 Update `file.go` tool to set `Type: ResultFile` and `FilePath` on read operations
- [x] 1.6 Update `browser.go` tool to set appropriate `Type` for its results
- [x] 1.7 Write unit tests for Result struct default values and field behavior

## 2. Tool Capability Interface

- [x] 2.1 Define `CapableTool` optional interface in `internal/tool/tool.go` with `Capabilities() ToolCapabilities` method
- [x] 2.2 Define `ToolCapabilities` struct with `IsReadOnly`, `IsDestructive`, `RequiresNetwork`, `ApprovalMode` fields
- [x] 2.3 Add `getCapabilities(t Tool) ToolCapabilities` helper using type assertion with safe defaults
- [x] 2.4 Implement `CapableTool` on `bash.go` (IsDestructive context-dependent, RequiresNetwork: false)
- [x] 2.5 Implement `CapableTool` on `file.go` (IsReadOnly for reads, IsDestructive for writes)
- [x] 2.6 Implement `CapableTool` on `http.go` (RequiresNetwork: true, IsReadOnly for GET/HEAD)
- [x] 2.7 Implement `CapableTool` on `browser.go` (RequiresNetwork: true, IsReadOnly: true)
- [x] 2.8 Bridge with Phase 1's `ReadOnlyTool`: make `CapableTool` subsume it, add adapter for backward compatibility
- [x] 2.9 Write unit tests for `getCapabilities` with various tool types

## 3. Permission Engine

- [x] 3.1 Add `permissions` config section to `internal/config/config.go` (default action, rules list)
- [x] 3.2 Define `PermissionRule` struct: tool pattern, command/path pattern, action (allow/deny/ask)
- [x] 3.3 Create `internal/tool/permissions.go` with `PermissionEngine` struct
- [x] 3.4 Implement glob-style pattern matching for tool names and command patterns
- [x] 3.5 Implement `path_pattern` matching for file tool operations
- [x] 3.6 Implement rule evaluation: top-to-bottom, first-match-wins, fallback to default
- [x] 3.7 Implement multi-source rule merging: global + project config, project rules prepended
- [x] 3.8 Integrate capability flags into permission decisions (destructive tools default to `ask`)
- [x] 3.9 Add backward-compatible fallback: if no rules configured, use legacy `Policy` blocklist
- [x] 3.10 Integrate `PermissionEngine` into `Runtime`, replacing direct `RequiresApproval()` + `Policy` checks
- [x] 3.11 Write unit tests for glob matching edge cases (wildcards, exact match, empty pattern)
- [x] 3.12 Write unit tests for multi-source rule merging and priority
- [x] 3.13 Write integration test for full permission evaluation flow

## 4. Hook Event System

- [x] 4.1 Create `internal/hook/` package with `hook.go` defining event types and handler interfaces
- [x] 4.2 Define `PreToolUseEvent` struct and `PreToolUseHandler` interface with `PreToolUseResult` (action, reason, modified input)
- [x] 4.3 Define `PostToolUseEvent` struct and `PostToolUseHandler` interface with `PostToolUseResult` (modified result)
- [x] 4.4 Define `OnUserMessageEvent` struct and `OnUserMessageHandler` interface (context injection, message modification)
- [x] 4.5 Define `PreCompactEvent` struct and `PreCompactHandler` interface (message preservation)
- [x] 4.6 Implement `HookManager` with typed handler registration and chain dispatch
- [x] 4.7 Implement chain semantics: first non-passthrough result wins for PreToolUse; all handlers called for PostToolUse
- [x] 4.8 Implement built-in `SafetyAnalyzerHandler` (migrates Policy blocklist logic into hook form)
- [x] 4.9 Implement built-in `AuditLogHandler` for PostToolUse (logs tool name, input, result, duration, decision)
- [x] 4.10 Add `hooks` YAML config section with per-event handler lists
- [x] 4.11 Implement handler factory: resolve `type` string to handler constructor, log warning for unknown types

## 5. Runtime Integration

- [x] 5.1 Wire `HookManager` creation in `internal/gateway/gateway.go` initialization sequence
- [x] 5.2 Inject `HookManager` into `Runtime` struct
- [x] 5.3 Call `PreToolUse` hooks before tool execution in `HandleMessage` (replace current approval+policy logic)
- [x] 5.4 Call `PostToolUse` hooks after tool execution in `HandleMessage`
- [x] 5.5 Call `OnUserMessage` hooks at the start of `HandleMessage` after session retrieval
- [x] 5.6 Call `PreCompact` hooks before compression pipeline (integrates with Phase 1's layered compression)
- [x] 5.7 Apply same hook integration to `handleNonStreaming` path
- [x] 5.8 Write integration test: PreToolUse deny prevents execution
- [x] 5.9 Write integration test: PostToolUse audit logger records events
- [x] 5.10 Write integration test: OnUserMessage injects context into system prompt

## 6. Configuration & Testing

- [x] 6.1 Add all new config sections to `ironclaw.example.yaml` with commented examples
- [x] 6.2 Add default permission rules (deny `rm -rf /`, `DROP TABLE`, `shutdown`) in example config
- [x] 6.3 Run full test suite (`make test`) and fix regressions
- [x] 6.4 Run linter (`make lint`) and fix issues
