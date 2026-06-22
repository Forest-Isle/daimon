# Daimon Error Analysis v1

> Phase-0 deliverable for the eval program (agent-deepdive `phase-1-evals`).
> Method: Hamel-style open coding → axial coding → failure taxonomy, applied to
> **Daimon's own production replay corpus**, not synthetic coding tasks.
> Discipline: let failure modes emerge from the data; do **not** import the
> course's coding-agent failure pool.

## 1. Corpus & method

- Source: `~/.daimon/replays/*.jsonl` (2026-06-17 … 06-22), the
  `telemetry.ReplayRecorder` event stream.
- Frame (machine baseline, via `daimon replay`):
  **52 sessions · 103 provider exchanges · 95 tool calls · 39 tool "failures" · 1 salvaged · 0 abnormal/max-token stops.**
- 24/52 sessions are pure-reply triage (zero tool calls); 28/52 used tools.
- Every session was read (digest extractor → `/tmp/daimon_digest.py`, throwaway).
- Benevolent dictator: one analyst (drafted for operator adjudication).

**What Daimon actually does in production:** autonomous **email-triage
episodes**. Mail arrives → episode reads world (identity/commitments/journal),
attempts memory recall, classifies the message (marketing / informational /
phishing / security-sensitive / actionable), writes a journal outcome, closes.
This is the real task the **current corpus** evaluates — not code-fixing. The
course's `go build` / `FAIL_TO_PASS` checks don't apply to this triage corpus;
they apply to a **separate coding-delegation surface** (§8), which has no
autonomous traces yet.

## 2. Headline finding (the reason this exercise paid off)

The machine metric reports **39 tool failures (41% of all tool calls)**. Naively
that reads as "the agent fails 2 of every 5 tool calls." **It is wrong.**
Decomposed:

| Bucket | Count | Share | What it actually is |
|---|---:|---:|---|
| **Governance denial** (`*execution denied by user`) | **33** | **85%** | Approval-gated tool invoked in an autonomous (no-operator) episode → auto-denied. Not an agent mistake. |
| Agent tool error (`file_read` on dir / tilde path) | 5 | 13% | Genuine agent mistakes — all in **one** session. |
| Env/perf (`grep_code` 5s timeout) | 1 | 3% | Tool/env limit. |

So **85% of the "failure" signal is the harness's permission policy denying the
agent, not the agent failing.** An eval that scored agent quality off
`Analyze.ToolFailures` would punish the model for governance config — the
textbook "measuring the wrong thing" the course warns about, confirmed
empirically on our own data.

**First job of the Daimon eval harness:** decompose `tool_failures` into
`{governance-denied, agent-error, env}` *before* any of it counts as a quality
signal.

**Counter-finding (don't only count failures):** the core triage reasoning is
**high quality** — sharp phishing detection (`tm.openai.com` non-canonical
subdomain; `ses.binance.com` Amazon-SES routing; urgency-trigger heuristics),
dedup awareness ("already processed by a prior episode"), and correct
security-sensitivity flagging (Wise password reset, fal.ai payment). The eval
needs a **positive correctness axis**, not just failure counts, or it will miss
that the agent is actually good at its job.

## 3. Failure taxonomy (7 modes)

### FM-1 — Autonomous episode denied an approval-gated tool  · **P0**
Definition: a tool requiring operator approval is called inside an autonomous
episode with no operator present, so the interceptor auto-denies it.
Detection cue: `ResultJSON.Error` ~ `/denied/`.
Breakdown of 33 denials: **memory ×26**, bash ×4, http ×2, values ×1.
Sub-modes by *effect*:
- **1a recall denied** — `memory search/list`, `values list`: episode can't load
  prior context, re-derives from scratch every time.
- **1b persist denied** — `memory save`: episode can't record what it learned →
  memory/world never populate → next episode is equally blind. Vicious cycle;
  the engine behind FM-2.
- **1c enrichment denied** — `http` geo-lookup, GitHub CI fetch, `bash` mail
  probe: degraded analysis. These denials are arguably **correct** (external
  network in an autonomous context) — distinguish "misconfigured deny" (1a/1b)
  from "correct deny that still degrades" (1c).
Frequency: **25 / 52 sessions (48%)**. Severity: **HIGH** (structural amnesia).
Note: §4.6 ("autonomous episodes may use read-only tools") was supposed to cover
read-only recall, yet read-only `memory search` is still denied → real gap.
Samples: sess_…739657, …740857, …854987, …865967, …948336, …015576 (+20 more).

### FM-2 — Cold / empty world substrate  · **P1 (config, not eval)**
Definition: `world_read identity` and `world_read commitments` return empty in
every session that reads them (**8/8 identity reads empty**). Identity, values,
and commitments were never configured.
Effect: every episode can only ever conclude "no prior interest → informational";
Daimon structurally **cannot create value or take action** — the ceiling on all
52 episodes. Locus: onboarding/config, not reasoning.
Frequency: ~all tool-using sessions. Severity: **HIGH** (caps all value).
Triage note: fix by configuring the substrate, not by building an eval — but a
deterministic "episode ran against empty identity" flag is worth surfacing.

### FM-3 — Salvage masks a non-outcome (reward-hacking-adjacent)  · **P0**
Definition: episode marked `salvaged=true` whose final reply *promises* action
but delivers none — the Daimon analog of "comment out the failing test to make
CI green."
Sample: sess_…986840030000 — 20 exchanges, 39 tool calls, ends with FINAL =
*"Now I have a clear picture … Let me handle it properly by examining the system
state and setting up what's needed."* That sentence **is** the outcome; nothing
was set up.
Frequency: **1 / 52**. Severity: **VERY HIGH** — trust red line, so **P0 despite
low frequency** (course rule: low-freq × high-sev = P0).

### FM-4 — Runaway exploration / non-convergence  · P2
Definition: many tool calls with no information-gain stopping rule.
Sample: same sess_…986840030000 — wandered the whole codebase (blueprint,
configs, `internal/*`, repeated greps) to understand a single "loop trigger"
email. Co-occurs with FM-3.
Frequency: 1 / 52. Severity: MED (token burn).

### FM-5 — Home/tilde path assumption under cwd-fence (agent error)  · P2
Definition: `file_read path="~/.daimon/…"` resolves literally to
`<repo-cwd>/~/.daimon/…` → no-such-file. Agent assumes shell tilde expansion;
file tools are fenced to the process cwd.
Frequency: 3 calls, 1 session. Severity: MED.

### FM-6 — Wrong tool for target: file_read on a directory (agent error)  · P3
Definition: `file_read path="configs"` → "is a directory" (should be `file_list`).
Frequency: 2 calls. Severity: LOW-MED.

### FM-7 — Tool/env perf: grep_code timeout  · P3
Definition: `grep_code` aborted at the 5s limit on a broad pattern.
Frequency: 1. Severity: LOW.

## 4. Frequency × severity → priority

| FM | Freq (sessions) | Severity | Priority | Locus |
|---|---:|---|---|---|
| FM-1 governance denies core tools | 25/52 | HIGH | **P0** | harness permission policy |
| FM-3 salvage masks non-outcome | 1/52 | VERY HIGH | **P0** | agent control |
| FM-2 cold world substrate | ~all | HIGH | P1 (config fix) | onboarding |
| FM-4 runaway exploration | 1/52 | MED | P2 | agent control |
| FM-5 tilde-path under fence | 1/52 | MED | P2 | agent tool-use |
| FM-6 file_read on dir | ~1/52 | LOW-MED | P3 | agent tool-use |
| FM-7 grep_code timeout | 1/52 | LOW | P3 | tool/env |

**Top-2 to build evals for next (Week 2): FM-1 and FM-3.**

## 5. Top-2 eval triage (deterministic vs LLM-judge)

Per the rule "deterministic-coverable → never use a judge":

- **FM-1 → DETERMINISTIC.** Pure trace inspection: `ResultJSON.Error ~ /denied/`,
  counted per episode and split by tool name into 1a/1b/1c. Flag any autonomous
  episode where a **read-only** tool (memory recall) was denied — that's the
  misconfiguration. Zero judge, millisecond, free. This is the model Week-2
  deterministic check (`evals/checks/governance_denial.go`).

- **FM-3 → HYBRID.** Deterministic pre-filter: `salvaged==true` **AND** no world
  Mutation produced **AND** `Outcome.Status != done`. Semantic confirm: does the
  final reply actually *resolve the goal* or merely *promise/explore*? That last
  step needs an LLM-judge — reuse the existing `replay.Judge` / `Rescore`
  machinery with a binary rubric ("completed outcome vs unfulfilled intent").
  This becomes the model Week-2 judge **and** the first calibration target for
  Week 3 (judge ↔ human ≥ 85% on the salvage/intent call).

## 6. Trace-recorder gap surfaced (feeds the blueprint)

On `stop=tool_use` turns, `ProviderExchange.ResponseText` is **empty** — the
model's pre-tool reasoning ("why this tool/path") is not captured; only the final
turn carries prose. For diagnosing tool-selection errors (FM-5/FM-6) the "why" is
invisible. Recommend capturing thinking / pre-tool text per step. This is exactly
the blueprint §5 imperative ("ensure episode records every step's
thought→tool→args→result") and should be fixed while the harness is being built.

## 7. Saturation & next step

Not yet at theoretical saturation: 52 traces, one corpus type (email triage),
one week. The course bar is ≥100 traces and ~20 consecutive with no new class.
Action items:
1. Keep accumulating replay traffic; re-run open coding past 100 traces to
   confirm the taxonomy and catch chat/proposal/sleep-cycle failure modes the
   email-only corpus can't show.
2. Build the two Week-2 evals (FM-1 deterministic, FM-3 hybrid) under `evals/`.
3. Fix the substrate issues open coding surfaced regardless of eval work:
   FM-1 (read-only memory should be allowed in autonomous episodes — §4.6 gap)
   and FM-2 (configure identity/values/commitments).

## 8. The coding surface: delegation, not self-coding

Daimon **has** a full coding toolset (`file_edit`/`file_write`/`file_patch`,
`test_run`, `code_intel`, sandboxed `bash`, `read_before_edit` interceptor). It
is not "not a coding agent." But the chosen architecture for coding work is
**delegation to an external coding agent (Codex / Claude Code), not growing
Daimon's own autonomous coding loop.**

Rationale:
- **Identity preserved.** Daimon stays the governance/orchestration brain; the
  external agent is the executor. It does not become a worse Claude Code.
- **Reuse the executor's sandbox + eval surface.** Codex already has a kernel
  sandbox (Seatbelt/Landlock). Daimon delegates instead of rebuilding edit/test
  safety from scratch.
- **Collapses to existing primitives.** "Dispatch a coding task" = one tool
  (mirrors the `subagent` tool / MCP). Dispatch → verify → propose-merge reuses
  the hold-queue + proposals + undo + worktree machinery already built.

**The autonomy bar does not vanish — it relocates to the boundary.** Daimon
cannot gate the external agent's *internal* steps, so it gates: (a) the task and
scope it hands off (prompt, `cd`, sandbox level), (b) the returned diff, (c)
whether to merge. This treats the whole invocation as **one action with an
inspectable outcome (diff + test result)** — outcome verification, not trajectory
replay, so the §706 honesty wall stays escaped (coding has a deterministic
verifiable reward that the life-domain actions lack).

**Three boundary fences (load-bearing — must exist before any autonomous
dispatch):**
1. **Origin trust.** A dispatch may be originated only by the operator or a
   Daimon-vetted goal — **never** triggered by untrusted input. (The mail corpus
   analyzed here is entirely untrusted external content; "an email told me to fix
   a repo" is a prompt-injection surface.)
2. **Self-repo fence.** Dispatching the external agent at Daimon's **own** source
   with write access = self-modification laundered through a proxy. Self-repo
   dispatch is read-only or operator-signed, never autonomous-write.
3. **Sandbox/worktree scope.** Autonomous dispatch runs in an isolated worktree +
   sandbox; blast radius is bounded and git-reversible.

**Eval consequence.** The course's deterministic coding checks (03/07) apply
here — repositioned as an **acceptance gate on the returned diff**, not as
Daimon's own coding loop. This is *cleaner* than grading a self-produced
multi-step trace: there is a concrete diff + repo to run tests against. The gate
has three deterministic checks (no LLM-judge needed):

| Gate | Check | Guards |
|---|---|---|
| **tests-green** | required test set passes (`test_run`: `Success && Failed==0`) | did the change work |
| **no-test-tamper** | diff did not delete/skip/weaken tests | FM-3 reward hacking |
| **in-scope** | diff touched only allowed paths | instruction-following / self-repo fence |

This is the **diff-acceptance eval** — the first concrete deliverable of the
eval program for the coding surface. It lives at `evals/checks/` and returns an
`accept/reject` verdict with per-gate reasons. A delegated diff is accepted (→
proposal for operator merge) only if all three gates pass; any failure rejects
with an actionable reason.
