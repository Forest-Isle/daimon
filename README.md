# IronClaw

**Self-evolving Cognitive Agent Runtime — local-first, built with Go.**

[中文文档](README_zh.md)

[![CI](https://github.com/Forest-Isle/IronClaw/actions/workflows/ci.yml/badge.svg)](https://github.com/Forest-Isle/IronClaw/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![SQLite](https://img.shields.io/badge/SQLite-Local--first-003B57?logo=sqlite&logoColor=white)](https://www.sqlite.org)
[![Anthropic](https://img.shields.io/badge/Claude-AI_Powered-D97757?logo=anthropic&logoColor=white)](https://www.anthropic.com)

IronClaw is a self-hosted AI agent runtime that **gets better at its job over time**. It runs entirely on your own infrastructure, connecting Claude AI with real-world tools — shell commands, file operations, HTTP requests, browser automation — through multiple channels (Telegram, Terminal UI). All data stays local in SQLite and Markdown files.

**What makes IronClaw different:**
- **Cognitive depth, not just tool breadth** — A 5-phase cognitive loop (PERCEIVE → PLAN → ACT → OBSERVE → REFLECT) with structured verification, DAG-based parallel execution, and confidence-calibrated replanning. Not a simple prompt-response loop.
- **Self-evolution** — Three closed-loop feedback systems learn from every interaction: preference learning, skill synthesis from recurring patterns, and strategy optimization with hard control that directly tunes agent behavior based on measured outcomes.
- **Science-inspired memory** — File-based Markdown storage with episodic/semantic/procedural memory types, Ebbinghaus forgetting curves, multi-section user profiling, and automatic consolidation. Your agent's memory is inspectable, git-friendly, and backed by hybrid FTS5+vector search.
- **Multi-provider LLM support** — Claude, OpenAI (GPT-4o), Ollama, vLLM, LiteLLM, OpenRouter — any OpenAI-compatible endpoint works out of the box with zero SDK dependency.
- **Single binary, zero runtime dependencies** — One Go binary with embedded SQLite and web dashboard. No Python, no npm, no Docker required. Deploy and run.

## Features

- **Dual Agent Modes** — Simple linear loop or Cognitive 5-phase loop (PERCEIVE → PLAN → ACT → OBSERVE → REFLECT) with automatic replanning and confidence tracking
- **Multi-Provider LLM** — Claude (default), OpenAI (GPT-4o), and any OpenAI-compatible endpoint (Ollama, vLLM, LiteLLM, OpenRouter) via pure `net/http` — zero SDK dependency
- **Sub-Agent Orchestration** — `SubAgentManager` for context-isolated sub-agent lifecycle: per-invocation sessions, scoped tool registries, model routing, `SpawnParallel` with `best_effort`/`fail_fast` strategies, declarative Markdown agent specs (`.ironclaw/agents/*.md`)
- **Agent Teams** — `/team <goal>` slash command for multi-agent parallel task execution with LLM-driven task decomposition, dependency DAG scheduling, and worker pool
- **Web Dashboard** — Real-time agent monitoring via embedded Preact SPA — WebSocket live event streaming, 5-phase timeline visualization, tool call feed, session tracking; `go:embed` single-binary deployment
- **Security Sandbox** — Interceptor chain architecture for tool execution: 4-level permissions (`none`/`notify`/`approve`/`deny`), Docker session containers per agent session, `FileGuard` path validation with symlink protection, `NetworkPolicy` with built-in SSRF protection
- **Advanced Memory System** — File-based Markdown storage with cognitive science-inspired memory types (episodic/semantic/procedural), importance scoring, forgetting curve integration, automatic reflection (L1 patterns + L2 strategic insights), hierarchical compression (facts → summaries → user profiles), and layered retrieval
- **User Profile Modeling** — Multi-section user profiles (`communication`, `tech_stack`, `work_pattern`, `projects`, `feedback`, `identity`) with two-layer fact routing, buffered incremental updates, cold-start learning, and priority-sorted system prompt injection
- **Knowledge Base** — Document ingestion pipeline (Markdown, code, PDF, text, web) with BM25+vector hybrid retrieval, RRF fusion, and optional LLM reranker
- **Temporal Knowledge Graph** — Entity/relation extraction with time-aware edge versioning, multi-hop recursive CTE traversal, memory-graph bidirectional sync, provenance tracking, and automatic graph decay
- **Privacy Controls** — PII detection (email, phone, SSN, credit card), sensitivity classification (public/private/secret), user-facing memory management tool, configurable retention policies, and audit logging
- **Context Compression** — 5-layer progressive compression pipeline (tool pruning → eviction → summarization → removal → emergency truncation), reactive 413 auto-retry, system prompt cache boundary split for Anthropic Prompt Cache
- **MCP Protocol** — Connect multiple MCP servers with hot-reload, automatic tool discovery and registration
- **Skill System** — Extensible SKILL.md format with built-in ClawHub registry for searching, installing, and managing skills
- **Multi-Channel** — Telegram bot (streaming, inline keyboard approvals) and TUI terminal interface (Bubble Tea + Glamour markdown rendering)
- **HTTP Metrics** — Optional Prometheus-style `/metrics` endpoint exposing active sessions, total tool calls, LLM tokens used, and agent iteration counts (enabled via `http.metrics_enabled`)
- **Reinforcement Learning** — Three-layer RL system: Contextual Bandit (tool selection), PPO (plan strategy), DQN (replan decisions) with full neural network training
- **Self-Evolution** — Three closed-loop feedback systems: preference learning (user feedback → tool priorities), skill synthesis (pattern detection → draft skills), strategy optimization with hard control (directly tunes replan threshold); unified reward formula across online/offline paths
- **Eval Harness** — `ironclaw eval run/compare/longitudinal` CLI for reproducible agent benchmarking with evolution snapshots, live cognitive agent runner, and time-series trend visualization
- **Cognitive Health Metrics** — `ironclaw insights health` CLI with rolling-window metrics: assertion pass rate, replan efficiency, tool reliability, complexity success rate
- **Tool System** — Built-in tools for Bash (structured JSON output), file I/O, HTTP, browser automation (`browser_search` + `browser_extract`), skill execution, and memory management, plus MCP-based dynamic tool discovery
- **Speculative Execution** — Read-only tools pre-executed during LLM streaming for latency reduction
- **Unified Task Ledger** — SQLite task registry for all execution paths with atomic claiming, heartbeat-based stale detection, and recursive dependency tracking
- **Persona & User Directory** — Auto-initialized `~/.IronClaw/` with personality files (Soul.md, Memory.md, Agent.md) and per-user configs
- **Local Storage** — SQLite with WAL mode, 18 embedded migrations, FTS5 full-text search (graceful degradation to LIKE)
- **Task Scheduler** — Cron-based scheduled tasks with database-backed persistence
- **Structured Verification** — Auto-generated assertions for 10+ tool types (bash, HTTP, file ops, browser, MCP, skills, memory) with Observation Metadata channel and typed failure contexts fed into REFLECT for targeted replanning
- **Task Checkpoints** — Interrupted cognitive tasks auto-save state to SQLite; `/resume` slash command restores execution from the last completed subtask
- **Smart Retry** — Failure context (error type, attempt count, per-assertion details) injected into REFLECT prompts; tiered degradation warnings after repeated failures
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
┌─────────────┐                      ┌──────┴──────┐        ┌─────┴──────┐
│  Dashboard  │◀── WebSocket ───────▶│  HTTP API   │        │   Tools    │
│  (Preact)   │    Event Bus         │  + REST     │        │ bash/file/ │
└─────────────┘                      └─────────────┘        │ http/mcp   │
                                                            └─────┬──────┘
                                                                  │
                                                            ┌─────┴──────┐
┌─────────────┐  ┌─────────────┐                            │ Interceptor│
│ SubAgent    │  │ Agent Teams │                            │   Chain    │
│  Manager    │  │ /team cmd   │                            │ perm/hook/ │
│ (isolation) │  │ (workers)   │                            │  sandbox   │
└─────────────┘  └─────────────┘                            └─────┬──────┘
                                                                  │
┌─────────────┐  ┌─────────────┐  ┌───────────────────────────────┴───────┐
│  Scheduler  │  │   Skills    │  │            Store (SQLite)              │
│  (cron)     │  │  (ClawHub)  │  ├──────────────┬────────────────────────┤
└─────────────┘  └─────────────┘  │   Memory     │   Knowledge Base      │
                                  │ File-first   │   (BM25 + vector)     │
┌═════════════╗  ┌═════════════╗  │ MD + SQLite  ├────────────────────────┤
║  Evolution  ║  ║  RL Engine  ║  │ index        │   Knowledge Graph     │
║ Preferences ║  ║ Bandit/PPO/ ║  ├──────────────┤   (temporal edges,    │
║ Skills      ║  ║ DQN + train ║  │  Reflector   │    provenance)        │
║ Strategy    ║  ╚══════╤══════╝  │  Compactor   ├────────────────────────┤
║ Metrics     ║─────────┘         │  Profiler    │   Privacy & Audit     │
╚═════════════╝                   └──────────────┴────────────────────────┘
```

## How It Learns

IronClaw improves through three self-evolution feedback loops that run automatically in the background:

```
                    ┌─── Loop 1: Preference Learning ───┐
                    │   Extract user preferences from    │
                    │   successful reflections            │
                    └──────────────┬─────────────────────┘
                                   │
   Agent Execution ──► Trajectory ─┼─── Loop 2: Skill Synthesis ────┐
   (cognitive loop)    Recording   │   Detect recurring tool         │
                                   │   patterns → generate skills    │
                                   └──────────────┬─────────────────┘
                                                  │
                                   ┌──── Loop 3: Strategy Optimizer ─┐
                                   │   Tune replan threshold and     │
                                   │   tool priorities from outcomes  │
                                   └─────────────────────────────────┘
```

Every 6 hours, an insights cycle reads trajectory data, generates pattern analysis, and feeds recommendations back into the strategy optimizer. The agent's next session benefits from tuned parameters — measurable through `ironclaw eval compare`.

## Cognitive vs Simple Mode

| Aspect | Simple Mode | Cognitive Mode |
|--------|-------------|----------------|
| Loop | Linear: prompt → LLM → tools → repeat | 5-phase: PERCEIVE → PLAN → ACT → OBSERVE → REFLECT |
| Verification | None | Auto-generated assertions per tool type |
| Planning | Single-shot | DAG with dependency validation, parallel batches |
| Error handling | Retry or fail | Structured failure analysis → targeted replan |
| Context | Static prompt | Dynamic budget allocation by complexity |
| Learning | None | Evolution hooks, RL integration, trajectory recording |
| Resume | Not supported | Checkpoint/resume from last completed subtask |

Use simple mode for quick Q&A. Use cognitive mode for multi-step tasks where reliability matters.

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
| `llm` | AI provider config — `provider` (`claude`/`openai`/`openai-compatible`), API key, model, base URL, max tokens |
| `telegram` | Bot token and allowed user IDs |
| `tui` | Terminal UI settings (auto_approve mode) |
| `agent` | Mode (simple/cognitive), max iterations, RL config, compression strategy, team config |
| `store` | SQLite database path |
| `memory` | Storage dir, fact extraction, similarity threshold, reflection/compaction thresholds, retention policies |
| `knowledge` | Document ingestion dirs, chunk size, hybrid retrieval, reranker, graph |
| `skills` | Enable/disable, extra skill directories |
| `scheduler` | Cron task scheduler |
| `tools` | Per-tool enable/disable, timeouts, approval settings, MCP servers |
| `sandbox` | Security sandbox — `enabled`, `allowed_directories`, `readonly_directories`, Docker backend config, network policy |
| `dashboard` | Web Dashboard — `enabled`, `addr`, `token` |
| `server` | Optional HTTP API endpoint |
| `http.metrics_enabled` | Enable Prometheus-style `/metrics` endpoint (default: `false`) |
| `evolution` | Self-evolution — optimizer config with `hard_control_enabled` |
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

## Web Dashboard

IronClaw includes an embedded real-time web dashboard for monitoring agent activity.

```yaml
dashboard:
  enabled: true
  addr: "127.0.0.1:8080"
  token: "your-secret-token"   # empty = no auth (dev mode)
```

The dashboard provides:
- **Agent Status** — current phase (PERCEIVE/PLAN/ACT/OBSERVE/REFLECT), active tools, session info
- **Phase Timeline** — horizontal timeline visualization of the 5-phase cognitive loop with durations
- **Tool Call Feed** — real-time scrolling log of tool invocations and results
- **Session Tracking** — active sessions list with today's session count

Data flows through an in-process event bus with non-blocking publish/subscribe. The Preact SPA frontend is embedded via `go:embed` — no external assets needed.

## Sub-Agent Orchestration

IronClaw supports context-isolated sub-agents via `SubAgentManager`. Define agent specs as Markdown files:

```markdown
---
name: "code-reviewer"
model: claude-haiku
max_iterations: 5
tools: [bash, file_read]
failure_strategy: fail_fast
---

You are an expert code reviewer. Focus on correctness, security, and performance.
```

Place specs in `.ironclaw/agents/*.md` (or `.yaml`). Sub-agents get isolated sessions, scoped tool registries, and structured result extraction. Use `SpawnParallel()` to run multiple sub-agents concurrently with `best_effort` or `fail_fast` strategies.

The `/team <goal>` command leverages sub-agents for multi-agent parallel task execution with LLM-driven task decomposition and dependency scheduling.

## Security Sandbox

Tool execution passes through a configurable interceptor chain:

```
PermissionInterceptor → HookInterceptor → SandboxInterceptor → Tool.Execute()
```

```yaml
sandbox:
  enabled: true
  allowed_directories: ["${WORKSPACE_DIR}", "/tmp/ironclaw"]
  readonly_directories: ["${HOME}/.ssh"]
  bash:
    backend: docker
    docker:
      image: "ironclaw-sandbox:latest"
      network: none
      memory_limit: "512m"
  network:
    mode: blacklist
    blacklist: ["internal.corp.com"]
```

Features:
- **4-level permissions**: `none` (auto-execute) → `notify` (auto-execute with notification) → `approve` (block until user approves) → `deny` (reject). Backward-compatible with legacy `allow`/`ask`/`deny` values.
- **Docker session containers**: per-session long-lived containers with idle reaping and orphan cleanup on startup
- **FileGuard**: path whitelist validation with symlink escape prevention
- **NetworkPolicy**: URL blacklist/whitelist with built-in SSRF protection (blocks `169.254.169.254`, `localhost`, etc.)
- **Graceful degradation**: falls back to host execution with policy-only restrictions when Docker is unavailable

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
make build          # Build binary (CGO_ENABLED=1, -tags fts5) — auto-builds web frontend
make web            # Build Preact frontend only (npm ci + vite build)
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

- [ ] Discord / Slack channel adapters
- [ ] Webhook triggers
- [ ] Multi-provider RL training (cross-model strategy transfer)
- [x] ~~Multi-provider LLM support~~ (OpenAI Provider — GPT-4o, Ollama, vLLM, OpenRouter)
- [x] ~~Web UI dashboard~~ (Embedded Preact SPA with WebSocket live streaming)
- [x] ~~Multi-agent collaboration~~ (Sub-Agent Isolation + Agent Teams)
- [x] ~~Plugin system for custom tools~~ (Skill System + MCP)
- [x] ~~RAG with document ingestion~~ (Knowledge Base + Knowledge Graph)
- [x] ~~Terminal UI~~ (Bubble Tea TUI Channel)
- [x] ~~Advanced memory~~ (Type taxonomy, reflection, compression, privacy, user profiling)

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
