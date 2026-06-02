# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased] — 2026-06-03

### Added
- **Temporal Fact Modeling**: memory facts now carry `valid_from`/`valid_to` timestamps with soft invalidation on contradiction — default search returns only current-truth facts (`valid_to IS NULL`), `IncludeHistorical` flag for audit/historical queries, `SoftInvalidate()` method for contradiction handling, partial index on active facts for fast current-truth retrieval. Enables 15+ point LongMemEval improvement per 2026 industry benchmarks.
- **Circuit Breaker for LLM Providers**: `CircuitBreaker` type with Closed→Open→HalfOpen state machine integrated into `RetryProvider` and `AgentTool` — prevents cascading retry storms against failing upstreams. Configurable threshold (5 consecutive failures) and open timeout (30s). `ErrCircuitOpen` sentinel for graceful caller handling.
- **OpenInference Span Kinds**: all OTel spans annotated with `openinference.span.kind` (LLM/TOOL/AGENT/RETRIEVER/CHAIN/RERANKER/GUARDRAIL/EMBEDDING) and `trace.SpanKind` (Client for LLM/tool calls, Internal for sub-agents) — compatible with Arize Phoenix, Grafana, LangSmith, and traceAI.
- **OTel-GenAI Token Attributes**: LLM spans now carry `gen_ai.usage.input_tokens` and `gen_ai.usage.output_tokens` from Claude and OpenAI responses — enables token-usage-based alerting and cost tracking in any OTel backend.
- **Agent-as-Tool Enhancements**: `AgentTool` now auto-generates rich descriptions including available tools, max iterations, and model info; implements `CapableTool` interface with proper `ParallelSafety` and `ApprovalMode` — parent LLMs can now make informed delegation decisions.
- **Config Validation**: `CheckUnknownKeys()` warns on startup about top-level YAML keys with no corresponding Config struct field — catches typos and keys from removed features (e.g., `rl:`).
- **CI Pipeline**: added `vet` job, `build-bin` target (no npm dependency), `.golangci.yml` with project-specific linter config (gosec, gocognit, dupl).
- **Docker Production Readiness**: OCI labels, HEALTHCHECK, EXPOSE ports, build tags (`fts5`), version/commit/date build args.

### Changed
- **RL System Purged**: deleted dead `007_rl_system.sql` migration (82 lines, 6 SQLite tables), added `022_drop_rl_tables.sql` cleanup migration, removed ~33 lines of dead RL config from both YAML configs, renamed `FormatRLHF`→`FormatReward` in eval/training_export (uses evolution trajectory data, not the removed RL system).
- **Dockerfile**: Go builder `golang:1.23`→`1.25` to match `go.mod`; added CGO build tags.
- **Makefile**: added `build-bin`, `test-coverage`, `vet` targets; docker target passes version/commit/date.

### Fixed
- Stale Dockerfile Go version causing potential toolchain directive mismatch.
- Config YAML silently ignoring unknown keys (no user feedback when RL keys did nothing).

## [0.4.0] - 2026-04-19

### Added
- **Web Dashboard**: real-time agent monitoring via embedded Preact SPA — in-process event bus, WebSocket live streaming, REST API (`/api/agent/state`, `/api/sessions`, `/ws`), 5-phase timeline visualization, token-based auth, `go:embed` single-binary deployment; configured via `dashboard:` config section
- **Sub-Agent Isolation**: `SubAgentManager` as unified sub-agent lifecycle manager — per-invocation isolated sessions (`subagent_{name}_{uuid8}`), scoped tool registries, model routing per `AgentSpec`, structured `SubAgentResult` with XML template extraction (+ LLM summarization fallback), `SpawnParallel()` with `best_effort`/`fail_fast` strategies, Markdown agent spec format (`.ironclaw/agents/*.md` with YAML frontmatter), `AgentTool` simplified from ~580 to ~132 lines, `TeamCoordinator` executor upgraded to full agent loop
- **Security Sandbox**: interceptor chain architecture (`ToolInterceptor` middleware) for tool execution — 4-level permission system (`none`/`notify`/`approve`/`deny` with backward-compatible mapping from `allow`/`ask`), Docker session containers per agent session (idle reaping, orphan cleanup), `FileGuard` with symlink-safe path validation, `NetworkPolicy` with built-in SSRF protection, graceful degradation when Docker unavailable
- **OpenAI-Compatible Provider**: `OpenAIProvider` supporting any OpenAI Chat Completions API (OpenAI, Ollama, vLLM, LiteLLM, OpenRouter) — pure `net/http` implementation with zero SDK dependency, SSE streaming with tool-call fragment accumulation, `llm.provider` config key (`claude`/`openai`/`openai-compatible`)
- **User Profile Modeling**: multi-section user profiles (`communication`, `tech_stack`, `work_pattern`, `projects`, `feedback`, `identity`) stored as `user/profile_*.md` files — two-layer fact routing (category mapping + LLM classification), per-section buffered updates with priority-based thresholds, `LoadProfileSections` for system prompt injection with `ExcludeTypes` deduplication, cold-start prompt for early learning, legacy single-file profile auto-migration
- **Evolution Loop Closure**: 10 fixes across 6 packages closing the 3-loop self-evolution pipeline — P0: skill draft loading (LoadDir now scans flat `*.md` in subdirectories); P1: synchronous `DispatchToolExec`, consistent online/offline reward formula, `ReflectionBrief.Reward` field separation, eval runner `WaitPending()` sync, `UserFeedback` → preference learning; P2: simple-task evolution events, insights → preference feedback, `EvolutionSnapshot` for eval, UTC-safe trajectory filtering
- **Evolution Benchmark**: eval pipeline 7-fix closure + workload injection for evolution pressure + time-series `LongitudinalReport` with ASCII sparkline visualization; `ironclaw eval longitudinal` command for multi-iteration benchmarking
- **Eval Live Mode**: `CognitiveAgentRunner` driving real cognitive agent evaluation, evolution-specific task sets, `ironclaw eval longitudinal` for cross-session trend tracking

### Fixed
- **Assertion Metadata Fix**: `Observation.Metadata` channel bridging `tool.Result.Metadata` to assertion functions — HTTP assertions rewritten with 3-tier fallback (metadata → plain-text parsing → legacy JSON), browser search/extract assertions corrected for actual output formats (raw arrays, raw Markdown), MCP assertions enhanced with non-empty output check, new `file_list` dedicated assertions

## [0.3.0] - 2026-04-18

### Added
- **Agent Teams**: `/team <goal>` slash command for multi-agent parallel task execution — LLM-driven task decomposition with dependency DAG, `TeamCoordinator` with worker pool, `ClaimNext` task claiming, cascading dependency failure propagation, configurable `agent.team.max_workers`
- **Context Compression Pipeline**: `ContextManager` unified interface with 5-layer progressive compression (tool output pruning → tool eviction → turn summarization → old context removal → emergency truncation) — reactive 413/context_length_exceeded auto-retry, system prompt `<!-- DYNAMIC_CONTEXT -->` split for Anthropic Prompt Cache, `PipelineContextManager` always created for fallback
- **Speculative Execution**: pre-execute read-only tools (`IsReadOnly() == true`) during LLM streaming — early `tool_use` block detection in SSE stream, background goroutine execution, result collection before runtime tool dispatch, write tools unaffected
- **Unified Task Ledger**: SQLite task registry (`internal/taskledger/`) for all execution paths — atomic `ClaimNext` with `UPDATE...RETURNING`, recursive CTE tree operations, heartbeat-based stale task detection, parent-child hierarchy, `/tasks` slash command for real-time status
- **Eval Harness**: `ironclaw eval run` and `ironclaw eval compare` CLI commands — `TaskSet` YAML definition, pluggable `TaskRunner` interface, `SuiteResult` with per-task metrics (success, assertion pass rate, replan count, duration), delta comparison reporting
- **Cognitive Health Dashboard**: `internal/cogmetrics/` package with `ironclaw insights health` CLI — `RollingAvg` sliding window metrics (assertion pass rate, replan rate/efficiency, tool reliability, complexity success rate, average confidence), Markdown and JSON output, offline trajectory replay
- **Expanded Assertion Coverage**: structured assertions expanded from 3 to 10+ tool types — `file_read`, `browser_search`, `browser_extract`, `mcp_*` (prefix match), `skill_*`, `memory_*`, generic fallback for unknown tools; 19 new unit tests + 3 integration tests
- **Strategy Hard Control**: `StrategyOptimizer` directly overrides `CognitiveAgent.confidenceThreshold` when `hard_control_enabled: true` — `GetReplanThreshold()` with `[0.01, 0.99]` clamp, unified `ComputeReward` function eliminating online/offline reward formula divergence
- **Agent Reliability Improvements**: task checkpoint/resume (`/resume` command, migration 018), structured verification (auto-generated assertions per tool type in OBSERVE phase), context-aware smart retry (typed `FailureContext` in REFLECT prompts with degradation warnings), structured bash JSON output, `browser_search` and `browser_extract` tools, per-task tool result cache with SHA256 keying and write-triggered invalidation, project context auto-injection (Go/Node/Rust/Python detection), Git state awareness (`{{GIT_STATE}}` in PLAN prompts), dynamic context budget allocation by task complexity

## [0.2.0] - 2026-04-11

### Added
- HTTP metrics endpoint (`/metrics`) on the optional HTTP gateway, exposing Prometheus-style counters for active sessions, total tool calls, LLM tokens used, and agent iteration counts; implemented in `internal/gateway/metrics.go`
- New config key `http.metrics_enabled` (default: `false`) to opt in to the metrics endpoint

## [0.1.2] - 2026-04-11

### Added
- Performance section in README documenting p50/p99 benchmarks for tool calls (10ms p99), LLM round trips (50ms p99), memory search, and knowledge base retrieval
- Troubleshooting section in README covering common issues: SQLite lock errors, FTS5 degradation, Telegram bot not responding, and LLM auth errors

### Changed
- Architecture diagram updated to highlight the RL Engine box (Bandit/PPO/DQN) more prominently

## [0.1.1] - 2026-04-11

### Fixed
- Memory consolidator no longer overwrites existing files when promoting session-scope memories to user scope — target path is now checked before the move, and a `_v2` suffix is appended if a file with the same name already exists, preventing silent data loss

## [0.1.0] - 2025-01-01

### Added

- Claude AI agent runtime with multi-turn conversation support
- Telegram Bot channel adapter with user-level access control
- Tool system: Bash execution, file I/O, HTTP requests, browser automation
- SQLite-based persistent storage
- Vector embedding memory search for long-term recall
- Cron-based task scheduler
- Configurable tool execution approval mechanism
- Optional HTTP gateway for REST API access
- Per-user session management with conversation history
- Context compaction for long conversations
- YAML-based configuration with environment variable support
