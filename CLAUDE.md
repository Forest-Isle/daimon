# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

IronClaw is a local-first, self-evolving AI agent runtime in Go. It connects LLM providers (Claude, OpenAI, Ollama, vLLM) with tools (bash, file, HTTP, browser) and exposes them through channels (Telegram, TUI, Web Dashboard). All data persists in SQLite.

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

**Feature Registry** (`internal/feature/`, `internal/gateway/features.go`):
- Centralized feature lifecycle management: register → dependency resolution (Kahn topo-sort) → auto-detect → enable/disable
- **19 features** across 3 tiers: Tier 1 (default ON, no deps), Tier 2 (auto-detect), Tier 3 (opt-in) + one `mcp_<name>` feature per configured MCP server
- Config file `enabled` fields are overrides — features use `Feature.Default` when not configured
- **Priority order**: persisted runtime state (`~/.IronClaw/feature_state.json`) > config file override > `Feature.Default`
- Runtime control via `/feature list|enable|disable` slash commands; `/model` sets LLM model on runtime + cognitiveAgent; TUI shows dynamic arg completions after each command
- `featureEnabled(name)` nil-safe helper used by all `init_*.go` files (replaces scattered `cfg.XXX.Enabled` checks)
- AutoDetect: sandbox probes Docker; knowledge degrades to BM25-only without OpenAI key
- **Hot-reloadable features** (`HotReloadable: true`): `dashboard`, `evolution`, `scheduler`, `mcp_*` — can be toggled at runtime without restart; non-hot-reloadable changes show a restart warning in TUI
- **Late-bound lifecycle hooks** (`SetOnEnable`/`SetOnDisable`): called in `bindFeatureLifecycleHooks()` after all subsystems are initialized to avoid circular dependencies; `Enable()`/`Disable()` release the write lock before invoking hooks to prevent re-entrancy deadlock
- **MCP servers as features**: each `cfg.Tools.MCP.Servers` entry is registered as `mcp_<name>`; OnEnable = `mcpManager.StartServer`, OnDisable = `mcpManager.StopServer`; startup failures automatically `Disable()` the feature
- Adding a new feature: register in `gateway/features.go`, done (no config struct / yaml / init gate changes needed)

**Gateway wiring order** (`internal/gateway/gateway.go`) — initialization is sequential and order-dependent:
1. DB → session manager → **Feature Registry** (register → apply config overrides → load persisted `feature_state.json` → resolve & init)
2. Tool registry → LLM provider (Claude or OpenAI based on `llm.provider`) → agent runtime
3. Memory store (optional fact extractor + lifecycle manager + profiler)
4. Cognitive agent (if mode=cognitive)
5. Knowledge base + hybrid retriever + knowledge graph (if enabled)
6. SubAgentManager → AgentManager (with `.md` + `.yaml` agent specs) → TeamCoordinator (executor upgraded to use SubAgentManager.Spawn)
7. Interceptor chain: PermissionInterceptor → HookInterceptor → SandboxInterceptor (if sandbox.enabled) → inject into runtime/cognitiveAgent
8. Dashboard subsystem (if dashboard.enabled): Bus → StateTracker → EvolutionBridge → Emitter → Hub → HTTP server
9. ContextManager (always created, wraps CompressionPipeline if strategy=layered)
10. **`bindFeatureLifecycleHooks()`** — wires OnEnable/OnDisable for dashboard, evolution, scheduler, and all `mcp_*` features after all subsystems exist
11. Skill manager → scheduler → channels → MCP servers (async, per-server goroutines; failures call `features.Disable("mcp_<name>")`)

**Sub-agent isolation** (`internal/agent/subagent.go`):
- `SubAgentManager` is the central manager for sub-agent lifecycle: `Spawn()` creates an isolated session + scoped tool registry + model override per invocation, runs a full `Runtime` loop, extracts structured results, and cleans up ephemeral sessions
- `SpawnParallel()` runs multiple sub-agents concurrently with configurable `FailureStrategy` (best_effort / fail_fast)
- `AgentTool` delegates all execution to `SubAgentManager.Spawn()` — no longer constructs Runtimes directly
- Agent specs can be defined as `.ironclaw/agents/*.md` (YAML frontmatter + Markdown body as system prompt) or `.yaml` files
- Result extraction: sub-agents are instructed to output `<result>` XML blocks; if absent, LLM summarization fallback is used
- `TeamCoordinator`'s executor is upgraded to use `SubAgentManager.Spawn()` for full agent-loop task execution instead of single LLM calls

**Key adapter patterns**:
- `completerAdapter` in gateway.go bridges `agent.Provider` → `memory.Completer` (avoids circular imports between agent and memory packages)
- `noopKBEmbedder` provides a no-op `knowledge.EmbeddingProvider` when OpenAI key is absent (BM25-only fallback)
- `channel.ApprovalSender` / `channel.ReflectionSender` — optional interfaces for channels that support interactive tool approval and replan decisions; channels that don't implement them auto-approve / auto-continue
- `DashboardEmitter` interface defined in `agent` package, implemented in `dashboard` package — avoids `agent` → `dashboard` circular dependency; all emitter call sites nil-guarded for zero overhead when dashboard disabled

**LLM provider selection** (`internal/gateway/init_agent.go`):
- `llm.provider: "claude"` (default) → `agent.NewClaudeProvider`
- `llm.provider: "openai"` or `"openai-compatible"` → `agent.NewOpenAIProvider` (pure `net/http`, zero SDK dependency, supports Ollama/vLLM/LiteLLM/OpenRouter)
- `RetryProvider` wrapping layer works with both backends

**Security sandbox** (`internal/tool/interceptor.go`, `internal/sandbox/`):
- `InterceptorChain` wraps `ToolInterceptor` middleware in onion-model execution order
- `PermissionInterceptor` — 4-level permissions: `none`/`notify`/`approve`/`deny` (backward-compat with `allow`/`ask`); depends on `ToolNotifier`/`ToolApprover` channel interfaces
- `HookInterceptor` — wraps existing `pre_tool_use` hook logic
- `SandboxInterceptor` — dispatches by tool type: `bash` → Docker session container, `file_*` → `FileGuard` path validation, `http` → `NetworkPolicy` URL filtering
- `DockerSessionManager` — per-session containers (`ironclaw-sandbox-{sessionID}`), idle reaping, orphan cleanup on startup; `ProbeDocker()` detects Docker availability
- When `interceptorChain` is nil, original inline execution logic works unchanged (backward compat)

**Web Dashboard** (`internal/dashboard/`):
- Event Bus: in-process pub/sub with non-blocking publish (slow subscribers drop events)
- `Emitter` implements `agent.DashboardEmitter`, converts method calls to events; 500-char input truncation
- `StateTracker` subscribes to bus, maintains in-memory `StateSnapshot` (active sessions, current phase/tool) via `sync.RWMutex`
- `EvolutionBridge` implements `evolution.Hook`, converts evolution events to dashboard events
- `Hub` manages WebSocket connections with 30s ping heartbeat and exponential backoff reconnect on client side
- HTTP server: 7 REST endpoints + SPA fallback + optional token auth; frontend embedded via `go:embed all:dist`

**Context compression** (`internal/agent/context_manager.go`):
- `ContextManager` interface: `Compress`, `ReactiveCompress`, `Utilization`, `SplitSystemPrompt`
- `PipelineContextManager` wraps `CompressionPipeline` (5-layer: tool_output_prune → tool_eviction → turn_summarization → old_context_removal → emergency_truncation)
- Reactive 413 retry: `isContextLengthError` → `ReactiveCompress` → `RunForced` (all layers, no threshold check); per-iteration circuit breaker prevents infinite retry
- `SplitSystemPrompt` splits at `<!-- DYNAMIC_CONTEXT -->` for Anthropic Prompt Cache
- **Observability**: `EmitContextCompress(sessionID, reason, layersRun, beforePct, afterPct)` is called on both proactive and reactive compression; TUI displays `🗜️ Context compressed: X%→Y% (reason, N layers)` as a system message and tracks compression count in the stats panel

**Speculative execution** (`internal/agent/speculative.go`):
- During LLM streaming, completed `tool_use` blocks for read-only tools (`IsReadOnly() == true`) are pre-executed in background goroutines
- Results collected before runtime tool dispatch; write tools always wait for full response

**Cognitive agent internal wiring** (`NewCognitiveAgent`):
- PERCEIVE phase runs: `ProjectContextScanner.Scan()` → `GitContextProvider.Collect()` → `ContextBudgetAllocator.Apply()` → populate `CognitiveState`
- PLAN phase substitutes `{{PROJECT_CONTEXT}}` and `{{GIT_STATE}}` templates from state
- OBSERVE phase calls `generateAssertions()` → populates `ObservationResult.Assertions` and `.Failures`
- REFLECT phase calls `enrichFailureContexts()` → substitutes `{{FAILURE_CONTEXT}}` template; `replanAttempt` counter threaded through loop
- Checkpoint saved after OBSERVE, deleted on task success; `/resume` command loads checkpoint and re-enters loop

## Key Packages

- `internal/feature/` — **Feature Registry**: `feature.go` (Feature/FeatureInfo structs with `HotReloadable bool`), `registry.go` (Kahn topo-sort, `Enable`/`Disable` release lock before hook invocation to prevent re-entrancy deadlock, `SetOnEnable`/`SetOnDisable` for late binding, `RuntimeOverrides()`), `persistence.go` (`LoadOverrides`/`SaveOverrides` atomic JSON write, `DefaultStatePath`). Slash commands: `/feature list|enable|disable`, `/config show`, `/compact`, `/model`
- `internal/agent/` — Provider interface (ClaudeProvider + OpenAIProvider), Runtime (simple), CognitiveAgent (5-phase), context building, history compaction. Cognitive subsystems: `assertion.go` (auto-verification for 10+ tool types with Observation Metadata and 3-tier fallback), `failure_context.go` (structured error analysis for REFLECT), `checkpoint.go` (SQLite-backed task resume), `tool_cache.go` (per-task read-only result cache), `project_scanner.go` (project type detection), `git_context.go` (branch/status/log injection), `context_budget.go` (complexity-aware context allocation), `context_manager.go` (ContextManager interface + PipelineContextManager with 5-layer compression + reactive 413 retry), `speculative.go` (read-only tool pre-execution during streaming). **Sub-agent subsystem**: `subagent.go` (SubAgentManager — context-isolated sub-agent lifecycle), `subagent_result.go` (structured result extraction with XML template + LLM fallback), `agent_tool.go` (AgentTool delegates to SubAgentManager), `agent_manager.go` (loads `.md` agent specs with YAML frontmatter + Markdown system prompt), `spec.go` (AgentSpec with FailureStrategy). **OpenAI subsystem**: `openai.go` (OpenAIProvider with Complete + Stream + SSE parsing, pure net/http, zero SDK dependency)
- `internal/memory/` — **File-based storage**: Markdown files at `~/.ironclaw/memory/` as primary storage (YAML frontmatter + content), SQLite as auxiliary index for FTS5+vector hybrid search (RRF fusion). Scopes: session/user/global/feedback. Lifecycle management (ADD/UPDATE/DELETE/NOOP) with conflict detection. Forgetting curve integration for strength-based ranking and auto-archival. Migration tool: `ironclaw memory migrate` converts legacy SQLite data to files. **User Profile subsystem**: `profile_schema.go` (section registry with priority routing), `section_buffer.go` (per-section fact buffering with count/time triggers), `profiler.go` (fact routing → LLM-based section updates → `profile_*.md` files; `LoadProfileSections` for prompt injection; `ColdStartPrompt` for early-interaction learning; `MigrateLegacyProfile` for single→multi-section conversion). Profile files use `type: profile` with `ExcludeTypes` filtering to prevent duplicate injection.
- `internal/dashboard/` — **Web Dashboard**: `eventbus.go` (in-process pub/sub, 10 event types, non-blocking publish), `emitter.go` (DashboardEmitter implementation), `state_tracker.go` (in-memory agent state snapshots via sync.RWMutex), `evolution_bridge.go` (evolution.Hook → dashboard events), `ws_hub.go` (WebSocket connection management + broadcast), `server.go` (HTTP server with 7 REST endpoints + SPA fallback + token auth), `embed.go` (go:embed for Preact SPA dist)
- `internal/sandbox/` — **Security sandbox**: `docker_session.go` (DockerSessionManager — per-session containers with idle reaping + orphan cleanup), `docker_probe.go` (Docker availability detection), `file_guard.go` (path whitelist + symlink protection), `network_policy.go` (URL blacklist/whitelist + built-in SSRF protection)
- `internal/tool/` — Tool interface + Registry; bash/file/http/browser implementations; `policy.go` for blocked command checks; `interceptor.go` (ToolInterceptor interface + InterceptorChain onion model), `interceptor_permission.go` (4-level permissions: none/notify/approve/deny), `interceptor_hook.go` (pre_tool_use hook wrapper), `interceptor_sandbox.go` (dispatch to Docker/FileGuard/NetworkPolicy by tool type). Bash returns structured JSON (`stdout`, `stderr`, `exit_code`, `status`, `duration_ms`). Browser tools: `browser_search.go` (structured web search results), `browser_extract.go` (HTML-to-Markdown conversion with pagination)
- `internal/taskledger/` — **Unified Task Ledger**: SQLite task registry for all execution paths, atomic `ClaimNext` with `UPDATE...RETURNING`, recursive CTE tree ops, heartbeat-based stale detection. `team.go` (TeamCoordinator + worker pool + dependency scheduling), `team_planner.go` (LLM task decomposition + ParseTaskPlan validation)
- `internal/cogmetrics/` — **Cognitive Health**: `collector.go` (evolution.Hook implementation, rolling-window metrics), `rolling_avg.go` (O(1) ring buffer), `snapshot.go` (HealthReport), `reporter.go` (Markdown + JSON output)
- `internal/eval/` — **Eval Harness**: `harness.go` (RunSuite + EvolutionSnapshot + SnapshotCaptor; `SuiteResult.SaveJSON` creates parent dirs), `taskset.go` (`LoadTaskSetYAML`/`LoadTaskSetJSON`), `compare.go` (`ComparisonReport` includes `EvoSnapshotDiff` with 4 delta fields, `FormatMarkdown()` renders Evolution Snapshot Delta table), `cognitive_runner.go` (CognitiveAgentRunner for live evaluation). CLI: `ironclaw eval run --suite ./eval/example_tasks.yaml` (auto-detects .yaml/.yml); `eval compare --fail-on-regression` exits with code 1 on regressions. CI: `.github/workflows/eval-regression.yml` runs dry eval on PRs, compares against `eval_output/baseline.json` if present
- `internal/evolution/` — Self-evolution engine: `reward.go` (unified ComputeReward), `optimizer.go` (StrategyOptimizer with HardControlEnabled + GetReplanThreshold), `preference.go` (PreferenceLearner with UserFeedback + ApplyInsights), `trajectory.go` (ReflectionBrief.Reward field + UTC-safe filtering). `DispatchToolExec` is synchronous; `DispatchReflection`/`DispatchEpisode` are async with `WaitPending()` for eval sync
- `internal/knowledge/` — Document ingestion pipeline, BM25+vector hybrid retrieval, LLM reranker; `graph/` subpackage for entity/relation triples with recursive CTE traversal
- `internal/store/` — SQLite wrapper with WAL mode, embedded migrations (`//go:embed migrations/*.sql`) applied alphabetically at startup (idempotent `CREATE TABLE IF NOT EXISTS`)
- `internal/mcp/` — MCP protocol client; tools registered as `mcp_{server}_{tool}`. `Manager.StartServer(ctx, name, cfg, registry)` (exported) wraps `startServerWithRetry` for use by feature lifecycle hooks. Each configured server is a hot-reloadable feature in the registry; `/feature disable mcp_github` stops the server and unregisters its tools at runtime
- `internal/channel/telegram/` — Telegram adapter with streaming (edit-message), inline keyboard for tool approvals
- `internal/channel/tui/` — Terminal UI adapter using Bubble Tea (Charm ecosystem); supports streaming, Markdown rendering (Glamour), interactive tool approval dialogs, replan decisions, **slash command autocomplete** (type `/` to see available commands, navigate with ↑↓, accept with Tab, execute with Enter), **argument autocomplete** (dynamic suggestions for `/feature enable|disable` show live feature names via `ArgCompleter` injected by gateway), **context compression notifications** (`TUIEmitter.EmitContextCompress` sends `compressionNotificationMsg`; displayed as `🗜️ Context compressed: X%→Y%` system message + stats panel `Compressions:` counter)
- `internal/skill/` — SKILL.md files (YAML frontmatter + markdown body) loaded from `~/.IronClaw/skills/`. `LoadDir` supports two-tier scanning: `SKILL.md` in subdirectories (priority) + flat `*.md` files in subdirectories (for skill synthesizer drafts)

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

**Search flow**: `cachedIndex()` (mtime-invalidated in-memory cache of MEMORY.md) → SQL filter on `memory_index` → hybrid BM25 (FTS5) + vector (batch `IN` query) → RRF fusion with strength weighting → read top-k Markdown files → async `trackAccess` goroutine per result (file rewrite off hot path)

**Lifecycle**: LLM-driven ADD/UPDATE/DELETE/NOOP decisions with conflict detection. Updates archive old file to `archived/` and create new version.

**Forgetting curve**: Strength computed from `last_accessed_at` and access frequency. Memories with strength < 0.3 auto-archived by background task (runs every 24h).

**Consolidation**: Session files older than 24h with strength ≥ 0.5 promoted to user scope (file moved from `session/` → `user/`). Before moving, the target path is checked for conflicts; if a file with the same name already exists in `user/`, a `_v2` suffix is appended to prevent silent data loss.

**Performance**: `MEMORY.md` parsed at most once per mtime change (cached in `FileMemoryStore`; invalidated by `syncIndex`/`RebuildIndex`). Vector embeddings fetched in a single `WHERE memory_id IN (...)` batch instead of N separate queries. `trackAccess` (file rewrite + DB update) runs in a background goroutine so search returns immediately.

**Migration**: `ironclaw memory migrate` converts legacy SQLite `memory_facts` table to Markdown files. Backup created at `~/.ironclaw/backups/`. Restore with `ironclaw memory restore`.

**User Profile**: Multi-section profile stored as `user/profile_{section}.md` files (communication, tech_stack, work_pattern, projects, identity, feedback). Each section has priority, confidence, and evidence_count metadata. Facts are routed to sections via category mapping (with LLM fallback), buffered per-section, and trigger LLM-based incremental updates when thresholds are met. `LoadProfileSections` injects sorted profile into system prompt; `ExcludeTypes: ["profile"]` prevents duplicate injection from regular memory search. Cold-start prompt guides early learning when < 3 sections have confidence ≥ 0.5. Legacy single-file profiles are auto-migrated on startup.
