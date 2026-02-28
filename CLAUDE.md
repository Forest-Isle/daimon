# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

IronClaw is a local-first AI agent runtime in Go. It connects Claude AI with tools (bash, file, HTTP, browser) and exposes them through channels (Telegram). All data persists in SQLite.

## Build & Dev Commands

```bash
make build          # Build binary (CGO_ENABLED=1, -tags fts5)
make test           # Run all tests (CGO_ENABLED=1, -tags fts5)
make lint           # golangci-lint
make fmt            # go fmt + goimports
make run            # Build and start
```

**CGO_ENABLED=1 and `-tags fts5` are required** for all build/test commands — SQLite uses cgo (`mattn/go-sqlite3`) and FTS5 must be enabled at compile time.

Single test: `CGO_ENABLED=1 go test -tags "fts5" -run TestName ./internal/package/ -v`

## Architecture

**Module**: `github.com/punkopunko/ironclaw`

**Entry point**: `cmd/ironclaw/main.go` — Cobra CLI with `start`, `version`, `skill` commands.

**Two agent modes** (`agent.mode` in config):
- `simple` — linear loop: system prompt → LLM → tool calls → repeat (up to `max_iterations`)
- `cognitive` — 5-phase loop: PERCEIVE → PLAN → ACT → OBSERVE → REFLECT, with replan support

**Gateway wiring order** (`internal/gateway/gateway.go`) — initialization is sequential and order-dependent:
1. DB → session manager → tool registry → LLM provider → agent runtime
2. Memory store (optional fact extractor + lifecycle manager)
3. Cognitive agent (if mode=cognitive)
4. Knowledge base + hybrid retriever + knowledge graph (if enabled)
5. Skill manager → scheduler → channels

**Key adapter patterns**:
- `completerAdapter` in gateway.go bridges `agent.Provider` → `memory.Completer` (avoids circular imports between agent and memory packages)
- `noopKBEmbedder` provides a no-op `knowledge.EmbeddingProvider` when OpenAI key is absent (BM25-only fallback)

## Key Packages

- `internal/agent/` — Provider interface, Runtime (simple), CognitiveAgent (5-phase), context building, history compaction
- `internal/memory/` — mem0-style fact extraction, FTS5+vector hybrid search (RRF fusion), scopes: session/user/global, lifecycle management (ADD/UPDATE/DELETE/NOOP)
- `internal/knowledge/` — Document ingestion pipeline, BM25+vector hybrid retrieval, LLM reranker; `graph/` subpackage for entity/relation triples with recursive CTE traversal
- `internal/store/` — SQLite wrapper with WAL mode, embedded migrations (`//go:embed migrations/*.sql`) applied alphabetically at startup (idempotent `CREATE TABLE IF NOT EXISTS`)
- `internal/tool/` — Tool interface + Registry; bash/file/http/browser implementations; `policy.go` for blocked command checks
- `internal/mcp/` — MCP protocol client; tools registered as `mcp_{server}_{tool}`
- `internal/channel/telegram/` — Telegram adapter with streaming (edit-message), inline keyboard for tool approvals
- `internal/skill/` — SKILL.md files (YAML frontmatter + markdown body) loaded from `~/.IronClaw/skills/`

## Config

YAML at `configs/ironclaw.yaml` (copy from `ironclaw.example.yaml`). Environment variables expanded via `${VAR}` syntax. User overlay from `~/.IronClaw/config.yaml`.

## Database

SQLite at `./data/ironclaw.db`. Migrations in `internal/store/migrations/` (001-005). FTS5 is probed at startup and gracefully degrades to LIKE queries if unavailable.
