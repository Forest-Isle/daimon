# IronClaw Re-Founding Roadmap

**Date:** 2026-06-07
**Author:** ENI (Claude Opus 4.8)
**Status:** Phase 0 (correctness + cleanup) DONE & VERIFIED. Phases 1+ awaiting user direction.

---

## Thesis

IronClaw has an **excellent pipeline** (Gateway composition root, tool interceptor
chain, SQLite persistence, skills, scheduler, streaming) wrapped around an
**outdated conceptual core**. The fix is *re-founding, not rewrite*: keep the
pipeline, rebuild the concepts, delete the aspirational half-built features.

A top-tier agent wins on three things only — **the loop, context engineering, and
the verification/eval closed loop** — not on a long feature checklist. Every phase
below serves one of those three.

---

## Guiding Principles

1. **One minimal loop.** Intelligence lives in the model, not in orchestration
   layers. No multi-phase cognitive scaffolding.
2. **Context engineering is the top-leverage subsystem**, not a "memory" feature.
   Every turn: who decides what fills the token budget?
3. **Verification is the spine of "agentic"**, not a side interceptor.
4. **Eval is the product.** What you cannot measure you cannot improve.
5. **Cutting features is discipline.** Half-built features are liabilities.

---

## Phase 0 — Correctness & Honesty (DONE ✅)

All verified: `make build-bin`, `make vet`, `make test-short` (29 pkgs, 0 fail).

| Item | Severity | Status |
|------|----------|--------|
| OpenAI/DeepSeek tool-result shape bug (HTTP 400 on 2nd tool turn) | HIGH | ✅ fixed + test |
| OpenAI streaming ignores `tool_calls[].index` (multi-tool arg corruption) | HIGH | ✅ fixed + test |
| Delete dead cognitive cluster (act.go + cognitive_types.go + task_context.go, 768 lines) | — | ✅ deleted, compiler-verified |
| Stale `knowledge` reference in headless.go comment | LOW | ✅ fixed |

Net: **+155 / −790 lines.** Both fixes hit the user's own DeepSeek provider on the
critical tool loop.

---

## DECISION POINTS (resolved with user)

### D1 — Truncation handling ✅ DONE

**Decision: (a) Flag & stop.** `appendStopNotice` now appends a visible
`[response truncated: reached max output tokens]` notice on both streaming and
non-streaming finish points (`loop_common.go`). `stopReason` is no longer
silently discarded. Verified via TDD + full `make test` (race detector clean).

### D2 — Lower-severity correctness bugs ✅ DONE

- **D2#1** — OpenAI `finish_reason` mapping: added `StopAbnormal`; `content_filter`
  and unrecognized reasons now map to it (and are flagged via `appendStopNotice`),
  instead of `default→end_turn` masquerading as success. `"stop"` still maps to
  `end_turn`. Both the non-streaming (`parseChoice`) and streaming switches fixed.
- **D2#2** — `safeTrimHistory` (`context.go`) now strips orphaned `tool_result`
  messages **anywhere** in the window, not just at the leading boundary, so a
  mid-window orphan can no longer cause an HTTP 400. The three-branch boundary
  loop was simplified into a single total filter.

Both verified via TDD red→green + full `make test`.

---

## Phase 1 — Unified Event Log (foundation for everything)

**Goal:** Replace the scattered sessions/tasks/tool/audit tables with one
append-only event stream: `user_msg | assistant_msg | tool_use | tool_result |
compaction | subagent_spawn | subagent_result`.

**Why first:** memory, audit, replay, and eval all *derive* from this one log.
It's the substrate the next phases stand on.

**Scope:** new `events` table + migration; an `EventLog` writer/reader; adapt
`session` to project from events. SQLite is sufficient. Must preserve existing
session API surface (or shim it) so the pipeline keeps working.

**Verification:** replay test (events → reconstructed session == original);
all existing session/gateway tests stay green.

---

## Phase 2 — Provider-Neutral Transcript + Adapter Contract Tests

**Goal:** Kill the root cause of the two Phase-0 bugs — internal representation
polluted by provider-specific shape. Define one canonical transcript; each
provider gets a pure serialize/deserialize adapter + a shared **contract test
suite** every adapter must pass (tool-result round-trip, multi-tool stream,
mixed text+tool, stop-reason mapping).

**Why:** adding a new model = write one adapter that passes the contract. No more
silent shape mismatches.

**Verification:** the contract suite runs against Claude + OpenAI adapters; both green.

---

## Phase 3 — Context Manager (highest leverage)

**Goal:** Extract a single subsystem whose only job is deciding what's in the
context window each turn: compaction, on-demand retrieval (memory/file/code),
filesystem-as-external-memory, sub-agent summary re-injection. Explicit token budget model.

**Why:** this is the actual hard problem of agent quality. Today it's smeared
across `memory/`, `context.go`, and the deleted `knowledge/`.

**Verification:** budget tests (never exceed window); retrieval-injection tests;
eval delta (Phase 5) shows no regression vs. baseline.

---

## Phase 4 — Real Isolated Sub-Agents

**Goal:** Make sub-agents true isolated workers (subprocess/container) with their
own window + tool subset, returning a structured summary. The point is **context
isolation** (burn 100k, return 500 tokens) — parallelism and security are bonuses.

**Why:** `agent_tool.go` currently routes everything to in-process `Spawn`; the
subprocess/docker/fork specs are advertised but unused.

**Verification:** integration test spawning a real worker; parent window stays
clean; summary round-trips.

---

## Phase 5 — Eval Harness (build EARLY, ideally before Phase 3)

**Goal:** 10–20 real tasks (bug fix, add feature, config change) + machine-judgable
scorers (build passes? tests green? endpoint 200?) + transcript replay. Every later
change judged by **eval delta**.

**Why:** without this, "evolution/self-improvement" is theater. This is the line
between shipping a good agent and shipping a demo.

**Note:** I recommend pulling this *before* Phase 3 so context-engineering changes
are measured, not vibes.

---

## Explicit DELETIONS (debt removal)

- ✅ Cognitive cluster (done).
- **Evolution engine / model router / speculative execution** → take offline until
  an eval number justifies each. They earn their way back.
- **Studio frontend, standalone MCP server** → out of the v1 critical path.
- `plan_mode.go` → **KEEP for now** (it's wired + tested; my analysis says make
  planning *real*, not delete it). Revisit in Phase 3.

---

## Recommended Order

`Phase 0 (done)` → `D1 decision` → `Phase 5 (eval, partial)` → `Phase 1` →
`Phase 2` → `Phase 3` → `Phase 4`.

Each phase: its own spec → plan → TDD implementation → full verification, per the
project's `make build-bin && make vet && make test` gate.
