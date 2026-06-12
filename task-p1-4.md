# Task P1-4: Gateway wiring inversion — episode kernel replaces existing HandleMessage path

Context: `DAIMON_BLUEPRINT.md` Phase 1 final step. The episode kernel (`internal/episode.Runner`), world model (`internal/world`), and world tools are landed as parallel subsystems. This task wires them into production: `gateway.handleInbound` → `episode.Run.Run` instead of `agent.HandleMessage`. Then removes the now-dead scaffolding (plan injection, Reflexion, per-message fact extraction).

The commit `876cf14` established the baseline. P0-A through P1-3 are all committed. There is a stash `stash@{0}` from before the refound branch — do not pop or reference it.

CRITICAL RULE: One commit per major deletion so the user can revert individually. The commit sequence matters.

Branch `refound/daimon`; clean working tree. No git mutations beyond the ones explicitly listed.

## Step 1: Gateway wiring — episode path alongside existing path

Edit `internal/gateway/gateway.go` (handleInbound) to:

1. Before the existing `agent.HandleMessage` call, check if episode mode is enabled (config toggle `agent.episode_enabled`, bool, **default true**).
2. If enabled, assemble an episode `State` from the inbound message and `runner.Run(ctx, state)`.
   - State.ID = ulid
   - State.Goal = "Respond to the user's message"
   - State.Trigger = "chat: " + msg.Text
   - Budget: default budget struct.
3. If `runner.Run` succeeds and Outcome.Status != "failed", skip the old path and return. On error or failure, fall through to `agent.HandleMessage` as backup.
4. New gateway init fields: `EpisodeRunner *episode.Runner`, `EpisodeEnabled bool`. Accept them from config.
5. Add a stateless helper `msgToEpisodeState(msg channel.InboundMessage) episode.State`.

Config: `agent.episode_enabled: true` in config struct. Update `configs/daimon.example.yaml` with one-liner comment. Default true so the new path activates on next startup.

## Step 2: Remove plan injection from agent path

These are the files/functions that inject plan context into the system prompt. Remove:

- **`internal/agent/agent.go`**: remove `a.deps.Core.PlanStore` usage from `buildSystemPrompt`. Remove any plan-related context that was injected. Keep the function itself (it still does persona + memory + hooks).
- **`internal/agent/agent.go`**: remove `plan_verify_hint` injection in `executeToolCall`. The VerifyInterceptor plan-awareness is dead since plan is no longer injected.
- **`internal/tool/interceptor_verify.go`**: remove `buildPlanVerifyHint` and the plan-awareness from `VerifyInterceptor`. Make it a simple output-size verifier.
- **`internal/agent/reflection.go`**: DELETE the entire file. Reflexion self-critique is replaced by episode's exit contract.
- **`internal/agent/linear_loop.go`**: remove `a.maybeReflect`, `a.injectReflection`, `reflectionsUsed` variable. Bare loop only.
- **`internal/agent/events.go`**: remove `ReflectionTriggered` event type and all publish sites for it. (But be careful: search for all references to `ReflectionTriggered` in the codebase and remove them.)
- **`internal/gateway/plan_store.go`**: DELETE the file. PlanStore bridges to session.Metadata — obsolete.
- **`internal/tool/plan.go`**: either DELETE or convert to a thin `commitment` adapter command (if you think it's worth keeping as a legacy migration path). The spec says "降级改造" — safest is to delete. The model can use `commitment` tool directly. If you delete, remove from `subsystem_tool.go` registration too.

Search for any remaining plan-related references: `grep -rn 'plan_\|PlanStore\|plan.verify\|plan_verify\|Plan:.*<-' --include='*.go' .`

## Step 3: Remove per-message fact extraction

**`internal/tool/memory.go`**: Remove the `MemoryTool`'s fact-extraction call (the `go extractFacts` async path in agent.go or wherever facts.go's LLM extraction is triggered). The actual file: grep for `extractFacts\|FactExtractor\|ExtractFacts` and delete all active calls. Keep the fact-related types/defs if they're part of the memory store schema — they may be used by later sleep/reconcile jobs—but remove the **invocation** of LLM extraction on every message.

**`internal/memory/facts.go`**: Remove the `LLMFactExtractor` and associated types if they are **only** used by the per-message extraction path. If they're used elsewhere for sleep/consolidation, leave the type defs but disable the extraction trigger.

## Step 4: Verification

```bash
make build-bin
make vet
make test-short
CGO_ENABLED=1 go test -tags fts5 ./...
```

Run the full test suite. Fix any test failures introduced by deletions — update test expectations that referenced Reflexion/plan events, not by weakening assertions.

## Step 5: Sequence of commits

Execute as separate git commits (exact sequence, do NOT squash):

```
commit 1: "refound(p1-4): wire episode runner into gateway handleInbound"
  (gateway.go + config wiring, msgToEpisodeState helper)

commit 2: "refound(p1-4): remove plan injection and Reflexion"
  (plan_store.go deletion, reflection.go deletion, plan.go deletion,
   plan/Reflexion removal from agent.go/linear_loop.go/events.go,
   VerifyInterceptor cleanup, tool registration cleanup)

commit 3: "refound(p1-4): remove per-message fact extraction"
  (remove FactExtractor invocation from memory tool/agent paths)
```

Each commit individually must `make build-bin && make vet && make test-short` green. If a test fails in an intermediate commit, fix it in that same commit.

## Out of scope

- Memory retrieval subsystem overhaul.
- Existing tool behaviors or capabilities.
- TUI changes.
- Session management changes.
- The pre-existing stash entry.

## Output

Write `output-p1-4.md` at repo root: per-commit summaries with verification status, any test fixes applied, and the final `make test` results.
