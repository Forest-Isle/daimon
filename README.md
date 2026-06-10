# IronClaw

IronClaw is a local-first AI agent runtime written in Go. It wires LLM providers, channels, tools, memory, sub-agents, scheduling, and observability behind one Gateway.

The codebase is a Go 1.25.9 project.

## Architecture

```mermaid
flowchart LR
    User[User / Scheduler] --> Channel[Channel adapters]
    Channel --> Gateway[Gateway]
    Gateway --> Agent[Agent runtime]
    Agent --> Provider[Claude or OpenAI-compatible provider]
    Agent --> Tools[Tool registry]
    Agent --> Memory[Memory system]
    Agent --> Agents[Sub-agents]
    Tools --> Sandbox[Permission / Hook / Sandbox / Verify / Audit chain]
    Gateway --> Store[(SQLite WAL store)]
```

## Main Modules

| Area | Packages | Responsibility |
|---|---|---|
| CLI | `cmd/ironclaw` | Cobra entry points: `start`, `tui`, `skill`, `memory`, `mcp`. |
| Gateway | `internal/gateway` | Central composition root, feature registry, subsystem lifecycle, slash command dispatch. |
| Agent | `internal/agent` | LLM loop strategies, provider adapters, context compression, tool execution, sub-agent orchestration. |
| Tools | `internal/tool`, `internal/worktree` | Built-in tools, MCP adapters, worktree tools, permission and sandbox interceptor chain. |
| Memory | `internal/memory` | File memory, embeddings, lifecycle, unified retrieval. |
| Channels | `internal/channel/*` | Telegram, TUI, approval prompts, reflection prompts, feedback, streaming output. |
| State | `internal/store`, `internal/session`, `internal/taskledger`, `internal/scheduler` | SQLite migrations, sessions/messages, task ledger, stale detection, scheduled tasks. |
| Observability | `internal/observability` | OpenTelemetry tracing and metrics. |
| Security | `internal/sandbox`, `internal/hook` | Docker/host isolation, file/network policy, user hooks, safety checks. |

## Quick Start

```bash
cp configs/ironclaw.example.yaml configs/ironclaw.yaml
make build
./bin/ironclaw version
./bin/ironclaw tui -c configs/ironclaw.yaml
```

For a Go-only CI build:

```bash
make build-bin
make vet
make test-short
```

For full verification:

```bash
make test
```

`make test` uses `CGO_ENABLED=1`, the `fts5` build tag, and the Go race detector.

## Configuration

The example configuration lives at `configs/ironclaw.example.yaml`. Runtime loading uses this order:

1. Built-in defaults from `internal/config`.
2. The config file: the explicit YAML passed with `-c`, or `~/.ironclaw/config.yaml` by default (`configs/ironclaw.yaml` with `--dev`).
3. User directory injection from `~/.ironclaw`: `Soul.md`, `Memory.md`, `Agent.md`, MCP server files, skills, and agent specs.
4. Persisted runtime feature overrides from `~/.ironclaw/feature_state.json`, unless the caller opts out.

Most core runtime features are on by default; the standalone admin server is opt-in.

## License

See [LICENSE](LICENSE).
