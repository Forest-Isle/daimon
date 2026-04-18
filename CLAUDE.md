# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

IronClaw is a local-first AI agent runtime in Go. It connects Claude AI with tools (bash, file, HTTP, browser) and exposes them through channels (Telegram, TUI). All data persists in SQLite.

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

**Module**: `github.com/Forest-Isle/IronClaw`

**Entry point**: `cmd/ironclaw/main.go` — Cobra CLI with `start`, `tui`, `version`, `skill` commands.

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
- `channel.ApprovalSender` / `channel.ReflectionSender` — optional interfaces for channels that support interactive tool approval and replan decisions; channels that don't implement them auto-approve / auto-continue

**Cognitive agent internal wiring** (`NewCognitiveAgent`):
- PERCEIVE phase runs: `ProjectContextScanner.Scan()` → `GitContextProvider.Collect()` → `ContextBudgetAllocator.Apply()` → populate `CognitiveState`
- PLAN phase substitutes `{{PROJECT_CONTEXT}}` and `{{GIT_STATE}}` templates from state
- OBSERVE phase calls `generateAssertions()` → populates `ObservationResult.Assertions` and `.Failures`
- REFLECT phase calls `enrichFailureContexts()` → substitutes `{{FAILURE_CONTEXT}}` template; `replanAttempt` counter threaded through loop
- Checkpoint saved after OBSERVE, deleted on task success; `/resume` command loads checkpoint and re-enters loop

## Key Packages

- `internal/agent/` — Provider interface, Runtime (simple), CognitiveAgent (5-phase), context building, history compaction. Cognitive subsystems: `assertion.go` (auto-verification per tool type), `failure_context.go` (structured error analysis for REFLECT), `checkpoint.go` (SQLite-backed task resume), `tool_cache.go` (per-task read-only result cache), `project_scanner.go` (project type detection), `git_context.go` (branch/status/log injection), `context_budget.go` (complexity-aware context allocation)
- `internal/memory/` — **File-based storage**: Markdown files at `~/.ironclaw/memory/` as primary storage (YAML frontmatter + content), SQLite as auxiliary index for FTS5+vector hybrid search (RRF fusion). Scopes: session/user/global/feedback. Lifecycle management (ADD/UPDATE/DELETE/NOOP) with conflict detection. Forgetting curve integration for strength-based ranking and auto-archival. Migration tool: `ironclaw memory migrate` converts legacy SQLite data to files.
- `internal/knowledge/` — Document ingestion pipeline, BM25+vector hybrid retrieval, LLM reranker; `graph/` subpackage for entity/relation triples with recursive CTE traversal
- `internal/store/` — SQLite wrapper with WAL mode, embedded migrations (`//go:embed migrations/*.sql`) applied alphabetically at startup (idempotent `CREATE TABLE IF NOT EXISTS`)
- `internal/tool/` — Tool interface + Registry; bash/file/http/browser implementations; `policy.go` for blocked command checks. Bash returns structured JSON (`stdout`, `stderr`, `exit_code`, `status`, `duration_ms`). Browser tools: `browser_search.go` (structured web search results), `browser_extract.go` (HTML-to-Markdown conversion with pagination)
- `internal/mcp/` — MCP protocol client; tools registered as `mcp_{server}_{tool}`
- `internal/channel/telegram/` — Telegram adapter with streaming (edit-message), inline keyboard for tool approvals
- `internal/channel/tui/` — Terminal UI adapter using Bubble Tea (Charm ecosystem); supports streaming, Markdown rendering (Glamour), interactive tool approval dialogs, replan decisions, and **slash command autocomplete** (type `/` to see available commands, navigate with ↑↓, accept with Tab, execute with Enter)
- `internal/skill/` — SKILL.md files (YAML frontmatter + markdown body) loaded from `~/.IronClaw/skills/`

## Config

YAML at `configs/ironclaw.yaml` (copy from `ironclaw.example.yaml`). Environment variables expanded via `${VAR}` syntax. User overlay from `~/.IronClaw/config.yaml`.

## Database

SQLite at `./data/ironclaw.db`. Migrations in `internal/store/migrations/` (001-018). FTS5 is probed at startup and gracefully degrades to LIKE queries if unavailable.

## Memory System

**Storage architecture**: File-first with SQLite auxiliary index.

**Primary storage**: Markdown files at `~/.ironclaw/memory/` with YAML frontmatter:
- Directory structure: `user/`, `session/`, `feedback/`, `global/`, `archived/`
- File naming: `{scope}/{category}_{YYYYMMDD}_{id}.md`
- Frontmatter fields: id, scope, user_id, session_id, created_at, updated_at, last_accessed_at, strength, related_to, promoted_from
- `MEMORY.md` index file lists all active memories with one-line descriptions

**SQLite index** (migration 006): Three tables for fast search:
- `memory_index` — file path → metadata mapping (scope, user_id, timestamps, strength)
- `memory_fts` — FTS5 virtual table for BM25 full-text search
- `memory_embeddings` — vector embeddings for semantic search

**Search flow**: Parse MEMORY.md → query SQL index (FTS5 + vector) → RRF fusion with strength weighting → read top-k Markdown files

**Lifecycle**: LLM-driven ADD/UPDATE/DELETE/NOOP decisions with conflict detection. Updates archive old file to `archived/` and create new version.

**Forgetting curve**: Strength computed from `last_accessed_at` and access frequency. Memories with strength < 0.3 auto-archived by background task (runs every 24h).

**Consolidation**: Session files older than 24h with strength ≥ 0.5 promoted to user scope (file moved from `session/` → `user/`). Before moving, the target path is checked for conflicts; if a file with the same name already exists in `user/`, a `_v2` suffix is appended to prevent silent data loss.

**Migration**: `ironclaw memory migrate` converts legacy SQLite `memory_facts` table to Markdown files. Backup created at `~/.ironclaw/backups/`. Restore with `ironclaw memory restore`.
