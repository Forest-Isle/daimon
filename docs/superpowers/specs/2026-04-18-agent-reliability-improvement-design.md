# IronClaw Agent Reliability Improvement Design

**Date**: 2026-04-18  
**Status**: Approved  
**Scope**: Three parallel improvement tracks to close the gap with frontier agent projects (Claude Code, Hermes Agent)

---

## Overview

IronClaw already has a sophisticated architecture: cognitive 5-phase loop, RL/evolution engine, knowledge graph, hybrid retrieval, and multi-agent orchestration. The identified gaps are:

1. **Long-task reliability** — tasks fail mid-way with no recovery mechanism
2. **Tool output quality** — bash returns raw strings; browser lacks structured extraction
3. **Context intelligence** — Perceiver uses pure heuristics; no project/git awareness

These three tracks are independent and can be developed in parallel.

---

## Track A: Long-Task Reliability

### A1. Task Checkpoints (Checkpoint/Resume)

**Problem**: Task interruption requires full restart. The scheduler triggers tasks but does not persist execution state.

**Design**:
- New SQLite table `task_checkpoints`: `(id, session_id, subtask_index, observations_json, plan_json, created_at)`
- `CognitiveAgent` writes a checkpoint after each SubTask completes (using existing `db *store.DB`)
- New slash command `/resume <session_id>` restores execution from the last checkpoint
- Lifecycle: auto-cleared on task success; TTL 7 days for abandoned tasks

**Interface**:
```go
type CheckpointStore interface {
    Save(ctx context.Context, cp *TaskCheckpoint) error
    Load(ctx context.Context, sessionID string) (*TaskCheckpoint, error)
    Delete(ctx context.Context, sessionID string) error
}
```
`SQLiteCheckpointStore` implements this and is injected into `CognitiveAgent`.

**Files affected**: `internal/agent/cognitive.go`, `internal/store/migrations/`, new `internal/agent/checkpoint.go`

---

### A2. Structured Verification (Assertion Loop)

**Problem**: The OBSERVE phase only counts successes/failures. Tool results are not verified against expected outcomes. A file write that silently fails is reported as success.

**Design**:
- Add `Assertions []AssertionResult` to `ObservationResult`
- Observer auto-generates assertions based on tool type:
  - `file_write` / `file_edit` → verify file exists and contains expected content
  - `bash` → check `exit_code == 0`, stderr has no error keywords
  - `http` → check `status_code < 400`
- Assertion failures are passed as `FailureContext` into REFLECT, triggering targeted re-plan
- Assertions run as local checks (no LLM call)

**Files affected**: `internal/agent/observe.go`, `internal/agent/cognitive_types.go`

---

### A3. Context-Aware Smart Retry

**Problem**: `retry.go` handles API-level network errors only. Tool execution failures enter re-plan without structured failure context, so the LLM re-plans blind.

**Design**:
- New `FailureContext` struct: `{SubTask, ToolName, ErrorType, ErrorMsg, AttemptCount, LastObservation}`
- REFLECT phase serializes `FailureContext` into re-plan prompt: explicitly tells the LLM which step failed, why, and how many times
- Tiered retry strategy: after 3 same-type failures, degrade to more conservative tool (e.g. `file_write` instead of `bash echo`)

**Files affected**: `internal/agent/reflect.go`, `internal/agent/cognitive_types.go`, `internal/agent/cognitive_prompts.go`

---

## Track B: Tool Quality

### B1. Structured Bash Output

**Problem**: bash tool returns raw string. LLM must self-parse exit code / stderr, leading to false-positive success detection.

**Design**:
- bash tool returns structured JSON: `{stdout, stderr, exit_code, truncated, duration_ms, status}`
- `status` is `"ok"` or `"failed"` based on exit_code — explicit signal for Observer assertions
- Output > 8KB auto-written to temp file; path returned instead of inline content (saves context tokens)
- Timeout causes `truncated: true` with partial output

**Files affected**: `internal/tool/bash.go`

---

### B2. Browser Tool Enhancement

**Problem**: Current browser tool returns raw HTML/content. No structured search or clean article extraction.

**Design**:
- New `browser_search` tool: query → `[{title, url, snippet}]` structured list
- New `browser_extract` tool: URL → Readability-style Markdown (strips nav/ads/boilerplate)
- Existing `browser` tool retained as raw mode for cases needing full page
- Both new tools support `page` parameter for paginated long content

**Files affected**: new `internal/tool/browser_search.go`, `internal/tool/browser_extract.go`, `internal/tool/tool.go` (registration)

---

### B3. Tool Result Cache

**Problem**: Repeated reads of the same file within one task consume redundant tokens and can cause apparent content inconsistencies.

**Design**:
- `ToolResultCache` in Executor layer: key = `{tool_name, sha256(input)}`, TTL = task lifetime
- Only caches `IsReadOnly() == true` tools: `file_read`, `file_list`, `http` GET
- Cache hit annotated as `cached: true` in observation for debugging
- Automatic invalidation: when a write tool targets a path, all cached reads for that path are evicted (using existing `ExtractPaths`)

**Files affected**: new `internal/agent/tool_cache.go`, `internal/agent/act.go`

---

## Track C: Context Intelligence

### C1. Project Context Auto-Injection

**Problem**: Perceiver uses pure heuristics with no awareness of the current project. Every task wastes tool calls discovering basic facts (language, build commands, directory structure).

**Design**:
- New `ProjectContextScanner` scans working directory at PERCEIVE time:
  - Detects: `CLAUDE.md`, `README.md`, `Makefile`, `package.json`, `go.mod`, `pyproject.toml`, `Cargo.toml`
  - Extracts: project name, language, build commands, key directories
- Result injected as `{{PROJECT_CONTEXT}}` into Plan prompt (parallel to existing `{{KNOWLEDGE}}`)
- Scan result cached in memory; invalidated on file modification

**Files affected**: new `internal/agent/project_scanner.go`, `internal/agent/perceive.go`, `internal/agent/cognitive_prompts.go`

---

### C2. Git State Awareness

**Problem**: Agent does not know current branch or uncommitted changes, leading to code tasks executed in wrong context.

**Design**:
- New `GitContextProvider` collects at PERCEIVE time: `current_branch`, `uncommitted_files[]`, `recent_commits[5]`
- Injected into `CognitiveState.ProjectContext`
- Appended to Plan prompt header for code-related tasks
- Builds on existing `hook/injector_git.go` rather than reimplementing

**Files affected**: new `internal/agent/git_context.go`, `internal/agent/perceive.go`, `internal/agent/cognitive_types.go`

---

### C3. Dynamic Context Budget

**Problem**: Memory/knowledge injection uses a fixed strategy regardless of task complexity, wasting tokens on simple tasks or truncating important context on complex ones.

**Design**:
- `ContextBudgetAllocator` reads Perceiver's existing `complexity` classification (simple/moderate/complex) and allocates accordingly:
  - `simple` → memory top-3 + project context only
  - `moderate` → memory top-5 + KB top-3 + project context
  - `complex` → memory top-10 + KB top-5 + knowledge graph + project context + git state
- Built on existing `token_budget.go`
- Each source has a soft cap; overrun content truncated by relevance score, not position

**Files affected**: new `internal/agent/context_budget.go`, `internal/agent/context.go`, `internal/agent/perceive.go`

---

## Implementation Order

| Phase | Track | Items | Rationale |
|-------|-------|-------|-----------|
| 1 | A | A2 (Assertions) + B1 (Structured Bash) | Highest impact on reliability, low risk |
| 2 | A | A3 (Smart Retry) + C1 (Project Context) | Builds on A2; C1 independent |
| 3 | B | B2 (Browser) + B3 (Cache) + C2 (Git) | Parallel; B3 needs B1 done first |
| 4 | A | A1 (Checkpoint) + C3 (Budget) | Most complex; benefits from all prior work |

## Non-Goals

- Rewriting the RL/evolution engine (already advanced)
- Multi-modal image input (separate concern)
- New channel integrations
- Changing the cognitive loop structure
