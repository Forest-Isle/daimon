# IronClaw Codebase Optimization Analysis

**Date:** 2026-06-03
**Scope:** Full codebase structural, complexity, and redundancy audit
**Methodology:** Quantitative file/package/LOC analysis + qualitative architectural review

---

## 1. Executive Summary

IronClaw is a 104,682-line Go codebase (559 source files, 37 internal packages) implementing a local-first, self-evolving AI agent runtime. The project has accumulated significant structural debt: the `agent` package alone contains 19,174 lines across 128 files and 218 structs, the `gateway.go` wiring file spans 1,101 lines, and there are at least 5 distinct planning subsystems, 4 execution graph implementations, and 3 memory/retrieval backends with overlapping responsibilities. The test-to-production ratio is a healthy 0.56 (36,859 test LOC), but the eval harness alone (6,477 LOC) is larger than the entire `tool` package (4,963 LOC) that it tests.

The core issue is not lack of features but **accumulation without consolidation**. Multiple features were added as experimental flags (`wasm_plugins`, `a2a`, `collective`, `worktree`, `code_engine`, `cortex`, `guardian`) without clear adoption signals. The feature registry (19 features) gate-keeps code that cannot be removed at compile time. The project has outgrown its original architecture but has not been refactored to match its actual usage patterns.

**Primary recommendation:** A phased, 12-week simplification that reduces the agent package by 30-40%, eliminates 3-4 redundant subsystems, collapses 5 package-level feature silos into their parent packages, and enforces a package size ceiling of 2,500 lines.

---

## 2. Current State Assessment

### 2.1 Project Vital Statistics

| Metric | Value | Health Indicator |
|--------|-------|-----------------|
| Total Go source files | 559 | Large for a single-binary project |
| Total LOC (all .go) | 104,682 | -- |
| Production LOC (internal/) | 64,798 | -- |
| Test LOC (internal/) | 36,859 | Healthy 0.56 ratio |
| Internal packages | 37 | High for single-binary Go |
| Largest package (agent) | 19,174 LOC / 128 files | **Critical: monolith** |
| Second largest (eval) | 6,477 LOC / 48 files | Disproportionate to runtime |
| Interfaces declared | 71 | Reasonable |
| Structs declared | ~350 (est.) | High |
| Database migrations | 20 | Acceptable |
| Feature flags | 19 | Many experimental, low-adoption |
| Top-level config sections | 22 | High cognitive load |
| TODO/FIXME/HACK markers | 16 | Low -- good sign |
| Git worktree feature LOC | 555 | Isolated but unused |

### 2.2 Package Size Distribution (Non-Test LOC)

| Package | LOC | Files | Rating | Concern |
|---------|-----|-------|--------|---------|
| `agent` | 19,174 | 128 | **CRITICAL** | God package; contains runtime, loops, planners, backends, providers, compression, streaming, replay, hooks, sub-agents, team orchestration, assertions, self-healing, debate, context management |
| `eval` | 6,477 | 48 | **HIGH** | Larger than the tools it evaluates; has its own runners, harnesses, classifiers, verifiers, judges |
| `tool` | 4,963 | 49 | MODERATE | Reasonable size but 49 files suggests over-fragmentation |
| `memory` | 4,832 | 40 | MODERATE | File-store + profiler + reflector + SQLite index = too many responsibilities |
| `evolution` | 4,650 | 39 | **HIGH** | Complex self-modification system with unclear efficacy evidence |
| `gateway` | 4,350 | 33 | **HIGH** | gateway.go alone is 1,101 lines; 33 files for wiring is excessive |
| `channel` | 3,564 | 17 | MODERATE | Three adapter implementations, each ~450 lines |
| `knowledge` | 2,530 | 27 | MODERATE | Overlaps with memory and cortex |
| `config` | 1,298 | 15 | MODERATE | Merge/hierarchy/watcher complexity |
| `hook` | 991 | 14 | LOW | Reasonable |
| `a2a` | 944 | 4 | LOW | Isolated; unclear adoption |
| `code_engine` | 897 | 6 | LOW | Overlaps with agent/codebase_index.go |
| `collective` | 875 | 8 | LOW | Market/consensus/reputation -- speculative |
| `sandbox` | 845 | 13 | MODERATE | Reasonable scope |
| `taskledger` | 770 | 10 | LOW | Overlaps with agent task tracking |
| `wasm` | 693 | 8 | LOW | Plugin system with no known plugins |
| `cogmetrics` | 650 | 9 | LOW | Rolling-window metrics -- could be simpler |
| `cortex` | 630 | 5 | LOW | Unified retriever overlapping with knowledge+memory |
| `mcp` | 585 | 6 | LOW | Protocol client, reasonable |
| `worktree` | 555 | 4 | LOW | Git worktree tools, isolated |
| `session` | 453 | 5 | LOW | Core; reasonable |
| `feature` | 449 | 4 | LOW | Core; reasonable |
| `observability` | 427 | 7 | MODERATE | Distributed tracing setup |
| `guardian` | 418 | 2 | LOW | Process supervision |
| `skill` | 417 | 8 | LOW | SKILL.md loader |
| Remaining (<400 each) | ~1,700 | ~20 | LOW | Config, ratelimit, health, store, etc. |

### 2.3 Complexity Hotspots

**File-level:**
- `agent/cognitive_loop.go` (1,110 lines) -- the core loop is a single file with phase methods, pre-plan search, reward computation, task registration, streaming control, and reflection logic all interleaved.
- `gateway/gateway.go` (1,101 lines) -- the `New()` constructor wires every subsystem sequentially; adding a feature means adding another block to this function.
- `agent/mcts_planner.go` (729 lines) -- Monte Carlo Tree Search planner, one of 5 planning approaches.
- `memory/file_store.go` (940 lines) -- file operations, indexing, search, caching, lifecycle management in one struct.
- `agent/openai.go` (671 lines) -- pure net/http OpenAI client with SSE parsing inline alongside provider methods.

**Structural:**
- 5 planning systems: `Planner` (plan.go), `MCTSPlanner` (mcts_planner.go), `StrategicTreePlanner` (tree_planner.go), `PlanMode` (plan_mode.go), `team_planner` (taskledger/team_planner.go)
- 4 execution graphs: `graph_engine.go` (DAG), `cognitive_loop.go` (5-phase), `autonomous_loop.go` (discovery), `streaming_pipeline.go` (streaming wrapper)
- 3 execution backends: `InProcessBackend`, `DockerBackend`, `SubprocessBackend` -- all in the agent package
- 3 memory/retrieval systems: `memory` (file+SQIite), `knowledge` (BM25+vector+graph), `cortex` (unified retriever wrapping both)
- 2 code intelligence systems: `agent/codebase_index.go` (embedding-based) and `code_engine/` (symbol+call-graph)

---

## 3. Ecosystem Context

### 3.1 What the Agent Landscape Tells Us

The 2024-2026 agent ecosystem has converged on several patterns that provide a useful lens for evaluating IronClaw:

**Converged patterns (what matters):**
1. **Tool use is the atomic unit** -- Every successful agent framework (LangChain, CrewAI, AutoGen, Anthropic's tool-use API) centers on tool calling. IronClaw's `tool` package is well-structured for this.
2. **The loop is simple** -- The most successful agents (Claude Code, Cursor, Aider, Goose) use a basic observe-think-act loop. IronClaw's cognitive 5-phase loop adds genuine value (structured reflection, replan) but the streaming variants, MCTS planning, and autonomous discovery loop represent experimental overhead.
3. **Memory matters, but simply** -- Effective memory systems are retrieval-focused (vector + keyword), not life-cycle-management heavy. IronClaw's file-first memory with strength decay, consolidation, and multi-section profiling is over-engineered relative to demonstrated improvements.
4. **Multi-agent is niche** -- Despite hype, production multi-agent systems are rare. IronClaw's `team`, `taskledger`, `collective`, and sub-agent subsystems represent a large investment in unproven territory.
5. **Self-evolution is unproven** -- No major agent framework has demonstrated reliable self-improvement. The `evolution` package (4,650 LOC) is the largest speculative investment in the codebase.

**Anti-patterns IronClaw embodies:**
- **Inner-platform effect:** Building a Wasm plugin system (693 LOC), a collective agent market (875 LOC), and an agent-to-agent protocol (944 LOC) before having a single external plugin, market participant, or remote agent.
- **Speculative generality:** The feature registry gates 19 features; at least 8 (`wasm_plugins`, `a2a`, `collective`, `graph`, `code_engine`, `model_routing`, `worktree`, `reranker`) have no evidence of production usage.
- **Configuration accretion:** 22 top-level config sections with merge, hierarchy, and file-watching -- for a single-user local agent.

### 3.2 Necessary vs. Unnecessary Feature Map

| Feature | LOC | Necessary? | Rationale |
|---------|-----|-----------|-----------|
| `agent` (core runtime) | ~8,000 | **Yes** | The product |
| `tool` (bash/file/http/browser) | 4,963 | **Yes** | Core capability |
| `channel` (TUI/Telegram) | 3,564 | **Yes** | User interface |
| `memory` (file+SQIite) | 4,832 | **Yes** | But simplify |
| `gateway` (wiring) | 4,350 | **Yes** | But needs decomposition |
| `sandbox` (Docker) | 845 | **Yes** | Security |
| `session` / `store` / `config` | 1,919 | **Yes** | Infrastructure |
| `agent/compression` | 1,500 | **Yes** | Context windows are finite |
| `agent/cognitive_loop` | 1,110 | **Yes** | Core product differentiator |
| `mcp` | 585 | **Yes** | Standard protocol |
| `knowledge` | 2,530 | **Conditional** | Overlaps with memory |
| `evolution` | 4,650 | **Questionable** | 0 known self-improvements shipped |
| `eval` | 6,477 | **Questionable** | Larger than runtime; should be a separate module |
| `agent/mcts_planner` | 729 | **No** | Experimental; no clear win over Planner |
| `agent/tree_planner` | 449 | **No** | Redundant with MCTSPlanner |
| `agent/autonomous_loop` | 543 | **No** | Speculative; no usage evidence |
| `agent/self_heal` | 552 | **No** | Complex fallback; 5 other error paths |
| `agent/debate` + `cognitive_debate` | 277 | **No** | Experimental |
| `agent/replay_*` | 622 | **No** | Debug feature; should be tooling |
| `agent/streaming_*` | 930 | **Maybe** | Could be consolidated into main loops |
| `taskledger` | 770 | **No** | Overlaps with session+agent task tracking |
| `cortex` | 630 | **No** | Wrapper around memory+knowledge |
| `collective` | 875 | **No** | Speculative multi-agent market |
| `a2a` | 944 | **No** | Speculative protocol |
| `wasm` | 693 | **No** | Zero known plugins |
| `code_engine` | 897 | **No** | Overlaps with agent/codebase_index.go |
| `worktree` | 555 | **No** | Shell command wrapper, not agent logic |
| `guardian` | 418 | **No** | Process supervision, not agent logic |
| `cogmetrics` | 650 | **Maybe** | Could be 150 lines with no loss |

**Estimated removable LOC:** 8,000-12,000 (12-18% of codebase) without losing any production capability.

---

## 4. Subsystem-by-Subsystem Analysis

### 4.1 agent (19,174 LOC) -- CRITICAL: 2/10

**Score: 2/10** (10 = clean, focused package)

**Problems:**
- 128 files is 3-5x too many for a single Go package. Go convention: packages should be focused concepts, not grab-bags.
- 218 structs means developers cannot hold the package's type system in their head.
- Contains: runtime loop (3 variants), LLM providers (2), execution backends (3), planning (5 approaches), compression (5-layer), streaming (5 files), sub-agents, team orchestration, assertions, self-healing, debate, hook integration, context management, speculative execution, checkpointing, replay, codebase indexing, prompt caching, token budgeting, circuit breaking, and event emission.
- `cognitive_loop.go` (1,110 lines, single file) mixes phase logic, reward computation, task registration, streaming control, and reflection.
- `openai.go` (671 lines) implements a full HTTP client, SSE parser, and tool-call accumulator inline -- should be a separate `openai/` subpackage.

**Recommendations:**
1. **Split into subpackages under `agent/`:**
   - `agent/runtime/` -- simple loop, cognitive loop (~2,500 LOC)
   - `agent/provider/` -- ClaudeProvider, OpenAIProvider, RetryProvider (~2,000 LOC)
   - `agent/planner/` -- Planner only (sunset MCTS, Tree, PlanMode) (~1,000 LOC)
   - `agent/compression/` -- 5-layer pipeline (~1,500 LOC)
   - `agent/subagent/` -- SubAgentManager, AgentManager, AgentTool (~3,000 LOC)
2. **Delete or extract:**
   - `autonomous_loop.go`, `self_heal.go`, `debate.go`, `cognitive_debate.go` -- remove (~1,500 LOC)
   - `replay.go`, `replay_engine.go`, `replay_sqlite.go` -- move to `cmd/ironclaw/debug/` (~622 LOC)
   - `mcts_planner.go`, `tree_planner.go` -- remove (~1,178 LOC)
3. **Consolidate streaming into main loops:** Embed streaming control directly in `cognitive_loop.go` and `simple_loop.go` instead of separate wrapper files (~930 LOC savings).
4. **Target:** agent/ shrinks to ~12,000 LOC across 5-6 subpackages.

### 4.2 eval (6,477 LOC) -- HIGH: 4/10

**Score: 4/10**

**Problems:**
- At 6,477 LOC, the eval harness is larger than the `tool` package (4,963 LOC) it evaluates.
- Contains its own runners (`cognitive_runner.go`, `longitudinal_runner.go`), classifiers, verifiers, judges, and dimensions -- essentially a mini-framework.
- `fixtures_self_learning.go` (519 lines), `fixtures.go` (337 lines), and 8 other fixture files suggest eval is testing itself more than the product.
- `learning_metrics.go` (382 lines) and `training_export.go` (365 lines) are meta-evaluation infrastructure.

**Recommendations:**
1. **Extract to `cmd/ironclaw/eval/` or a separate `eval/` top-level module.** Eval should not live in `internal/` -- it is tooling, not runtime.
2. **Remove meta-evaluation:** `learning_metrics.go`, `training_export.go`, `adaptive.go`, `longitudinal_runner.go` (~1,200 LOC) are research infrastructure, not product testing.
3. **Simplify to:** harness (run suites) + compare (regression detection) + fixtures (test data). Target: ~2,500 LOC.

### 4.3 evolution (4,650 LOC) -- HIGH: 3/10

**Score: 3/10**

**Problems:**
- 39 files for self-evolution -- the largest single "feature" investment after the core agent.
- Contains: optimizer, preference learner, genetic algorithm, prompt optimizer, skill synthesizer, skill activator, skill loader, brain (strategy store), ablation tester, safety gates, router, insights engine, trajectory system, reward computation, pattern detection.
- No evidence in the codebase of a single self-improvement that was shipped to users.
- `safety_gates.go` (202 lines) exists because the system knows it can produce harmful changes.
- Tight coupling to agent internals (`brain.go` references agent types).

**Recommendations:**
1. **Freeze as experimental.** Guard with a single `--experimental-evolution` flag (not a feature registry entry).
2. **Do not further invest** in genetic algorithms, ablation testing, or skill synthesis until there is demonstrated user value from the core optimizer.
3. **Extract to `experiment/evolution/`** top-level directory to signal its status clearly.
4. **Target:** Remove from hot path; keep code but stop active development.

### 4.4 memory + knowledge + cortex (7,992 LOC combined) -- MODERATE: 5/10

**Score: 5/10**

**Problems:**
- Three packages with overlapping retrieval responsibilities:
  - `memory`: file-first storage, SQLite FTS5+vector hybrid search, lifecycle management, user profiling
  - `knowledge`: document ingestion, BM25+vector hybrid retrieval, LLM reranker, entity/relation graph
  - `cortex`: unified retriever wrapping both, procedural memory, prompt injection
- `memory/file_store.go` (940 lines) does too much: file I/O, caching, search, lifecycle, index management.
- `memory/profiler.go` (649 lines) -- multi-section user profile with fact routing, buffering, LLM-based updates -- is a sub-product in itself.
- The `knowledge/graph/` subpackage (14 files) builds entity/relation triples with recursive CTE traversal -- a feature that would take an entire startup to build well.

**Recommendations:**
1. **Merge `cortex` into `memory`.** The unified retriever becomes `memory.UnifiedRetriever`. (~630 LOC absorbed, net -200 after dedup)
2. **Reduce knowledge to ingestion + retrieval.** Remove the graph subpackage (14 files, ~1,200 LOC) -- entity extraction is an LLM feature, not a storage feature.
3. **Simplify memory lifecycle:** Remove strength decay and auto-archival background tasks (~300 LOC). Users manage memory explicitly or not at all.
4. **Target:** memory + knowledge = ~5,000 LOC combined, down from 7,992.

### 4.5 gateway (4,350 LOC) -- HIGH: 5/10

**Score: 5/10**

**Problems:**
- `gateway.go` at 1,101 lines is a god-object constructor. The `New()` function wires 30+ subsystems sequentially.
- 33 files total suggests the package was used as a dumping ground for "wiring" code rather than being intentionally structured.
- `features.go` defines 19 features. The config→override mapping, persistence loading, runtime resolution, and lifecycle hook binding are spread across `gateway.go`, `features.go`, and `feature/`.

**Recommendations:**
1. **Extract wiring groups into `gateway/wire_*.go` files:**
   - `wire_core.go` -- DB, session, feature registry
   - `wire_agent.go` -- tool registry, LLM provider, runtime
   - `wire_memory.go` -- memory store, profiler, knowledge base
   - `wire_channels.go` -- Telegram, TUI, Discord
   - `wire_dashboard.go` -- bus, tracker, emitter, hub, HTTP
2. **Reduce `New()` to 150 lines** of sequenced delegation calls.
3. **Remove feature flags for dead code** (see Section 5).

### 4.6 tool (4,963 LOC) -- MODERATE: 7/10

**Score: 7/10**

**Problems:**
- 49 files for a tool registry is fragmentation. Many files are <150 lines (`file_list.go` 69 lines, `file_write.go` 91 lines).
- Interceptor subsystem (permission, hook, sandbox, audit, verify, trust) is 8 files totaling ~1,400 lines -- a lot of middleware for simple tool dispatch.
- `code_intel.go` (559 lines) overlaps with `agent/codebase_index.go` and `code_engine/`.

**Recommendations:**
1. **Merge small tool files:** `file_read.go` + `file_write.go` + `file_edit.go` + `file_list.go` + `file_patch.go` → `file_tools.go` (~800 LOC in 1 file vs. 5).
2. **Consolidate interceptors:** 8 files → 3 files (chain.go, permission.go, sandbox.go).
3. **Remove `code_intel.go`:** Either fold into `code_engine/` or delete if unused.
4. **Target:** ~3,500 LOC, 25 files.

### 4.7 Remaining Packages (Quick Scores)

| Package | LOC | Score | Key Issue |
|---------|-----|-------|-----------|
| `channel` | 3,564 | 7/10 | Reasonable; TUI could be simplified |
| `config` | 1,298 | 6/10 | Merge/hierarchy/watcher is complex for single-user tool |
| `hook` | 991 | 8/10 | Clean; focused |
| `sandbox` | 845 | 8/10 | Clean; well-scoped |
| `taskledger` | 770 | 4/10 | Overlaps with agent task tracking + session |
| `a2a` | 944 | N/A | Remove or extract to experiment/ |
| `collective` | 875 | N/A | Remove or extract to experiment/ |
| `wasm` | 693 | N/A | Remove or extract to experiment/ |
| `code_engine` | 897 | N/A | Merge with agent/codebase_index or remove |
| `worktree` | 555 | N/A | Extract to standalone tool |
| `guardian` | 418 | N/A | Extract to standalone tool |
| `cogmetrics` | 650 | 5/10 | Simplify to 150-line metrics collector |
| `mcp` | 585 | 8/10 | Clean protocol client |
| `feature` | 449 | 7/10 | Core; reduce feature count |
| `session` | 453 | 8/10 | Core; well-scoped |
| `store` | 168 | 9/10 | Minimal wrapper; ideal |

---

## 5. Top Simplification Candidates (Ranked)

### Rank 1: Split the agent package (Impact: VERY HIGH, Effort: HIGH)

**Current state:** 128 files, 19,174 LOC, 218 structs, 21 interfaces in a single Go package.

**What to do:**
1. Create subpackages: `agent/runtime/`, `agent/provider/`, `agent/planner/`, `agent/compression/`, `agent/subagent/`
2. Move files to their subpackage, add `internal/` import paths
3. Update `gateway/wire_agent.go` to import from subpackages

**Files affected:** ~100 files moved, ~20 files modified for import updates
**LOC savings:** 0 (reorganization), but unlocks all other simplifications
**Risk:** Medium -- import cycle risk; need careful dependency direction (subpackages must not import each other)

### Rank 2: Remove 5 experimental/speculative features (Impact: HIGH, Effort: LOW)

**What to remove:**
| Feature | Files | LOC | Rationale |
|---------|-------|-----|-----------|
| `wasm` | 8 | 693 | Zero known plugins; compile-time Wasm dependency complexity |
| `collective` | 8 | 875 | Agent market/consensus/reputation -- no users |
| `a2a` | 4 | 944 | Agent-to-agent protocol -- no remote agents |
| `code_engine` | 6 | 897 | Overlaps with `agent/codebase_index.go` (6,754 lines) |
| `cortex` | 5 | 630 | Wrapper around memory+knowledge; merge, don't wrap |

**Total removable:** 31 files, ~4,039 LOC
**Plus config:** Remove `wasm`, `collective`, `a2a`, `code_engine`, `graph`, `reranker` config sections
**Plus features:** Remove 8 feature registry entries
**Risk:** Low -- all are feature-gated; no production dependency

### Rank 3: Delete redundant planning systems (Impact: MEDIUM, Effort: LOW)

**What to remove:**
- `agent/mcts_planner.go` (729 lines) -- Monte Carlo Tree Search; experimental, no evidence of better plans
- `agent/tree_planner.go` (449 lines) -- Strategic tree search; redundant with MCTS
- `agent/plan_mode.go` (309 lines over Planner baseline) -- Pre-planning mode; concept overlaps with cognitive PLAN phase

**Keep:** `agent/plan.go` (Planner -- single LLM call, structured output)
**Total removable:** ~1,487 LOC
**Risk:** Low -- all are alternatives to the default Planner; no production dependency

### Rank 4: Remove speculative agent loop variants (Impact: MEDIUM, Effort: LOW)

**What to remove:**
- `agent/autonomous_loop.go` (543 lines) -- Discovery loop; no usage evidence
- `agent/self_heal.go` (552 lines) -- Complex error recovery; agent already has retry, circuit breaker, replan, and user approval
- `agent/debate.go` + `agent/cognitive_debate.go` (277 lines) -- Multi-agent debate; experimental

**Total removable:** ~1,372 LOC
**Risk:** Low -- all are experimental/alternative paths

### Rank 5: Consolidate streaming into main loops (Impact: MEDIUM, Effort: MEDIUM)

**Current state:** 5 streaming files (930 lines) wrapping cognitive phases with streaming-specific logic.

**What to do:** Embed streaming control (token accumulation, partial-output emission) directly in `cognitive_loop.go` and `simple_loop.go`. The streaming variants are thin wrappers (streaming_perceive.go is 89 lines); the logic belongs in the main loop methods.

**Total removable:** ~700 LOC (after embedding ~230 lines of essential streaming logic)
**Risk:** Medium -- streaming is user-visible; need careful testing

### Rank 6: Simplify memory system (Impact: MEDIUM, Effort: MEDIUM)

**What to do:**
1. Merge `cortex` into `memory` (UnifiedRetriever becomes `memory.UnifiedRetriever`)
2. Remove `knowledge/graph/` subpackage (entity extraction is LLM responsibility, not storage)
3. Remove strength-decay background tasks (auto-archival, consolidation)
4. Simplify profiler: single profile file, not multi-section with fact routing

**Total removable:** ~2,500 LOC
**Risk:** Medium -- memory is user-facing; need migration path for existing memory files

### Rank 7: Downsize eval to regression testing only (Impact: MEDIUM, Effort: MEDIUM)

**What to remove:**
- `learning_metrics.go` (382 lines) -- meta-evaluation
- `training_export.go` (365 lines) -- training data export
- `adaptive.go` (353 lines) -- adaptive testing
- `longitudinal_runner.go` (143 lines) -- long-running eval
- `classifier.go` (235 lines) -- result classification
- `dimension.go` (161 lines) -- multi-dimensional scoring

**Keep:** `harness.go`, `compare.go`, `taskset.go`, `judge.go`, fixtures
**Total removable:** ~1,639 LOC
**Risk:** Low -- these are research tools, not regression guards

---

## 6. Overlap and Redundancy Map

### 6.1 Planning Systems (5 implementations)

```
Planner (plan.go:314 lines)
  └── Single LLM call → structured TaskPlan JSON

MCTSPlanner (mcts_planner.go:729 lines)
  └── Monte Carlo Tree Search over plan space
  └── Uses Planner internally for node expansion

StrategicTreePlanner (tree_planner.go:449 lines)
  └── Tree-search with candidate scoring
  └── Separate from MCTS but same conceptual space

PlanMode (plan_mode.go:309 lines)
  └── Pre-execution plan generation with user review
  └── Overlaps with cognitive PLAN phase

TeamPlanner (taskledger/team_planner.go)
  └── LLM task decomposition for team coordination
  └── Uses same TaskPlan types from agent
```

**Verdict:** Keep Planner only. PlanMode can be a Planner option (preview flag). Team decomposition is a separate concern.

### 6.2 Execution Graphs (4 implementations)

```
cognitive_loop.go (1,110 lines)
  └── 5-phase: PERCEIVE → PLAN → ACT → OBSERVE → REFLECT
  └── Primary execution path for cognitive mode

simple_loop.go (~300 lines)
  └── Linear: system prompt → LLM → tool calls → repeat
  └── Primary execution path for simple mode

autonomous_loop.go (543 lines)
  └── Discovery loop: scan → identify opportunity → execute
  └── No integration with cognitive or simple loops

streaming_pipeline.go (240 lines)
  └── Wraps cognitive phases with streaming token emission
  └── Thin wrapper; logic should be in cognitive_loop.go

graph_engine.go + graph_nodes.go + graph_node_adapters.go (700 lines)
  └── DAG-based execution graph with pluggable nodes
  └── No consumer other than codebase_index.go
```

**Verdict:** Keep cognitive_loop and simple_loop. Embed streaming. Remove autonomous_loop and graph_engine (no consumers).

### 6.3 Memory/Retrieval (3 systems)

```
memory/ (4,832 LOC)
  └── File-first storage (Markdown + YAML frontmatter)
  └── SQLite FTS5 + vector hybrid search (RRF fusion)
  └── Lifecycle: strength decay, consolidation, archival
  └── User profiling: multi-section with fact routing

knowledge/ (2,530 LOC)
  └── Document ingestion pipeline
  └── BM25 + vector hybrid retrieval (separate from memory's)
  └── LLM reranker
  └── graph/ subpackage: entity/relation triples + CTE traversal

cortex/ (630 LOC)
  └── UnifiedRetriever wrapping memory + knowledge
  └── Procedural memory store
  └── Prompt injection (PromptSections)
```

**Overlap:** Three separate BM25+vector implementations. Two separate embedding pipelines. Two separate ingestion paths.
**Verdict:** Merge into single `memory/` package. Remove graph subpackage. Remove cortex (its UnifiedRetriever becomes `memory.UnifiedRetriever`).

### 6.4 Code Intelligence (2 systems)

```
agent/codebase_index.go (6,754 lines)
  └── Embedding-based code chunk indexing
  └── CodeChunk, IndexConfig, CodebaseIndex types
  └── Used in cognitive PERCEIVE phase

code_engine/ (897 LOC)
  └── Symbol index (symbol_index.go:420 lines)
  └── Call graph (call_graph.go:246 lines)
  └── Semantic search (semantic_search.go:231 lines)
  └── No consumer found in production code paths
```

**Verdict:** Keep codebase_index.go (it is used). Remove code_engine/ (no consumers, overlaps).

### 6.5 Agent Coordination (3 systems)

```
agent/subagent.go + agent/agent_manager.go + agent/agent_tool.go (~23,000 LOC)
  └── Sub-agent spawning, spec loading, result extraction
  └── Primary multi-agent mechanism

taskledger/ (770 LOC)
  └── SQLite task registry, team coordinator, worker pool
  └── Overlaps with agent's task tracking and sub-agent spawning

collective/ (875 LOC)
  └── Agent market, consensus, reputation, specialization
  └── Speculative; no integration with subagent or taskledger
```

**Verdict:** Keep subagent subsystem. Merge taskledger task registration into agent (it is already imported by agent). Remove collective.

---

## 7. The Minimal IronClaw Vision

### 7.1 What IronClaw Actually Is

Stripped of experimental features, IronClaw is:

> A local-first AI agent that connects to LLM providers, executes tools (bash, file, HTTP, browser) in a sandbox, persists memory to files, and exposes itself through TUI and Telegram channels.

That is ~30,000 lines of well-structured Go -- not 104,000.

### 7.2 Target Architecture

```
cmd/ironclaw/           -- CLI entry point, eval subcommand
internal/
  agent/
    runtime/            -- simple loop + cognitive loop (2,500 LOC)
    provider/           -- Claude + OpenAI providers (2,000 LOC)
    planner/            -- Planner only (1,000 LOC)
    compression/        -- 5-layer pipeline (1,500 LOC)
    subagent/           -- SubAgentManager + AgentManager (3,000 LOC)
    types.go            -- Shared types: TaskPlan, ReflectionResult, etc.
  tool/                 -- Tool registry + implementations (3,500 LOC)
  memory/               -- File storage + SQLite index + retriever (4,000 LOC)
  channel/
    tui/                -- Bubble Tea TUI
    telegram/           -- Telegram bot
  gateway/              -- Wiring (2,000 LOC, decomposed)
  sandbox/              -- Docker sandbox (845 LOC)
  session/              -- Session management (453 LOC)
  store/                -- SQLite wrapper (168 LOC)
  config/               -- Simplified config (800 LOC)
  mcp/                  -- MCP protocol (585 LOC)
  feature/              -- Feature registry (350 LOC, <10 features)
  hook/                 -- Hook system (991 LOC)
  skill/                -- SKILL.md loader (417 LOC)

experiment/             -- NOT compiled by default
  evolution/            -- Self-evolution (gated by build tag)
```

**Target totals:** ~25,000 production LOC, ~15 packages, largest package <4,000 LOC.

### 7.3 What Gets Cut

| Category | LOC Removed | Mechanism |
|----------|-------------|-----------|
| Experimental features (wasm, collective, a2a, code_engine) | 4,039 | Delete or move to experiment/ |
| Redundant planning (MCTS, Tree, partial PlanMode) | 1,487 | Delete |
| Speculative loops (autonomous, self_heal, debate) | 1,372 | Delete |
| Streaming wrappers (consolidated into main loops) | 700 | Consolidate |
| Memory bloat (cortex merge, graph removal, lifecycle simplification) | 2,500 | Simplify |
| Eval downsizing | 1,639 | Remove research infra |
| Replay subsystem (move to debug tool) | 622 | Extract |
| Agent package split (structural, no LOC change) | 0 | Reorganize |
| Gateway decomposition (structural, no LOC change) | 0 | Reorganize |
| **Total** | **~12,000** | |

---

## 8. Phased Implementation Roadmap

### Phase 1: Safe Removals (Week 1-2)

**Goal:** Remove code with zero production dependency. No behavior change.

1. **Delete `wasm/` package** -- Remove directory, feature entry, config section. Update `gateway.go` to remove `wasm_plugins` feature check.
2. **Delete `collective/` package** -- Same process.
3. **Delete `a2a/` package** -- Same process.
4. **Delete `code_engine/` package** -- Same process.
5. **Remove from feature registry:** `wasm_plugins`, `collective`, `a2a`, `graph`, `code_engine`, `model_routing`, `reranker`.

**Deliverable:** -4,039 LOC. All tests pass. No feature flags reference removed code.

### Phase 2: Planning and Loop Consolidation (Week 3-4)

**Goal:** Remove redundant agent internals. Behavior change: fewer planning options.

1. **Delete `mcts_planner.go`, `tree_planner.go`** -- Remove files, update imports. Default to `Planner` everywhere.
2. **Simplify `plan_mode.go`** -- Make plan preview a boolean option on Planner, not a separate system.
3. **Delete `autonomous_loop.go`, `self_heal.go`, `debate.go`, `cognitive_debate.go`** -- Remove files.
4. **Consolidate streaming** -- Move streaming logic from `streaming_*.go` into `cognitive_loop.go` and `simple_loop.go` methods. Delete 5 streaming wrapper files.
5. **Delete replay subsystem** -- Move `replay.go`, `replay_engine.go`, `replay_sqlite.go` to `cmd/ironclaw/debug/replay.go` (not compiled into binary by default).

**Deliverable:** -3,559 LOC. Cognitive loop is single-file (plus types). Simple loop is single-file.

### Phase 3: Agent Package Split (Week 5-6)

**Goal:** Structural reorganization. No behavior change. Highest risk phase.

1. **Create subpackage directories:** `agent/runtime/`, `agent/provider/`, `agent/planner/`, `agent/compression/`, `agent/subagent/`
2. **Move files to subpackages:**
   - `agent/runtime/`: cognitive_loop.go, simple_loop.go, agent.go, act.go, observe.go, perceive.go, plan.go, reflect.go, cognitive_types.go, cognitive_options.go, cognitive_prompts.go, loop_strategy.go
   - `agent/provider/`: claude_provider.go, openai.go, provider.go, retry.go, tokenizer.go, prompt_cache.go
   - `agent/planner/`: plan.go (remaining), plan_mode.go (simplified)
   - `agent/compression/`: compression.go, compaction.go, context_manager.go, context.go, context_budget.go, token_budget.go, context_builder.go, circuit_breaker.go
   - `agent/subagent/`: subagent.go, subagent_result.go, subagent_context.go, agent_manager.go, agent_tool.go, agent_mcp.go, team_manager.go, team_task.go, team_message.go, spec.go, task_context.go
3. **Resolve import cycles:** Ensure subpackages only import downward (planner imports provider, runtime imports planner+provider, etc.)
4. **Update `gateway/` imports** to reference subpackages.

**Deliverable:** agent/ shrinks from 128 files to ~80 across 5 subpackages. Maximum subpackage size <4,000 LOC.

### Phase 4: Memory and Knowledge Simplification (Week 7-8)

**Goal:** Merge overlapping systems. Behavior change: simplified memory lifecycle.

1. **Merge `cortex/` into `memory/`** -- `UnifiedRetriever` becomes `memory.UnifiedRetriever`. `ProceduralStore` becomes `memory.ProceduralStore`.
2. **Remove `knowledge/graph/` subpackage** -- 14 files. Entity extraction is an LLM feature, not storage.
3. **Simplify memory lifecycle** -- Remove auto-archival background task, strength decay computation, consolidation logic. Keep explicit user-triggered operations.
4. **Simplify profiler** -- Single profile file (`user/profile.md`) instead of multi-section with fact routing.
5. **Merge `knowledge/ingest/` into `knowledge/`** -- Flatten package.

**Deliverable:** memory + knowledge = ~5,000 LOC (from 7,992). Single retrieval path.

### Phase 5: Gateway and Eval Cleanup (Week 9-10)

**Goal:** Decompose gateway wiring. Downsize eval.

1. **Split `gateway.go`** into `wire_core.go`, `wire_agent.go`, `wire_memory.go`, `wire_channels.go`, `wire_dashboard.go`.
2. **Reduce `New()`** to sequence of delegation calls.
3. **Remove eval research infrastructure** -- `learning_metrics.go`, `training_export.go`, `adaptive.go`, `longitudinal_runner.go`, `classifier.go`, `dimension.go`.
4. **Extract eval to top-level** `eval/` directory (not `internal/eval/`).

**Deliverable:** gateway.go <200 lines. eval/ <3,000 LOC (regression-only).

### Phase 6: Tool and Config Simplification (Week 11-12)

**Goal:** Consolidate tool files. Simplify config.

1. **Merge tool files:** `file_read.go` + `file_write.go` + `file_edit.go` + `file_list.go` + `file_patch.go` → `file_tools.go`.
2. **Merge browser files:** `browser_search.go` + `browser_extract.go` + `browser.go` → `browser_tools.go`.
3. **Consolidate interceptors:** 8 files → 3.
4. **Remove `code_intel.go`** from tool package (overlaps with agent/codebase_index.go).
5. **Simplify config:** Remove hierarchy merging, reduce to flat YAML with env var expansion. Single file: `~/.ironclaw/config.yaml`.

**Deliverable:** tool/ <3,500 LOC (25 files). config/ <800 LOC (8 files).

---

## 9. Risks and Mitigations

### Risk 1: Breaking Import Cycles During Agent Split

**Severity:** HIGH
**Likelihood:** MEDIUM
**Mitigation:**
- Define interface direction before moving code. Runtime imports Planner interface, not concrete type.
- Use `agent/types.go` as a shared types package that all subpackages can import without cycles.
- Run `go vet` and compilation after each subpackage move.
- If a cycle emerges between runtime and subagent, extract a shared interface to types.go.

### Risk 2: Removing Features Users Depend On

**Severity:** HIGH
**Likelihood:** LOW
**Mitigation:**
- All removal candidates in Phase 1 are feature-gated and default-disabled. No production user has them enabled.
- For Phase 2 removals (MCTS, autonomous loop), check git history for any config file that references them.
- Add deprecation warnings one release before removal.
- Keep removed code in git history for recovery.

### Risk 3: Memory Migration Breaking Existing Data

**Severity:** MEDIUM
**Likelihood:** MEDIUM
**Mitigation:**
- Write migration tool that reads old memory files and writes simplified format.
- Keep backward-compat reader for 2 releases.
- Add `--memory-migrate` command for explicit user invocation.

### Risk 4: Streaming Consolidation Causing Visual Regressions

**Severity:** MEDIUM
**Likelihood:** MEDIUM
**Mitigation:**
- Streaming is user-visible; add TUI screenshot tests before consolidation.
- Run full eval suite after consolidation to catch behavioral differences.
- Keep old streaming code on a branch for A/B comparison.

### Risk 5: Evolution Removal Angering Power Users

**Severity:** LOW
**Likelihood:** LOW
**Mitigation:**
- Evolution is not being removed -- it is being moved to `experiment/evolution/` behind a build tag.
- Power users who want it can build with `-tags evolution`.
- This signals clearly that it is experimental.

---

## 10. Success Metrics

### 10.1 Quantitative Metrics

| Metric | Current | Target (Post-Phase 6) | Measurement |
|--------|---------|----------------------|-------------|
| Total production LOC | 64,798 | <48,000 | `find internal -name '*.go' ! -name '*_test.go' -exec cat {} + \| wc -l` |
| Internal packages | 37 | <22 | `find internal -type d \| wc -l` |
| Largest package LOC | 19,174 (agent) | <4,000 | `find internal/<pkg> -name '*.go' ! -name '*_test.go' -exec cat {} + \| wc -l` |
| Agent package files | 128 | <80 (across 5 subpackages) | `find internal/agent -name '*.go' ! -name '*_test.go' \| wc -l` |
| gateway.go LOC | 1,101 | <200 | `wc -l internal/gateway/gateway.go` |
| Feature flags | 19 | <10 | `grep -c 'Name:' internal/gateway/features.go` |
| Planning systems | 5 | 1 | Manual count |
| Memory backends | 3 | 1 | Manual count |
| Test-to-production ratio | 0.56 | >0.60 | Test LOC / Production LOC |
| Build time (clean) | Baseline | -20% | `time go build -tags fts5 ./cmd/ironclaw/` |
| Binary size | Baseline | -15% | `ls -lh ironclaw` |

### 10.2 Qualitative Metrics

| Metric | Measurement |
|--------|-------------|
| New developer onboarding time | Time to first meaningful PR (target: <4 hours reading code) |
| Bug fix cycle time | Time from bug report to merged fix (target: <20% overhead from navigation) |
| Feature addition cost | Lines changed per feature added (target: <200 lines, down from typical 500+) |
| Code review effectiveness | Reviewer can understand change without reading >3 files (target: 90% of PRs) |

### 10.3 Gates Per Phase

Each phase must pass before the next begins:
1. **Phase 1:** All existing tests pass. `go build` succeeds. No grep hits for removed packages.
2. **Phase 2:** Full eval suite passes. Cognitive loop behavior unchanged (manual smoke test).
3. **Phase 3:** No import cycles (`go vet ./...`). All subpackage tests pass. Gateway wiring unchanged.
4. **Phase 4:** Memory migration tool works on real user data. Search results identical.
5. **Phase 5:** Gateway `New()` returns identical `*Gateway`. Eval regression suite passes.
6. **Phase 6:** All tool tests pass. Config loading backward-compatible.

---

## Appendix A: File Hit List (Complete)

### Files to DELETE (safe removals, Phase 1-2)

```
internal/wasm/*.go                          (8 files, 693 LOC)
internal/collective/*.go                    (8 files, 875 LOC)
internal/a2a/*.go                           (4 files, 944 LOC)
internal/code_engine/*.go                   (6 files, 897 LOC)
internal/agent/mcts_planner.go              (729 LOC)
internal/agent/tree_planner.go              (449 LOC)
internal/agent/autonomous_loop.go           (543 LOC)
internal/agent/self_heal.go                 (552 LOC)
internal/agent/debate.go                    (190 LOC)
internal/agent/cognitive_debate.go          (87 LOC)
internal/agent/streaming_execute.go         (141 LOC)
internal/agent/streaming_observe.go         (106 LOC)
internal/agent/streaming_perceive.go        (89 LOC)
internal/agent/streaming_pipeline.go        (240 LOC)
internal/agent/streaming_plan.go            (225 LOC)
internal/agent/streaming_reflect.go         (129 LOC)
internal/agent/graph_engine.go             (170 LOC)
internal/agent/graph_nodes.go              (149 LOC)
internal/agent/graph_node_adapters.go      (381 LOC)
internal/agent/graph_types.go              (119 LOC)
internal/agent/replay.go                   (283 LOC)
internal/agent/replay_engine.go            (167 LOC)
internal/agent/replay_sqlite.go            (172 LOC)
internal/agent/aggregator.go               (70 LOC)
internal/knowledge/graph/*.go              (14 files, ~1,200 LOC)
internal/cortex/*.go                        (5 files, 630 LOC)
internal/tool/code_intel.go                (559 LOC)
```

### Files to CONSOLIDATE (Phase 5-6)

```
Merge: internal/tool/file_read.go + file_write.go + file_edit.go
       + file_list.go + file_patch.go
  → internal/tool/file_tools.go

Merge: internal/tool/browser_search.go + browser_extract.go + browser.go
  → internal/tool/browser_tools.go

Merge: internal/tool/interceptor.go + interceptor_permission.go
       + interceptor_hook.go (→ consolidated into sandbox dispatch)
  → internal/tool/interceptor_chain.go

Merge: internal/cortex/*.go
  → internal/memory/unified_retriever.go, internal/memory/procedural.go

Merge: internal/knowledge/ingest/*.go
  → internal/knowledge/ingest.go (flat)
```

### Files to EXTRACT (Phase 3)

```
internal/agent/*.go → agent/runtime/, agent/provider/,
                      agent/planner/, agent/compression/,
                      agent/subagent/

internal/gateway/gateway.go → gateway/wire_core.go, gateway/wire_agent.go,
                               gateway/wire_memory.go, gateway/wire_channels.go,
                               gateway/wire_dashboard.go

internal/eval/*.go → eval/ (top-level, not internal/)

internal/evolution/*.go → experiment/evolution/ (build tag gated)
```

---

## Appendix B: Feature Registry Cleanup

### Features to REMOVE

| Feature Name | Reason |
|-------------|--------|
| `wasm_plugins` | Zero plugins; compile-time complexity |
| `a2a` | No remote agents |
| `graph` | No consumers after graph_engine removal |
| `code_engine` | Overlaps with agent/codebase_index.go |
| `model_routing` | Unused; single-model deployments are the norm |
| `reranker` | LLM reranker unused in production path |
| `worktree` | Standalone tool, not agent feature |

### Features to KEEP

| Feature Name | Reason |
|-------------|--------|
| `memory` | Core |
| `skills` | Core |
| `multi_agent` | Sub-agent spawning is used |
| `team` | Team coordination is used |
| `speculative` | Performance optimization |
| `scheduler` | Cron-like scheduling |
| `knowledge` | Document retrieval |
| `knowledge_graph` | Remove (see above) |
| `sandbox` | Security |
| `evolution` | Move to build tag |
| `dashboard` | Web UI |
| `server` | HTTP API |
| `mcp_*` | Per-configured-server |

**Result:** 10 features (down from 19+dynamic MCP).

---

*End of report. Total estimated simplification: ~12,000 LOC removed, 15 packages consolidated, 9 features retired, 5 planning systems reduced to 1, 3 memory backends merged to 1, agent package decomposed into 5 focused subpackages. Target: 12 weeks, phased with gates.*
