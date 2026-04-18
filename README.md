# IronClaw

**Local-first AI Agent Runtime, built with Go.**

[中文文档](README_zh.md)

[![CI](https://github.com/Forest-Isle/IronClaw/actions/workflows/ci.yml/badge.svg)](https://github.com/Forest-Isle/IronClaw/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![SQLite](https://img.shields.io/badge/SQLite-Local--first-003B57?logo=sqlite&logoColor=white)](https://www.sqlite.org)
[![Anthropic](https://img.shields.io/badge/Claude-AI_Powered-D97757?logo=anthropic&logoColor=white)](https://www.anthropic.com)

IronClaw is a self-hosted AI agent runtime that runs entirely on your own infrastructure. It connects Claude AI with real-world tools — shell commands, file operations, HTTP requests, browser automation — and exposes them through multiple channels (Telegram, Terminal UI). All data stays local in SQLite and Markdown files.

## Features

- **Dual Agent Modes** — Simple linear loop or Cognitive 5-phase loop (PERCEIVE → PLAN → ACT → OBSERVE → REFLECT) with automatic replanning and confidence tracking
- **Advanced Memory System** — File-based Markdown storage with cognitive science-inspired memory types (episodic/semantic/procedural), importance scoring, forgetting curve integration, automatic reflection (L1 patterns + L2 strategic insights), hierarchical compression (facts → summaries → user profiles), and layered retrieval
- **Knowledge Base** — Document ingestion pipeline (Markdown, code, PDF, text, web) with BM25+vector hybrid retrieval, RRF fusion, and optional LLM reranker
- **Temporal Knowledge Graph** — Entity/relation extraction with time-aware edge versioning, multi-hop recursive CTE traversal, memory-graph bidirectional sync, provenance tracking, and automatic graph decay
- **Privacy Controls** — PII detection (email, phone, SSN, credit card), sensitivity classification (public/private/secret), user-facing memory management tool, configurable retention policies, and audit logging
- **MCP Protocol** — Connect multiple MCP servers with hot-reload, automatic tool discovery and registration
- **Skill System** — Extensible SKILL.md format with built-in ClawHub registry for searching, installing, and managing skills
- **Multi-Channel** — Telegram bot (streaming, inline keyboard approvals) and TUI terminal interface (Bubble Tea + Glamour markdown rendering)
- **HTTP Metrics** — Optional Prometheus-style `/metrics` endpoint exposing active sessions, total tool calls, LLM tokens used, and agent iteration counts (enabled via `http.metrics_enabled`)
- **Reinforcement Learning** — Three-layer RL system: Contextual Bandit (tool selection), PPO (plan strategy), DQN (replan decisions) with full neural network training
- **Tool System** — Built-in tools for Bash, file I/O, HTTP, browser automation, skill execution, and memory management, plus MCP-based dynamic tool discovery
- **Persona & User Directory** — Auto-initialized `~/.IronClaw/` with personality files (Soul.md, Memory.md, Agent.md) and per-user configs
- **Local Storage** — SQLite with WAL mode, 18 embedded migrations, FTS5 full-text search (graceful degradation to LIKE)
- **Task Scheduler** — Cron-based scheduled tasks with database-backed persistence
- **Tool Approval** — Configurable per-tool approval via Telegram inline keyboard or TUI interactive prompts
- **Structured Verification** — Auto-generated assertions per tool type (bash exit codes, HTTP status codes, file ops), with typed failure contexts fed into the REFLECT phase for targeted replanning
- **Task Checkpoints** — Interrupted cognitive tasks auto-save state to SQLite; `/resume` slash command restores execution from the last completed subtask
- **Smart Retry** — Failure context (error type, attempt count, per-assertion details) injected into REFLECT prompts; tiered degradation warnings after repeated failures
- **Browser Tools** — `browser_search` (structured search results) and `browser_extract` (HTML-to-Markdown with pagination) for web-aware tasks
- **Tool Result Cache** — Per-task in-memory cache for read-only tool results with SHA256 keying and automatic path-based invalidation on writes
- **Project & Git Context** — Auto-detected project type (Go/Node/Rust/Python), build commands, README, and git state (branch, uncommitted files, recent commits) injected into PLAN prompts
- **Dynamic Context Budget** — Complexity-aware allocation of memories, KB chunks, graph context, and project/git info to prevent token waste on simple tasks

## Architecture

```
┌─────────────┐  ┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│  Telegram    │  │  TUI        │────▶│   Gateway    │────▶│   Agent         │
│  Channel     │  │  Channel    │◀────│   (Router)   │◀────│ Simple/Cognitive│
└─────────────┘  └─────────────┘     └──────┬───────┘     └──────┬──────────┘
                                            │                     │
                                     ┌──────┴──────┐        ┌─────┴──────┐
                                     │  HTTP API   │        │   Tools    │
                                     │  (optional) │        │ bash/file/ │
                                     └─────────────┘        │ http/mcp   │
                                                            └─────┬──────┘
                                                                  │
┌─────────────┐  ┌─────────────┐  ┌───────────────────────────────┴───────┐
│  Scheduler  │  │   Skills    │  │            Store (SQLite)              │
│  (cron)     │  │  (ClawHub)  │  ├──────────────┬────────────────────────┤
└─────────────┘  └─────────────┘  │   Memory     │   Knowledge Base      │
                                  │ File-first   │   (BM25 + vector)     │
┌─────────────┐  ┌═════════════╗  │ MD + SQLite  ├────────────────────────┤
│  User Dir   │  ║  RL Engine  ║  │ index        │   Knowledge Graph     │
│(~/.IronClaw)│  ║ Bandit/PPO/ ║  ├──────────────┤   (temporal edges,    │
└─────────────┘  ║ DQN + train ║  │  Reflector   │    provenance)        │
                 ╚═════════════╝  │  Compactor   ├────────────────────────┤
                                  │  Profiler    │   Privacy & Audit     │
                                  └──────────────┴────────────────────────┘
```

## Quick Start

### From Source

```bash
# Clone
git clone https://github.com/Forest-Isle/IronClaw.git
cd ironclaw

# Copy and edit config
cp configs/ironclaw.example.yaml configs/ironclaw.yaml
# Fill in your ANTHROPIC_API_KEY and TELEGRAM_BOT_TOKEN
vim configs/ironclaw.yaml

# Build (requires CGO for SQLite)
make build

# Run with Telegram channel
./bin/ironclaw start

# Or run with Terminal UI
./bin/ironclaw tui
```

### Docker

```bash
# Copy and edit config
cp configs/ironclaw.example.yaml configs/ironclaw.yaml

# Run with Docker Compose
docker compose up -d
```

### Pre-built Binaries

Download from the [Releases](https://github.com/Forest-Isle/IronClaw/releases) page.

```bash
# Download (example for Linux amd64)
curl -LO https://github.com/Forest-Isle/IronClaw/releases/latest/download/ironclaw_linux_amd64.tar.gz
tar xzf ironclaw_linux_amd64.tar.gz

# Copy and edit config
cp configs/ironclaw.example.yaml configs/ironclaw.yaml

# Run
./ironclaw start
```

## Configuration

IronClaw uses a YAML config file with environment variable expansion (`${VAR_NAME}`). See [`configs/ironclaw.example.yaml`](configs/ironclaw.example.yaml) for all options.

| Section | Description |
|---------|-------------|
| `llm` | AI provider config (API key, model, max tokens) |
| `telegram` | Bot token and allowed user IDs |
| `tui` | Terminal UI settings (auto_approve mode) |
| `agent` | Mode (simple/cognitive), max iterations, RL config |
| `store` | SQLite database path |
| `memory` | Storage dir, fact extraction, similarity threshold, reflection/compaction thresholds, retention policies |
| `knowledge` | Document ingestion dirs, chunk size, hybrid retrieval, reranker, graph |
| `skills` | Enable/disable, extra skill directories |
| `scheduler` | Cron task scheduler |
| `tools` | Per-tool enable/disable, timeouts, approval settings, MCP servers |
| `server` | Optional HTTP API endpoint |
| `http.metrics_enabled` | Enable Prometheus-style `/metrics` endpoint (default: `false`) |
| `log` | Log level and format |

## Memory System

IronClaw uses a **file-first memory architecture** inspired by cognitive science, with five layers of memory processing:

```
Layer 0: Working Context (current conversation)
    ↓ fact extraction
Layer 1: Session Facts (episodic/semantic/procedural, with importance & emotion)
    ↓ consolidation (24h, strength ≥ 0.5)
Layer 2: User Facts (promoted from session)
    ↓ compaction (same category ≥ 8 facts)
Layer 3: Summaries (LLM-merged structured summaries)
    ↓ reflection (L1 patterns → L2 strategic insights)
Layer 4: User Profile (identity, preferences, current focus)
```

### Memory Types

| Type | Decay Rate | Description |
|------|-----------|-------------|
| **Episodic** | Fast (12h × importance) | Time-bound events and specific experiences |
| **Semantic** | Standard (24h × importance) | General knowledge, preferences, stable facts |
| **Procedural** | Slow (48h × importance) | Behavioral patterns, workflows — strengthens with use |

### Storage Structure

```
~/.ironclaw/memory/
├── MEMORY.md              # Index of all active memories
├── user/                  # Long-term memories + summaries + profiles
├── session/               # Session-scoped temporary memories
├── feedback/              # User corrections
├── global/                # Cross-user system memories
└── archived/              # Auto-archived low-strength memories
```

Each memory file uses YAML frontmatter:

```markdown
---
id: abc123
scope: user
type: semantic
importance: 7
emotion: neutral
sensitivity: public
strength: 0.85
created_at: 2026-03-28T10:00:00Z
---

User prefers concise responses without verbose explanations.
```

### Key Mechanisms

- **Hybrid Search**: BM25 (FTS5) + vector (cosine similarity) with RRF fusion and strength weighting
- **Forgetting Curve**: Ebbinghaus-based decay `R(t) = e^(-t/S)` with type-dependent stability and access bonuses
- **Lifecycle Management**: LLM-driven ADD/UPDATE/DELETE/NOOP decisions with conflict detection (mem0-style)
- **Reflection**: Hybrid trigger (count ≥ 10 OR topic drift cosine < 0.7) producing multi-level insights
- **Privacy**: Auto PII detection, sensitivity classification, user-facing `memory_manage` tool for selective forgetting
- **Graph Sync**: Memory lifecycle events automatically sync to knowledge graph (entity extraction, provenance, edge weakening)

### Migration from Legacy Storage

```bash
ironclaw memory migrate            # Migrate SQLite → file storage
ironclaw memory migrate --dry-run  # Preview only
ironclaw memory restore            # Restore from backup
```

## Knowledge Graph

The temporal knowledge graph tracks entity relationships with version history:

- **Temporal Edges**: `valid_from`/`valid_to` timestamps enable point-in-time queries and relationship versioning
- **Memory Sync**: Memory ADD → entity extraction; UPDATE → provenance migration; DELETE → edge weakening (not deletion)
- **Graph Decay**: Background task cleans orphaned provenance, decays unsupported edges, removes dead edges
- **Multi-hop Traversal**: Recursive CTE with temporal predicates for current-state and historical queries
- **Graph-Boosted Retrieval**: Memory search results enriched by graph connectivity scoring

## Channels

### Telegram

Full-featured Telegram bot with streaming message updates, inline keyboard for tool approvals and replan decisions, and user-level access control.

### Terminal UI (TUI)

Interactive terminal interface built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) and [Glamour](https://github.com/charmbracelet/glamour) for rich markdown rendering.

```bash
ironclaw tui                # Start interactive TUI
ironclaw tui --auto-approve # Auto-approve all tool calls
```

## Performance

Measured on an Apple M2 Pro with the default SQLite configuration and a single active session:

| Operation | p50 | p99 |
|-----------|-----|-----|
| Tool call dispatch (bash/file/http) | ~3ms | ~10ms |
| LLM round trip (Claude API, streaming) | ~20ms | ~50ms |
| Memory hybrid search (FTS5 + vector, 10k facts) | ~5ms | ~15ms |
| Knowledge base retrieval (BM25 + vector, 1k chunks) | ~8ms | ~25ms |

These numbers reflect end-to-end latency from the agent's tool invocation to the result being written back into context. Network latency to the Claude API is the dominant factor for LLM round trips.

## HTTP Metrics

IronClaw exposes a Prometheus-compatible `/metrics` endpoint via the optional HTTP gateway. Enable it in your config:

```yaml
http:
  metrics_enabled: true
```

The endpoint (`GET /metrics`) returns counters in Prometheus text format:

| Metric | Description |
|--------|-------------|
| `ironclaw_active_sessions` | Number of currently active sessions |
| `ironclaw_tool_calls_total` | Cumulative tool call count |
| `ironclaw_llm_tokens_total` | Cumulative LLM tokens consumed |
| `ironclaw_agent_iterations_total` | Cumulative agent iteration count |

The handler is implemented in `internal/gateway/metrics.go`. The endpoint is disabled by default (`http.metrics_enabled: false`).

## Skill Management

IronClaw supports extensible skills via SKILL.md files and the [ClawHub](https://clawhub.ai) registry.

```bash
ironclaw skill list              # List installed skills
ironclaw skill search "web"      # Search ClawHub
ironclaw skill install <slug>    # Install a skill
ironclaw skill update            # Update all skills
ironclaw skill remove <name>     # Remove a skill
```

## User Directory

On first run, IronClaw initializes `~/.IronClaw/` with:

- `Soul.md` — Agent personality and communication style
- `Memory.md` — Persistent rules and preferences
- `Agent.md` — Core system prompt template
- `config.yaml` — User overlay configuration
- `skills/` — User-installed skills
- `mcp/` — MCP server configurations (YAML, hot-reloaded)
- `memory/` — Long-term memory (Markdown + SQLite index)

## Development

```bash
make build          # Build binary (CGO_ENABLED=1, -tags fts5)
make test           # Run all tests
make lint           # Run golangci-lint
make fmt            # Format code (goimports + go fmt)
make docker         # Build Docker image
make help           # Show all targets
```

Single test:

```bash
CGO_ENABLED=1 go test -tags "fts5" -run TestName ./internal/package/ -v
```

> **Note**: `CGO_ENABLED=1` and `-tags fts5` are required for all build/test commands — SQLite uses cgo and FTS5 must be enabled at compile time.

## Roadmap

- [ ] Multi-provider LLM support (OpenAI, local models)
- [ ] Web UI dashboard
- [ ] Discord / Slack channel adapters
- [ ] Multi-agent collaboration
- [ ] Webhook triggers
- [x] ~~Plugin system for custom tools~~ (Skill System + MCP)
- [x] ~~RAG with document ingestion~~ (Knowledge Base + Knowledge Graph)
- [x] ~~Terminal UI~~ (Bubble Tea TUI Channel)
- [x] ~~Advanced memory~~ (Type taxonomy, reflection, compression, privacy)

## Troubleshooting

### SQLite "database is locked" errors

IronClaw opens SQLite in WAL mode, which allows concurrent reads. If you see lock errors, ensure only one `ironclaw` process is running against the same `data/ironclaw.db` file. Docker and bare-metal instances must not share the same database path.

### FTS5 not available

If full-text search silently degrades to LIKE queries, your SQLite build was compiled without FTS5. Rebuild with `CGO_ENABLED=1 go build -tags fts5` or install a pre-built binary from the Releases page.

### Telegram bot not responding

1. Verify `TELEGRAM_BOT_TOKEN` is set correctly in `configs/ironclaw.yaml` or as an environment variable.
2. Check that your user ID appears in the `telegram.allowed_user_ids` list.
3. Run `ironclaw start --log-level debug` to see raw webhook events.

### LLM calls returning 401 / authentication errors

Ensure `ANTHROPIC_API_KEY` is exported in your shell or set via the config file. The key must have access to the model specified in `llm.model`.

## Contributing

Contributions are welcome! Please read the [Contributing Guide](CONTRIBUTING.md) before submitting a PR.

## License

[MIT](LICENSE)
