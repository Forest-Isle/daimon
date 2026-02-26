# IronClaw

**Local-first AI Agent Runtime, built with Go.**

[中文文档](README_zh.md)

[![CI](https://github.com/punkopunko/ironclaw/actions/workflows/ci.yml/badge.svg)](https://github.com/punkopunko/ironclaw/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/punkopunko/ironclaw)](go.mod)
[![Release](https://img.shields.io/github/v/release/punkopunko/ironclaw)](https://github.com/punkopunko/ironclaw/releases)

IronClaw is a self-hosted AI agent runtime that runs entirely on your own infrastructure. It connects Claude AI with real-world tools — shell commands, file operations, HTTP requests — and exposes them through channels like Telegram. All data stays local in SQLite.

## Features

- **Claude AI Agent** — Powered by Anthropic's Claude with multi-turn conversation and context compaction
- **Telegram Bot Channel** — Chat with your agent through Telegram with user-level access control
- **Tool System** — Built-in tools for Bash execution, file I/O, HTTP requests, and browser automation
- **Local Storage** — SQLite-based persistence with vector memory search for long-term recall
- **Task Scheduler** — Cron-based scheduled tasks for automated workflows
- **Tool Approval** — Configurable approval mechanism for sensitive tool executions
- **HTTP Gateway** — Optional REST API for programmatic access
- **Session Management** — Per-user conversation sessions with history

## Architecture

```
┌─────────────┐     ┌──────────────┐     ┌─────────────┐
│  Telegram    │────▶│   Gateway    │────▶│   Agent     │
│  Channel     │◀────│   (Router)   │◀────│   Runtime   │
└─────────────┘     └──────────────┘     └──────┬──────┘
                           │                     │
                    ┌──────┴──────┐        ┌─────┴──────┐
                    │   HTTP API  │        │   Tools    │
                    │  (optional) │        │ bash/file/ │
                    └─────────────┘        │ http/browse│
                                           └─────┬──────┘
                                                  │
                    ┌─────────────┐        ┌──────┴──────┐
                    │  Scheduler  │        │   Store     │
                    │  (cron)     │        │  (SQLite)   │
                    └─────────────┘        └─────────────┘
                                           ┌─────────────┐
                                           │   Memory    │
                                           │ (embedding) │
                                           └─────────────┘
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
| `agent` | Max iterations, system prompt |
| `store` | SQLite database path |
| `memory` | Embedding-based memory search |
| `scheduler` | Cron task scheduler |
| `tools` | Per-tool enable/disable, timeouts, approval settings |
| `server` | Optional HTTP API endpoint |
| `log` | Log level and format |

Environment variables can be used in config values with `${VAR_NAME}` syntax.

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
- [ ] Plugin system for custom tools
- [ ] Discord / Slack channel adapters
- [ ] Multi-agent collaboration
- [ ] RAG with document ingestion
- [ ] Webhook triggers

## Contributing

Contributions are welcome! Please read the [Contributing Guide](CONTRIBUTING.md) before submitting a PR.

## License

[MIT](LICENSE)
