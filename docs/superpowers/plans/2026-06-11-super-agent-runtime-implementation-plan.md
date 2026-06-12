# Super Agent Runtime Implementation Plan

Date: 2026-06-11

This plan tracks concrete implementation slices for the super-agent runtime
architecture. Each slice must include focused tests and at least one broader
verification command before it is considered complete.

## P0 Runtime Foundation

- [x] Add architecture baseline for the digital-life runtime.
- [x] Preserve `OnUserMessage` hook context through the real provider request.
- [x] Route approval and activity notifications by session ID.
- [x] Register configured sub-agent specs as `agent_*` tools.
- [x] Add capability-aware, bounded tool scheduling.
- [x] Pass real tool capabilities into permission evaluation.
- [x] Make stream/provider failures propagate as real turn errors.
- [x] Verify with targeted package tests and full `go test -tags fts5 ./...`.
- [x] Verify with `go vet -tags fts5 ./...`.

## P1 PromptFrame and Context Engineering

- [x] Promote `PromptFrame` from a base string to explicit ordered layers.
- [x] Model static/session/turn/iteration/ephemeral layer scopes.
- [x] Preserve a stable dynamic/cache boundary.
- [x] Render current plan as an iteration layer every model call.
- [x] Render hook additional context as a turn layer without mutating base state.
- [x] Add tests for layer ordering, dynamic plan updates, and hook context.
- [x] Add token/context metrics for prompt-frame rendering.

## P2 Tool OS

- [x] Add deferred tool catalog for large MCP/plugin tool sets.
- [x] Add a `ToolSearch` tool for schema discovery.
- [x] Keep always-core tools eagerly available.
- [x] Add tests proving deferred tools are hidden until resolved.
- [x] Add read-before-edit state tracking for file edit tools.
- [x] Add stronger tool-result persistence references in prompt context.

Note: existing MCP server tools remain eagerly registered until a dedicated MCP
lazy-registration migration can preserve connection lifecycle and hot-reload
semantics.

## P3 Permission Kernel and Sandboxing

- [x] Split policy profiles by channel class: local, remote, scheduled,
  background.
- [x] Add durable approval decisions with audit metadata.
- [x] Add shell execution backend interface: host first, sandbox/container later.
- [x] Add command parsing stronger than substring policy for dangerous shell use.
- [x] Verify remote/scheduled tasks cannot silently bypass destructive approval.

Note: sandbox/container execution is represented by the `ShellBackend`
interface; the current implementation remains host bash until a dedicated
sandbox backend is added.

## P4 Memory OS

- [x] Route prompt memory injection through `UnifiedRetriever`.
- [x] Record verified successful strategies into procedural memory.
- [x] Add autobiographical decision memory APIs.
- [x] Add contradiction handling and temporal validity metadata.
- [x] Add memory audit and deletion verification.

## P5 Task Runtime

- [x] Introduce task ledger objects: goal, state, evidence, next action.
- [x] Connect scheduler runs to task ledger entries.
- [x] Add wakeup/resume metadata for interrupted turns.
- [x] Add background agent attach/cancel/status commands.
- [x] Verify long-running tasks can be inspected and resumed.

## P6 Workflow Engine

- [x] Add deterministic workflow spec format.
- [x] Implement pipeline-first execution with optional parallel barriers.
- [x] Add structured sub-agent outputs and replay cache keys.
- [x] Add budget tracking per workflow run.
- [x] Verify workflow replay avoids rerunning unchanged completed steps.

## P7 Telemetry and Evals

- [x] Add structured events/spans for prompt-frame render, model call, tool call,
  permission decision, compact, reflection, and task transitions.
- [x] Add local trace exporter first; OpenTelemetry exporter later.
- [x] Add eval cases for multi-step edit, denied tool, compact/resume, subagent,
  scheduler, and workflow paths.
- [x] Add regression gate for agent behavior changes.
