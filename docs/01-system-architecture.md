# 01. System Architecture

IronClaw is organized around one explicit composition root: `internal/gateway.Gateway`. The CLI loads config, applies user-directory overlays, constructs the Gateway, adds one or more channels, and starts lifecycle-managed subsystems.

## Top-Level Flow

```mermaid
sequenceDiagram
    participant User
    participant CLI as cmd/ironclaw
    participant Config as internal/config + userdir
    participant Gateway as internal/gateway
    participant Channel as internal/channel
    participant Agent as internal/agent
    participant Tools as internal/tool
    participant Store as internal/store

    User->>CLI: ironclaw start/tui/etc.
    CLI->>Config: Load YAML, expand env, apply overlays
    CLI->>Config: userdir.Apply(~/.IronClaw)
    CLI->>Gateway: New(cfg, options)
    Gateway->>Store: Open SQLite and run migrations
    Gateway->>Gateway: Register features and init subsystems
    CLI->>Gateway: AddChannel(...)
    CLI->>Gateway: Start(ctx)
    Channel->>Gateway: InboundMessage
    Gateway->>Agent: HandleMessage
    Agent->>Tools: Execute tool calls through interceptor chain
    Agent->>Store: Persist sessions/messages/tool logs
    Agent-->>Channel: Outbound response
```

## Package Layers

```mermaid
flowchart TB
    subgraph Entry
        CMD[cmd/ironclaw]
        Web[web/studio]
    end

    subgraph Composition
        Gateway[internal/gateway]
        Feature[internal/feature]
        Config[internal/config]
        UserDir[internal/userdir]
    end

    subgraph Runtime
        Agent[internal/agent]
        Tool[internal/tool]
        Channel[internal/channel]
        Memory[internal/memory]
        Evolution[internal/evolution]
    end

    subgraph Infrastructure
        Store[internal/store]
        Session[internal/session]
        TaskLedger[internal/taskledger]
        Scheduler[internal/scheduler]
        Observability[internal/observability]
        Sandbox[internal/sandbox]
        Hook[internal/hook]
        MCP[internal/mcp]
        Worktree[internal/worktree]
    end

    CMD --> Config --> Gateway
    CMD --> UserDir --> Gateway
    Gateway --> Feature
    Gateway --> Runtime
    Gateway --> Infrastructure
    Web --> Gateway
```

## Runtime Responsibilities

### CLI

`cmd/ironclaw` owns user-facing commands. It does not directly implement agent behavior. Its job is to load config, apply userdir, choose a channel or command mode, and call Gateway or package-level services.

### Gateway

Gateway owns cross-module wiring:

- Database and session manager.
- Feature Registry and persisted feature overrides.
- Tool registry, hooks, permission engine, sandbox, verification, audit.
- LLM provider and `AgentDeps`.
- Memory, Skills, Agents, Teams.
- Scheduler, task ledger, health server, metrics, config reload.

### Agent

`internal/agent` receives normalized channel messages. It builds the system prompt from base prompt, userdir persona/rules, memories, profile sections, skills, and agent specs. It executes either SimpleLoop or UnifiedLoop, persists session state, emits events, and optionally forwards events to evolution.

### Tools

Tools are plain implementations of `tool.Tool`. Runtime concerns such as approval, user hooks, sandbox policy, post-edit verification, and audit are added by the interceptor chain rather than hidden inside every tool.

### Memory

Memory stores persistent user/session facts in files with a SQLite-backed index and optional embeddings. A unified retriever fuses the memory store and procedural memory into a single retrieval surface.

### Channels

Channels normalize Telegram, Discord, TUI, scheduler, and subprocess inputs into `channel.InboundMessage`. Optional channel interfaces add approval prompts, reflection prompts, feedback, notifications, and live tool-output streaming.

### State and Observability

SQLite stores sessions, messages, tool logs, memory indexes, task ledger state, execution events, and replay data. The observability package exposes OpenTelemetry traces and metrics, and cognitive metrics are surfaced to the TUI status bar.

## Data Flow for a User Message

```mermaid
flowchart LR
    Inbound[InboundMessage] --> RateLimit[Rate limiter]
    RateLimit --> Command{Slash command?}
    Command -- yes --> CommandTable[Gateway command table]
    Command -- no --> Agent[Agent.HandleMessage]
    Agent --> Session[Get/Create session]
    Agent --> Prompt[Build system prompt]
    Prompt --> Compress[Context compression]
    Compress --> LLM[LLM completion]
    LLM --> ToolCalls{Tool calls?}
    ToolCalls -- no --> Persist[Persist session and memory]
    ToolCalls -- yes --> Interceptors[Tool interceptor chain]
    Interceptors --> ToolExec[Tool implementation]
    ToolExec --> LLM
    Persist --> Response[Channel response]
```

## Source-of-Truth Files

| Concern | Primary files |
|---|---|
| CLI commands | `cmd/ironclaw/*.go` |
| Gateway lifecycle | `internal/gateway/gateway.go`, `internal/gateway/init_*.go` |
| Feature defaults | `internal/gateway/features.go` |
| Config defaults and structs | `internal/config/*.go`, `configs/ironclaw.example.yaml` |
| Agent loops | `internal/agent/simple_loop.go`, `internal/agent/unified_loop.go`, `internal/agent/loop_common.go` |
| Tool registry/interceptors | `internal/gateway/init_tools.go`, `internal/tool/*.go` |
| Memory | `internal/gateway/init_memory.go`, `internal/memory/*.go` |
| Store | `internal/store/sqlite.go`, `internal/store/migrations/*.sql` |
