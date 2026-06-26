# Spec: Implicit episode close for no-tool conversational turns

Status: IMPLEMENTED (Phases 0–2). Phase 3 (cheap-summary toggle) deferred.

## Implementation note — status gate (discovered during impl)

The auto-close trigger gained a third gate beyond the spec's original two. A
no-tool turn's status cannot be assumed "done": a model may report it is blocked
or handed off in plain text without calling episode_close (existing salvage tests
encode this). So implicit close fires only when `inferStatus(reply) == "done"`
(shared keyword heuristic, extracted from the transcript salvage path). A
blocked/handed_off reply falls through to the reminder → salvage path, where the
model can still declare follow_ups / an open question. Gates: (1) reminder not yet
sent, (2) no tool used this episode, (3) status reads as done.


Area: `internal/episode/episode.go` (+ `internal/world`, `internal/sleep`)
Author: design handoff (Claude). Optional cross-review: codex.

## 1. Problem

The episode Runner enforces a structured exit contract: every episode must end
with the model calling `episode_close`, carrying an `Outcome`
{status, summary, world_writes, receipts, follow_ups, open_question,
value_created_usd}. The Outcome is persisted to the world model — invariant #3
(交账强制): a started episode must leave a durable Outcome.

When the model answers in plain text with `stop_reason=end_turn` and **no tool
call** (common on backends that don't reliably bundle the tool call — e.g. the
DeepSeek-via-Anthropic-shim backend in use), the loop injects a
"You must call episode_close" reminder and burns a **second full round-trip on
the main model** purely to obtain the close call. That turn costs another full
input+output, adds latency, and makes the model emit conversational filler
("好的，这就关闭") — the noise the `lastReply` fix just had to band-aid.

For a trivial chat turn this is 2× main-model calls where 1 would do.

## 2. Chosen design — Option A (implicit close for no-tool turns)

When the first closing condition is a turn with `end_turn` + non-empty text +
**zero tool calls in the entire episode**, synthesize the Outcome directly and
close — instead of injecting the reminder and re-running the main model.

Scope is strict: auto-close only when **no tool was invoked anywhere in the
episode**. Any episode that called a tool keeps the strict `episode_close`
requirement, so the model still declares `world_writes` / `value_created_usd` /
`follow_ups`. Rationale: a turn where the model only talked and ended implicitly
decided nothing needs persisting; had it wanted to persist, it would have called
a world tool (then `toolCalls != 0`, which never reaches this path).

Summary source: **use the reply text directly** (zero extra model call) — this
is the whole point (latency + cost). A cheap haiku-tier extraction for a richer
summary is deferred to Phase 3 behind a config toggle; MVP is zero-call.

`stop_reason` is not relied on (the streamed shim reports it inconsistently);
the discriminator is purely "no tool calls this turn + no tool used this
episode + reminder not yet sent".

## 3. Key constraint — marker, not "salvaged"

Auto-closed conversational outcomes must **not** be marked `Salvaged`.
`Salvaged=true` feeds the salvaged-rate metric and disqualifies an outcome from
distillation (`internal/world/outcome_quality.go` `OutcomeSalvaged`,
`internal/sleep/distill.go` which mines only outcomes that closed through
`episode_close` and are not salvaged).

Three distinct states must be representable:

| State | meaning | salvaged-rate | distill candidate |
|---|---|---|---|
| model-closed | model called `episode_close` | no | **yes** |
| auto-closed (new) | framework closed a no-tool chat turn | **no** | no |
| salvaged | model never closed; framework recovered after work | yes | no |

Decision: add `AutoClosed bool` (framework-set, `json:"-"`, mirrors `Salvaged`)
on `Outcome`, plumb it through `world.OutcomeMeta` → `ApplyOutcome` → journal
`outcome` row detail. It must keep `Salvaged=false`. Distill and salvaged-rate
both exclude `AutoClosed`. (A bare `Salvaged=false` clean outcome is not enough:
distill would then mine trivial chat turns as if they were model-authored
closes.)

## 4. Touch points

- `internal/episode/episode.go`
  - `Outcome`: add `AutoClosed bool` (`json:"-"`).
  - `Execute()`: track `toolInvoked bool` (set true when a **non-close** tool is
    dispatched). In the `len(toolCalls) == 0` block, **before** the reminder
    injection, add: if `!closeReminderSent && !toolInvoked && strings.TrimSpace(fullText) != ""`
    → build auto-close Outcome and `return r.close(...)`.
  - New helper `autoCloseOutcome(reply string) Outcome` → `{Status:"done",
    Summary: truncateRunes(compactWhitespace(reply),500), AutoClosed:true}`.
- `internal/world/world.go`
  - `OutcomeMeta`: add `AutoClosed bool`; `ApplyOutcome` records it (journal
    detail token, e.g. `auto_closed=1`, kept distinct from `tool_failures` /
    `unverified_actions`). Verify exact struct/sig at impl time.
- `internal/world/outcome_quality.go`
  - Classify `AutoClosed` as a non-salvaged, non-candidate state (add
    `OutcomeAutoClosed` or equivalent predicate). Salvaged-rate must not count it.
- `internal/sleep/distill.go`
  - Exclude `AutoClosed` from distill candidacy (it already excludes salvaged).

## 5. Phased plan

- **Phase 0 — marker plumbing.** Add `Outcome.AutoClosed`; thread through
  `OutcomeMeta`/`ApplyOutcome`/journal detail. *Verify:* `go test -tags fts5
  ./internal/world/` green; new assertion that an `AutoClosed` outcome lands a
  distinct detail token and `Salvaged=false`.
- **Phase 1 — auto-close branch.** Track `toolInvoked`; add the branch + helper.
  *Verify:* new `episode` tests (below) green; existing episode tests unchanged.
- **Phase 2 — metrics/distill exclusion.** Update `outcome_quality.go` +
  `distill.go`. *Verify:* salvaged-rate test shows auto-close excluded; distill
  candidacy test shows auto-close not mined, model-close still mined.
- **Phase 3 (deferred) — optional cheap-summary toggle.** Config flag to route
  the summary through a haiku-tier `Complete()` instead of raw reply text. Off by
  default. Not in MVP.

## 6. Test plan (package `episode`, `fts5` tag, in-package)

Driver: existing `episodeTestProvider{streams []providerResponse}` (consumed in
order; counts model calls via `len(provider.requests)`).

1. `TestExecuteAutoClosesNoToolConversationalTurn` — single stream: text, no
   tool call. Assert: exactly **1** provider request (no reminder round-trip);
   `out.Reply == text`; `out.Status == "done"`; journal outcome exists with
   `Salvaged=false` and the auto-closed detail token.
2. `TestExecuteAutoCloseNotSalvaged` — same; assert outcome is **not** counted in
   salvaged-rate and **not** a distill candidate; model-authored close in a
   sibling case **is** a candidate.
3. `TestExecuteToolEpisodeStillRequiresClose` — stream: text + a real tool call,
   then text + `episode_close`. Assert: NOT auto-closed (reminder/strict path
   intact); `toolInvoked` gate works; `out.Status=="done"` from the model close.
4. `TestExecuteEmptyTextNoToolFallsBackToReminder` — stream: empty text, no tool
   (×2), then a closing turn. Assert: reminder path still used (auto-close
   requires non-empty text); no panic.
5. Regression: `TestExecuteReplyKeepsAnswerAfterCloseReminder` (existing) and
   `TestExecuteBasicHappyPath` (model text+close in one turn) stay green.
6. Edge: idempotent replay (`EpisodeID` re-delivery) and parallel-episode
   isolation unaffected — run existing suites with `-tags fts5`.

## 7. Edge cases

- Empty text + no tools → **not** auto-closed; reminder fallback (existing).
- Tool used earlier, then text-only end_turn → strict close required
  (`toolInvoked` gate), preserving world_writes/value declaration.
- Model closes in first turn → unchanged.
- Max-iterations / salvage path → unchanged (auto-close fires before it).
- Streamed-OpenAI usage under-count caveat → unchanged; auto-close skips a call,
  so cost can only drop.

## 8. Risks & rollback

- *Risk:* a no-tool turn that genuinely should persist (e.g. user stated a
  durable preference) is auto-closed with no `world_writes`. *Mitigation:* such
  persistence requires a world tool call → `toolInvoked=true` → strict path. If
  the model narrates a preference without calling a tool, it's lost either way
  today.
- *Risk:* downstream reader keys off "closed through episode_close" and now sees
  fewer such closes. *Mitigation:* Phase 2 enumerates consumers; `AutoClosed` is
  explicit, not silent.
- *Rollback:* single branch + one bool field; revert Phase 1 to restore the
  reminder behavior with zero data-model change beyond the unused `AutoClosed`.

## 9. Style

Surgical diff; match `episode.go`. Errors wrapped with `%w`, `context.Context`
first arg, no panic for business errors, helpers <50 lines, table-driven tests.
