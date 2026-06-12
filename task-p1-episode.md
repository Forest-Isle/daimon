# Task P1-3: `internal/episode` — cognitive kernel (Runner + Composer + exit contract)

Context: `DAIMON_BLUEPRINT.md` §4.3 (episode 情节内核). The world model data layer (P1-1) and tools (P1-2) are landed. This task builds the cognitive execution kernel: assemble context from world, run a bare ReAct loop with a mandatory `episode_close` tool for structured exit, salvage non-compliant exits.

Strangler: this runs **alongside** the existing `agent.HandleMessage` / `LinearLoop`. Do NOT touch `internal/agent/linear_loop.go`, `internal/agent/reflection.go`, plan injection, `internal/agent/agent.go` handle loop, or any existing agent execution path. The episode kernel is a parallel subsystem; wiring it into production (replacing the existing path) is a later task.

Branch `refound/daimon`; tree has unrelated uncommitted changes — no git mutations at all.

## Deliverables

### 1. Package `internal/episode/`

#### Core Types

```go
package episode

type State struct {
    ID        string    // ulid
    Goal      string    // why this episode was started
    Trigger   string    // source: "chat" | "timer" | "proposal" | "reaction"
    CreatedAt time.Time
    Budget    Budget
}

type Budget struct {
    MaxIterations int           // default 20, matches current agent
    MaxTokens     int           // prompt context budget
    Deadline      time.Duration // wall-clock timeout
}

type Outcome struct {
    Status      string           // done | blocked | handed_off
    Summary     string           // ≤500 chars, goes to journal
    WorldWrites []world.Mutation // what the model decided to persist
    Receipts    []string         // episode-level action IDs (stubs for future action layer)
    FollowUps   []FollowUp       // timers/subscriptions to plant
    OpenQuestion *string         // blocked → what's needed from user
    Salvaged     bool            // true if outcome was inferred (not model-declared)
}

type FollowUp struct {
    Kind    string        // "timer" | "watch" | "check"
    Detail  string        // timer interval like "24h", watch target
    Goal    string        // what to do when it triggers (becomes next episode's Goal)
}
```

#### Runner (bare ReAct)

`Runner` takes a `*Provider`, a `*Store` (world.Store), a `*Identity`, and a tool registry (use `tool.Registry` from `internal/tool` — check the existing interface, import path, and method signatures; match exactly).

```go
type Runner struct {
    provider  *agent.Provider  // existing provider abstraction; import pattern matches internal/agent/
    tools     *tool.Registry   // existing tool registry interface
    world     *world.Store
    identity  *world.Identity
}

func NewRunner(p *agent.Provider, tr *tool.Registry, ws *world.Store, id *world.Identity) *Runner
```

Method:

```go
func (r *Runner) Run(ctx context.Context, ep State) (Outcome, error)
```

**Run lifecycle**:

1. **Compose context**: call `composePrompt(ctx, ep, r.world, r.identity)` → returns system prompt string and initial messages slice. The prompt layout (order = cache boundary placement):
   - Personality + Daimon constitution summary (static, ~500 chars)
   - Identity digest from `r.identity.Digest()` (static per episode)
   - Active commitments digest from world
   - Available tools (from `r.tools`)
   - The `episode_close` tool (ALWAYS registered, even if not in the tool registry)
   - Goal instruction: "Your goal: {Goal}. You MUST call `episode_close` with a complete Outcome before finishing. Until you call it, the system will treat your work as incomplete."
   - User message: the trigger event's payload text
   - Recent transcript from the relevant session (if chat trigger, last 5 exchanges)

2. **Loop** (match existing LinearLoop shape but stripped — no plan injection, no Reflexion, no budget text injection):
   ```
   for iteration < maxIter:
     stream provider call (system prompt + messages)
     accumulate fullText + toolCalls from stream
     if stream error → return Outcome{Status: "failed", Salvaged: true}
     if len(toolCalls) == 0:
       // model just said something but didn't call episode_close
       → inject one "call episode_close" prompt and continue OR salvage
     for each tool call:
       if tool == "episode_close":  // must match the actual tool name registered
         → parse args as Outcome
         → validate Outcome schema (non-empty Status, Summary ≤500 chars)
         → apply WorldWrites via r.world.Apply
         → return Outcome{...Salvaged: false}
       else:
         → dispatch via r.tools.Execute (follow existing pattern)
         → append tool result to messages
     if iteration == maxIter-1:
       → salvage: haiku-level prompt to extract Outcome from transcript
       → return Outcome{...Salvaged: true}
   ```

3. **Register `episode_close` tool**: schema matches `Outcome` JSON, fields descriptions guide model behavior.

#### composePrompt

```go
func composePrompt(ctx context.Context, ep State, ws *world.Store, id *world.Identity) (system string, messages []agent.CompletionMessage)
```

Implementation details:
- Identity: call `id.Digest()`, if empty use "Not yet configured."
- Commitments: call `ws.CommitmentsDigest(ctx, "")` (all active), fallback "None."
- Messages: starts with ONE user message whose text is the trigger event text; if ep.Goal is set, prepend `"## Goal\n" + ep.Goal` as text prefix.
- Use `agent.CompletionMessage{Role: "user", Content: agent.Content{...}}` — check the exact type from `internal/agent/provider.go`.

### 2. Tests `internal/episode/episode_test.go`

Same-package, table-driven. Use a test provider (check how existing tests fake the provider — `internal/agent/claude_provider_test.go` or `openai_test.go` might have mock patterns; if none fits, a minimal `testProvider` that takes canned responses is fine).

Test cases:
- **Basic happy path**: Runner gets a simple user message, the test provider returns a tool call to `episode_close` with valid Outcome → Runner returns Outcome without error, Salvaged=false.
- **Max iterations salvage**: provider never calls episode_close (returns text-only stop) → Runner exhausts iterations → Outcome.Salvaged=true, Status extracted from transcript.
- **Stream error**: provider returns error → Outcome.Salvaged=true, Status="failed".
- **Tool dispatch**: provider returns one non-close tool call (e.g. world_read) then one episode_close → tool was executed before close.
- **Compose prompt content**: verify identity digest and commitments digest appear in the composed system prompt (use a known identity digest + known commitment in world store).

### 3. World mutations helper

Add a helper in `internal/world/` (existing file, not new):

```go
// ApplyOutcome applies the world writes from an Outcome, stamps episodeID,
// and appends the outcome summary to the journal. Returns error on first failure.
func (s *Store) ApplyOutcome(ctx context.Context, episodeID string, o Outcome) error
```

where `Outcome` here needs to live in `internal/world` as a lightweight type (or share a common type — share if possible, define interface if import cycle risk). Check: episode → world (imports world), world → episode (must NOT import). Since `episode.Outcome` references `world.Mutation`, the dependency is one-way. Keep `Outcome` in episode for now and have `ApplyOutcome` accept `[]Mutation` + `summary string`. This avoids any cross-package coupling.

So add to `internal/world/world.go`:

```go
func (s *Store) ApplyOutcome(ctx context.Context, episodeID string, muts []Mutation, summary string) error
```

This wraps `Apply` in a transaction that ALSO appends a journal entry with the outcome summary. Should be idempotent-safe (if called twice, the journal entry has the same episodeID — use INSERT OR IGNORE or a duplicate check).

## Out of scope

- Gateway wiring / replacing existing HandleMessage path.
- Removing plan injection, Reflexion, per-message fact extraction.
- Action-layer trust/hold/approve (Receipts are stubs).
- Replay recording (already done in P0-B; episode runs will naturally record via the existing EventBus subscription).

## Verification (must pass)

```bash
make build-bin
make vet
make test-short
CGO_ENABLED=1 go test -tags fts5 ./internal/episode/ ./internal/world/ ./internal/tool/
```

Sandbox note: `GOCACHE=$PWD/.gocache` if needed.

## Output

Write `output-p1-episode.md` at repo root: files added, design decisions (especially around salvage and tool dispatch), verification tails, any deviations + reasons.
