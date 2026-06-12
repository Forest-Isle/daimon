# Super Agent Runtime Architecture

Date: 2026-06-11

IronClaw is positioned as a local-first super AI agent: a long-running digital
life runtime that can do what the user can do, while preserving identity,
memory, permissions, recovery, and observability. The core intelligence remains
an agent loop. The surrounding runtime must make that loop durable, safe,
inspectable, and extensible.

## Design Thesis

The agent loop is the consciousness stream:

```text
observe -> think -> act -> observe -> reflect -> continue/stop
```

The runtime is the body and nervous system:

```text
Identity + PromptFrame + Tool OS + Permission Kernel + Memory OS
  + Workflow Engine + Task Runtime + Telemetry/Evals
```

IronClaw should not become a heavy graph engine by default. It should run a
simple loop inside a structured turn executor, and enable deterministic
workflows only for tasks that need explicit multi-agent orchestration.

## Target Layers

### 1. Identity / Soul

Stable digital-life identity lives outside transient conversation history.

Required sources:
- `Soul.md`: personality, values, durable self-concept.
- `Agent.md`: working style, capability model, tool-use principles.
- `Memory.md`: user preferences and long-term collaboration rules.
- project rules: `CLAUDE.md`, future `IRONCLAW.md`, and managed policy files.

Invariants:
- identity layers are versioned and auditable;
- compact/resume must rehydrate identity layers;
- user/project policy can override style, but cannot bypass safety policy.

### 2. PromptFrame

Prompt construction is layered. It must not be rebuilt ad hoc in multiple
places.

Target frame:

```text
Static      system identity, core tool protocol, cache boundary
Session     project rules, skills catalog, agent catalog, long memory
Turn        current user message, hook additional context, retrieval
Iteration   active plan, latest tool observations, verification hints
Ephemeral   deferred tool schemas and one-turn system reminders
```

Invariants:
- one turn has one `PromptFrame`;
- each iteration renders from that frame plus current session state;
- hooks can inject turn context without being overwritten by the loop;
- cacheable and dynamic layers are separated by an explicit boundary.

### 3. Cognitive Loop

The loop stays simple, but its phases are explicit:

```text
prepare -> model -> tool dispatch -> observe -> verify -> reflect -> finalize
```

Invariants:
- provider and stream errors return real errors;
- `SessionEnded.Succeeded` reflects actual execution status;
- reflection only extends the loop when there is explicit unfinished plan state;
- max iterations remains the hard stop.

### 4. Tool OS

Tools are runtime syscalls, not prompt suggestions.

Required capabilities:
- file, bash, HTTP, browser, MCP, scheduler, notification, memory, skill,
  subagent, and future app-control tools;
- capability metadata: read-only, destructive, network, approval mode,
  parallel safety, path scope;
- lazy tool schema loading for large MCP/plugin tool catalogs.

Invariants:
- dispatch respects tool capability and configured concurrency;
- path-scoped tools touching the same canonical path are serialized;
- destructive or interactive tools are never run in uncontrolled parallel;
- unavailable tools are not advertised to the model.

### 5. Permission Kernel

The stronger the agent gets, the more runtime policy matters.

Required permission tiers:
- read-only: allow or notify by policy;
- workspace write: configurable auto-allow for trusted local sessions;
- destructive: explicit approval or durable allow rule;
- network: host/method/data-sensitivity policy;
- credential/system: protected channel, sandbox, or deny by default.

Invariants:
- policy uses real tool capabilities, not an empty default struct;
- remote and scheduled channels have stricter default profiles than local TUI;
- approvals are auditable and can be persisted;
- safety does not rely on model self-restraint.

### 6. Memory OS

Memory must change future behavior, not only store facts.

Memory types:
- episodic: what happened;
- semantic: stable facts;
- procedural: successful task strategies;
- profile: user preferences;
- autobiographical: IronClaw's own decisions and reasons;
- relationship: collaboration norms with the user;
- temporal: facts with validity windows.

Invariants:
- successful verified task patterns are recorded as procedural memory;
- unified retrieval is used for prompt injection;
- contradictory memories are reconciled or explicitly marked;
- memory writes and deletions are auditable.

### 7. Workflow Engine

Workflows are deterministic orchestration for high-value complex tasks.

Default model:

```text
pipeline > parallel
```

Use `parallel` only when a barrier is semantically required. Workflow execution
must support budgets, caching, replay, resume, and structured outputs.

### 8. Task Runtime

Digital-life behavior requires long-lived tasks.

Required concepts:
- task ledger: goal, status, evidence, next action, owner;
- wakeups: resume at a future time;
- monitors: react to files, git, URLs, schedules, or user messages;
- background agents: query, cancel, attach, and summarize;
- checkpoints: resume interrupted turns when safe.

### 9. Telemetry and Evals

No complex agent runtime can improve without evidence.

Required spans/events:
- turn started/ended;
- model call, retry, stream error;
- tool call, permission decision, approval, sandbox result;
- prompt-frame render and token usage;
- compact, retrieval, reflection, workflow step;
- task success/failure and user feedback.

Verification:
- unit tests for every runtime invariant;
- integration tests through Gateway, not only isolated packages;
- eval cases for multi-step edit, denied tool, compact/resume, subagent,
  scheduler, and workflow paths.

## P0 Implementation Slice

The first implementation slice must repair the current runtime foundation:

1. PromptFrame: ensure hook-injected context survives into provider requests.
2. Session lookup: route approval and activity by session ID correctly.
3. Multi-agent wiring: registered specs must become usable `agent_*` tools.
4. ToolScheduler: use capability-aware, bounded parallel execution.
5. Permission capabilities: pass real tool metadata into policy evaluation.
6. Error truthfulness: stream/provider failures must mark sessions failed.

This slice is intentionally smaller than the full architecture but directly
unblocks safe evolution toward the super-agent runtime.
