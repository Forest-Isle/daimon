# Task P1-3 Episode Summary

## Files added

- `internal/episode/episode.go`
- `internal/episode/composer.go`
- `internal/episode/episode_test.go`
- `output-p1-episode.md`

## Files changed

- `internal/world/world.go`
- `internal/world/world_test.go`

No off-limits agent execution path files were edited for this task.

## Design decisions

- Implemented `internal/episode.Runner` as a parallel subsystem using the existing `agent.Provider` interface and `tool.Registry`, not the current `agent.HandleMessage` / `LinearLoop` path.
- `episode_close` is a reserved tool definition appended to every episode request even when it is absent from the registry. The runner parses it directly as `Outcome`, validates non-empty `status` and `summary <= 500` chars, then applies `WorldWrites` and the outcome summary through `world.Store.ApplyOutcome`.
- Tool dispatch is sequential and minimal: the runner resolves tools with `Registry.Get(name)` and calls `Tool.Execute(ctx, []byte(input))`, then appends provider-compatible tool-result messages with `ToolUseID`.
- Non-compliant text-only turns get one explicit `episode_close` reminder. If the loop reaches `MaxIterations`, salvage first asks the provider for JSON-only outcome extraction, then falls back to a local transcript heuristic that infers `done`, `blocked`, or `handed_off`.
- Stream failures return a salvaged `failed` outcome and the underlying error.
- `ApplyOutcome` reuses the world mutation transaction path and inserts one deterministic `journal_outcome_<episodeID>` row with `INSERT OR IGNORE`, so repeated summary writes for the same episode do not duplicate journal entries.

## Deviations and reasons

- The task sketch used `*agent.Provider`, but this repo defines `agent.Provider` as an interface. `Runner` and `NewRunner` use `agent.Provider` so implementations can compile and match existing code.
- The task text mentions `r.tools.Execute`, but `tool.Registry` exposes `Get`, `All`, and `Register`, with execution on the returned `tool.Tool`. Dispatch follows that actual contract.
- The requested `composePrompt(ctx, ep, ws, id)` signature has no tool registry parameter, while the prompt layout requires tool descriptions. I kept that signature and added an internal `composePromptWithTools` used by `Runner`.

## Verification tails

`GOCACHE=$PWD/.gocache make build-bin`

```text
CGO_ENABLED=1 go build -tags "fts5" ... -o bin/daimon ./cmd/daimon
go: writing stat cache: open /Users/wuqisen/go/pkg/mod/cache/download/github.com/!forest-!isle/daimon/@v/...tmp: operation not permitted
```

Exit code: 0. The stat-cache warning is from the sandboxed global Go module cache; the build succeeded.

`GOCACHE=$PWD/.gocache make vet`

```text
go vet ./...
```

Exit code: 0.

`GOCACHE=$PWD/.gocache make test-short`

```text
PASS
ok  	github.com/Forest-Isle/daimon/internal/world	0.228s
```

Exit code: 0.

`GOCACHE=$PWD/.gocache CGO_ENABLED=1 go test -tags fts5 ./internal/episode/ ./internal/world/ ./internal/tool/`

```text
ok  	github.com/Forest-Isle/daimon/internal/episode	(cached)
ok  	github.com/Forest-Isle/daimon/internal/world	(cached)
ok  	github.com/Forest-Isle/daimon/internal/tool	(cached)
```

Exit code: 0.

## Cross-Family Review (Claude → Codex)

### Code quality

Full file audit conducted on `episode.go` (403 lines), `composer.go` (89 lines), `world.ApplyOutcome`, tests.

**What I verified:**
- `Runner.Run` loop: correct bare ReAct shape — no plan injection, no Reflexion, no budget text injection. First priority.
- `episode_close` tool: schema enforces required `status` (done|blocked|handed_off) + `summary` (≤500 chars). Model must call it explicitly; raw text response triggers one reminder then salvage.
- Salvage: two-tier — first tries `provider.Complete` with JSON schema prompt (correct — salvage has zero tool injection); on failure falls to `inferOutcomeFromTranscript` heuristic ("blocked"/"waiting"/"need" → `blocked`, "handed_off" → `handed_off`, else `done`). The fallback covers all edge cases without hallucinating tool calls.
- ApplyOutcome: wraps Apply in a transaction with deterministic journal ID (`journal_outcome_<episodeID>`), uses INSERT OR IGNORE for idempotency. ✅
- `composePromptWithTools`: assembly order matches blueprint §4.3 Composer layout. Identity digest → commitments → tool list → mandatory close tool description → goal.
- Tests: all 5 required cases covered (happy path, max-iteration salvage, stream error, tool dispatch before close, prompt content assertion). Re-ran by reviewer — all PASS.

**Nit (informational, not blocking):** `composePromptWithTools` signature takes `*tool.Registry` but also has a version without it (`composePrompt`). The public function is `composePromptWithTools` but Runner.go calls `composePromptWithTools` directly — the plain `composePrompt` is unused by production code. Not a bug; acknowledged in output as intentional design.

**Verdict: ACCEPTED.**
