# Code Health Report
> Generated: 2026-06-03  
> Project: IronClaw — Local-First, Self-Evolving AI Agent Runtime  
> Scanned: 371 source files, 181 test files, 552 total Go files across 40+ packages

## Executive Summary

IronClaw is an architecturally ambitious, well-structured Go agent runtime that's currently mid-refactor (Phase 1/2 of a planned restructuring toward cleaner dependency injection via `AgentDeps` + `LoopStrategy`). Overall health is **good** — builds clean, no circular dependencies, proper SQLite indexing, and strong test coverage in critical paths. The primary issues are: (1) **RL system removal is incomplete** — dead migrations, config keys, and CLI commands remain; (2) **config file has accumulated orphan keys** from the RL removal that no code reads; (3) **Dockerfile Go version is stale** (1.23 vs go.mod's 1.25.9); (4) **three files exceed 1000 lines** and would benefit from decomposition; (5) **no `.golangci.yml` or linting config** committed to the repo. Fix the RL cleanup first — it's the highest-impact, lowest-effort item and affects both config clarity and DB schema hygiene.

## 🔴 Critical Issues
Issues that block correctness, compilation, or represent dead/conflicting code.

| # | Location | Issue | Why it matters |
|---|----------|-------|----------------|
| 1 | `internal/store/migrations/007_rl_system.sql` | Full RL schema migration (6 tables) still present and applied on fresh DBs | RL code was removed (commit `5fa1b49`), but the migration still creates `rl_episodes`, `rl_trajectories`, `rl_rewards`, `rl_model_checkpoints`, `rl_bandit_arms` tables — dead schema that bloats every new install |
| 2 | `configs/ironclaw.yaml` | RL config keys present (`clip_epsilon`, `gamma`, `gae_lambda`, `epsilon_decay`, `epsilon_start`, `epsilon_end`, `learning_rate`, `epochs`, `target_update_freq`, `prior_alpha`, `prior_beta`, `batch_size`, `buffer_size`) | These keys have zero consumers after RL removal — they silently mislead users into thinking RL is configurable |
| 3 | `cmd/ironclaw/training.go` | `training export` CLI command with `--format rlhf` still wired | References `eval.FormatRLHF` which produces trajectories from the removed RL system; command likely panics or produces empty output |
| 4 | `Dockerfile:3` | `FROM golang:1.23-bookworm` but `go.mod` specifies `go 1.25.9` | Build may fail on toolchain directives or produce unexpected behavior; image is 2 major Go versions behind |
| 5 | `internal/config/config.go` | No `RLConfig` struct found but YAML still has RL keys — config silently ignores them | Go's YAML unmarshalling drops unknown keys if no struct field matches, but this means users editing `ironclaw.yaml` will set RL values that have no effect — silent failure, terrible UX |

## 🟡 Incomplete Implementations
Stubbed, skipped, or partial code that signals unfinished work.

| # | Location | Pattern found | Notes |
|---|----------|---------------|-------|
| 1 | `internal/agent/backend.go:9-10` | `ErrBackendNotImplemented` — "backend not yet implemented (reserved for Phase 3)" | Deliberate placeholder for future A2A remote agent backend; non-critical but worth tracking |
| 2 | `internal/agent/spec.go:93` | "Phase 3: A2A remote agent support (reserved, not implemented)" | Companion to #1 — A2A server exists (`internal/a2a/server.go`, 540 lines) but remote agent client backend is stubbed |
| 3 | `internal/memory/reflector_test.go:499` | `// TODO: Consolidation integration tests (session→user promotion), knowledge graph` | Memory consolidation path lacks integration test coverage |
| 4 | `internal/sandbox/sandbox_test.go:10` | `t.Skip("skipping macOS-specific test")` | macOS sandbox tests permanently skipped — should be gated on `runtime.GOOS` with a meaningful test on macOS |
| 5 | `internal/observability/meter_test.go:24,34,61,66` | 4 Prometheus-related skips due to exporter unavailability | Observability tests are environment-dependent; consider mocks or a proper test setup |
| 6 | `internal/agent/backend_test.go:121,131,161` | 3 Docker-dependent skips | Subprocess backend test skips when `ironclaw` binary or Docker absent — these are integration tests that should run in CI only |

## 🟠 Broken Module Connections
Disconnected wiring: missing imports, orphaned exports, interface gaps.

| # | Location | Connection gap | Suggested fix |
|---|----------|---------------|---------------|
| 1 | `internal/store/migrations/007_rl_system.sql` | Migration creates tables no Go code references | Delete the migration file (or replace with a no-op that drops legacy RL tables from old installs) |
| 2 | `configs/ironclaw.yaml` → `internal/config/config.go` | ~15 RL config keys have no corresponding struct fields | Remove the YAML keys from the example config; add a config validation warning for unknown top-level keys |
| 3 | `internal/agent/emitter.go` | `DashboardEmitter` + `MetricsEmitter` interfaces defined in `agent` but implemented in `dashboard` and `channel/tui` | Intentional inversion-of-control pattern to avoid circular imports — not broken, but fragile; documented in CLAUDE.md |
| 4 | `internal/eval/training_export.go` | References `eval.FormatRLHF` — possibly dead code after RL removal | Audit this file; if format constants reference removed RL structures, delete or repurpose for evolution trajectory export |

## 🟣 Code Smells
Patterns that hurt readability, maintainability, or reliability.

| # | Location | Smell | Severity (H/M/L) |
|---|----------|-------|-----------------|
| 1 | `internal/agent/cognitive_loop.go` (1182 lines) | God file — contains PERCEIVE/PLAN/ACT/OBSERVE/REFLECT orchestration, debate mode, checkpoint resume, subtask tracking, and event emission all in one struct | H |
| 2 | `internal/gateway/gateway.go` (1098 lines) | God object — wires 25+ subsystems in a single constructor with 200+ line init methods | H |
| 3 | `internal/memory/file_store.go` (911 lines) | Too many concerns — file I/O, SQLite index, FTS5 search, hybrid retrieval, access tracking, archiving, profile loading | M |
| 4 | `configs/ironclaw.yaml` | Config file contains keys for removed features (RL), creating a misleading "available feature" impression | M |
| 5 | `internal/agent/openai.go` (655 lines) | SSE parsing, streaming, error handling, retry logic all in one file | M |
| 6 | Multiple `init_*.go` files in `gateway/` | Gateway init methods are ~12 separate files — hard to trace the full initialization order without reading gateway.go's constructor | L |
| 7 | No `.golangci.yml` committed | Linter config is implicit; new contributors won't know the linting rules | L |

## 🔵 Optimization Opportunities
Performance and resource improvements worth considering.

| # | Location | Opportunity | Estimated impact |
|---|----------|-------------|-----------------|
| 1 | `internal/memory/file_store.go:276` | Sensitivity filter `sensitivity != 'secret'` uses string comparison without index | Add partial index `WHERE sensitivity != 'secret'` for the common query path |
| 2 | `internal/memory/file_store.go` | `trackAccess` goroutine rewrites entire Markdown files on every access | Batch access writes or use a WAL-append model instead of full file rewrite per access |
| 3 | `internal/gateway/gateway.go` | Sequential init of 25+ subsystems — no parallelism where dependencies allow | Parallelize independent init groups (e.g., tools+hooks can init alongside memory system) |
| 4 | `internal/agent/cognitive_loop.go` | `generateAssertions()` creates LLM calls for assertion generation | Cache assertion patterns per tool type; most assertions are structural (exit code checks, stderr presence) |
| 5 | `internal/knowledge/store.go` (478 lines) | Chunk ingestion processes sequentially | Batch embeddings for chunks — call OpenAI API with multiple inputs per request |
| 6 | `Dockerfile` | Multi-stage build but final image is `debian:bookworm-slim` (~80MB) | Switch to `scratch` or `distroless` with CA certs only; Go binary is statically linked with CGO |

## Recommended Action Plan
Ordered list of concrete next steps (most impactful first):

1. **Purge RL remnants completely** — Delete `007_rl_system.sql` migration, remove RL config keys from `ironclaw.yaml` and `ironclaw.example.yaml`, audit `cmd/ironclaw/training.go` and `internal/eval/training_export.go` for dead RL references, remove RL-related config defaults from `config.go`.
2. **Fix Dockerfile Go version** — Update `FROM golang:1.23-bookworm` → `golang:1.25-bookworm` (match go.mod) and consider `distroless` for final stage.
3. **Add `.golangci.yml`** — Commit a linter config with project-appropriate rules; wire into CI's `ci.yml`.
4. **Decompose `cognitive_loop.go`** — Extract debate mode into `debate.go`, checkpoint logic into `cognitive_checkpoint.go`, event emission into a dedicated method set.
5. **Decompose `gateway.go`** — Extract subsystem grouping into a `SubsystemGroup` type with parallel init support; the 12 `init_*.go` files are fine but the constructor itself is too long.
6. **Config validation** — Add a post-unmarshal step that warns about unrecognized top-level keys; prevents silent ignore of removed/misspelled config.
7. **Audit eval/training_export.go** — Determine if RLHF/DPO/SFT export formats still work post-RL-removal; if not, remove the `training export` command or repurpose for evolution trajectory export.
8. **Batch memory access tracking** — Replace per-access file rewrite with batched WAL to reduce I/O on the memory hot path.
9. **Observability test fixtures** — Add mock OTel exporters so Prometheus-dependent tests can run without environment-specific skips.

## Stats
- Total issues found: 24
- Critical: 5 | Incomplete: 6 | Broken: 4 | Smells: 7 | Optimizations: 6
- Files scanned: 371 source + 181 test = 552 Go files
- Build: ✅ Clean (`go build ./...` passes)
- Vet: ✅ Clean (`go vet ./...` passes, no circular deps)
- Test ratio: 181 test files / 371 source files ≈ 48.8%
- Longest files: cognitive_loop.go (1182), gateway.go (1098), eval.go (1062)
