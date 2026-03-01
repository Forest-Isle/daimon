# IronClaw

**Local-first AI Agent Runtime, built with Go.**

[中文文档](README_zh.md)

[![CI](https://github.com/punkopunko/ironclaw/actions/workflows/ci.yml/badge.svg)](https://github.com/punkopunko/ironclaw/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/punkopunko/ironclaw)](go.mod)
[![Release](https://img.shields.io/github/v/release/punkopunko/ironclaw)](https://github.com/punkopunko/ironclaw/releases)

IronClaw is a self-hosted AI agent runtime that runs entirely on your own infrastructure. It connects Claude AI with real-world tools — shell commands, file operations, HTTP requests — and exposes them through channels like Telegram. All data stays local in SQLite.

## Features

- **Dual Agent Modes** — Simple linear loop or Cognitive 5-phase loop (PERCEIVE → PLAN → ACT → OBSERVE → REFLECT) with replan support
- **Long-term Memory** — mem0-style fact extraction with three scopes (session/user/global), FTS5+vector hybrid search, lifecycle management (ADD/UPDATE/DELETE), and automatic consolidation
- **Knowledge Base** — Document ingestion pipeline (Markdown, code, text, web) with BM25+vector hybrid retrieval and optional LLM reranker
- **Knowledge Graph** — Entity/relation triple extraction with multi-hop recursive CTE traversal and provenance tracking
- **MCP Protocol** — Connect multiple MCP servers with hot-reload, automatic tool discovery and registration
- **Skill System** — Extensible SKILL.md format with built-in ClawHub registry for searching, installing, and managing skills
- **Telegram Bot Channel** — Streaming message updates, inline keyboard for tool approvals and replan decisions, user-level access control
- **Tool System** — Built-in tools for Bash execution, file I/O, HTTP requests, and browser automation
- **Persona & User Directory** — Auto-initialized `~/.IronClaw/` with personality files (Soul.md, Memory.md, Agent.md) and per-user MCP configs
- **Local Storage** — SQLite with WAL mode, embedded migrations, FTS5 full-text search (graceful degradation)
- **Task Scheduler** — Cron-based scheduled tasks with database-backed persistence
- **Tool Approval** — Configurable per-tool approval mechanism with Telegram inline keyboard
- **HTTP Gateway** — Optional REST API for programmatic access
- **Session Management** — Per-user conversation sessions with history compaction

## Architecture

```
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│  Telegram    │────▶│   Gateway    │────▶│   Agent         │
│  Channel     │◀────│   (Router)   │◀────│ Simple/Cognitive│
└─────────────┘     └──────┬───────┘     └──────┬──────────┘
                           │                     │
                    ┌──────┴──────┐        ┌─────┴──────┐
                    │  HTTP API   │        │   Tools    │
                    │  (optional) │        │ bash/file/ │
                    └─────────────┘        │ http/mcp   │
                                           └─────┬──────┘
                                                  │
┌─────────────┐  ┌─────────────┐  ┌───────────────┴───────┐
│  Scheduler  │  │   Skills    │  │       Store (SQLite)   │
│  (cron)     │  │  (ClawHub)  │  ├────────────┬───────────┤
└─────────────┘  └─────────────┘  │  Memory    │ Knowledge │
                                  │ (FTS5+vec) │ (BM25+vec)│
┌─────────────┐                   ├────────────┤───────────┤
│  User Dir   │                   │  Knowledge Graph       │
│ (~/.IronClaw)│                  │  (entity triples)      │
└─────────────┘                   └────────────────────────┘
```

## Quick Start

### From Source

```bash
# Clone
git clone https://github.com/punkopunko/ironclaw.git
cd ironclaw

# Copy and edit config
cp configs/ironclaw.example.yaml configs/ironclaw.yaml
# Fill in your ANTHROPIC_API_KEY and TELEGRAM_BOT_TOKEN
vim configs/ironclaw.yaml

# Build (requires CGO for SQLite)
make build

# Run
./bin/ironclaw start
```

### Docker

```bash
# Copy and edit config
cp configs/ironclaw.example.yaml configs/ironclaw.yaml

# Run with Docker Compose
docker compose up -d
```

### Pre-built Binaries

Download from the [Releases](https://github.com/punkopunko/ironclaw/releases) page.

```bash
# Download (example for Linux amd64)
curl -LO https://github.com/punkopunko/ironclaw/releases/latest/download/ironclaw_linux_amd64.tar.gz
tar xzf ironclaw_linux_amd64.tar.gz

# Copy and edit config
cp configs/ironclaw.example.yaml configs/ironclaw.yaml

# Run
./ironclaw start
```

## Configuration

IronClaw uses a YAML config file. See [`configs/ironclaw.example.yaml`](configs/ironclaw.example.yaml) for all available options.

Key settings:

| Section | Description |
|---------|-------------|
| `llm` | AI provider config (API key, model, max tokens) |
| `telegram` | Bot token and allowed user IDs |
| `agent` | Mode (simple/cognitive), max iterations, system prompt, personality |
| `store` | SQLite database path |
| `memory` | Fact extraction, scopes, similarity threshold, consolidation, BM25/vector weights |
| `knowledge` | Document ingestion dirs, chunk size, hybrid retrieval, reranker, graph |
| `skills` | Enable/disable, extra skill directories |
| `scheduler` | Cron task scheduler |
| `tools` | Per-tool enable/disable, timeouts, approval settings, MCP servers |
| `server` | Optional HTTP API endpoint |
| `log` | Log level and format |

Environment variables can be used in config values with `${VAR_NAME}` syntax.

## Skill Management

IronClaw supports extensible skills via SKILL.md files and the [ClawHub](https://clawhub.ai) public registry.

```bash
# List installed skills (including built-in)
ironclaw skill list

# Search for skills
ironclaw skill search "web scraping"

# Install a skill
ironclaw skill install <slug>

# Update all skills
ironclaw skill update

# Remove a skill
ironclaw skill remove <name>
```

Skills are stored in `~/.IronClaw/skills/`. Requires `clawhub` CLI (`npm install -g clawhub`).

## User Directory

On first run, IronClaw initializes `~/.IronClaw/` with:

- `Soul.md` — Agent personality and communication style
- `Memory.md` — Persistent rules and preferences
- `Agent.md` — Core system prompt template
- `skills/` — User-installed skills
- `mcp/` — MCP server configurations (YAML, hot-reloaded)

## Development

```bash
# Build
make build

# Run tests
make test

# Lint (requires golangci-lint)
make lint

# Format code
make fmt

# Build Docker image
make docker

# Show all targets
make help
```

## Roadmap

- [ ] Multi-provider LLM support (OpenAI, local models)
- [ ] Web UI dashboard
- [ ] Discord / Slack channel adapters
- [ ] Multi-agent collaboration
- [ ] Webhook triggers
- [x] ~~Plugin system for custom tools~~ (Skill System + MCP)
- [x] ~~RAG with document ingestion~~ (Knowledge Base + Knowledge Graph)

## Contributing

Contributions are welcome! Please read the [Contributing Guide](CONTRIBUTING.md) before submitting a PR.

## License

[MIT](LICENSE)
