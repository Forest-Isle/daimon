# Task P2-1: `internal/action` foundation (reversibility + trust ledger data layer)

Context: `DAIMON_BLUEPRINT.md` §4.6 (action 行动层). The blueprint risk register says the action layer must land before the event heart — it is the security backbone for a remotely-triggered always-on agent. This task builds ONLY the data layer + store, as a parallel subsystem. Interceptor-chain wiring, hold-queue execution, and classification of real tools come in later tasks. Strangler rule: new package + new migration alongside; do NOT modify the existing interceptor chain, `internal/tool`, or `internal/agent` in this task.

Branch `refound/daimon`; clean tree expected. No git mutations (`add/commit/checkout/restore/stash`) at all.

## Deliverables

### 1. Migration `internal/store/migrations/028_action_ledger.sql`

Follow the exact embedding/registration mechanism the existing migrations use (check `internal/store`).

```sql
CREATE TABLE IF NOT EXISTS trust_ledger (
    action_class TEXT NOT NULL,        -- reversible | compensable | irreversible
    context_key  TEXT NOT NULL,        -- e.g. "mail.send|to:domain=company.com"
    attempts     INTEGER NOT NULL DEFAULT 0,
    verified_ok  INTEGER NOT NULL DEFAULT 0,
    corrected    INTEGER NOT NULL DEFAULT 0,
    level        INTEGER NOT NULL DEFAULT 0,   -- 0 ask-every 1 ask-first 2 hold-then-auto 3 full-auto
    updated_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (action_class, context_key)
);

CREATE TABLE IF NOT EXISTS undo_journal (
    receipt_id TEXT PRIMARY KEY,
    tool_name  TEXT NOT NULL,
    undo_spec  TEXT NOT NULL DEFAULT '',   -- JSON describing how to reverse
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    expires_at DATETIME,
    undone_at  DATETIME
);

CREATE TABLE IF NOT EXISTS holds (
    id         TEXT PRIMARY KEY,
    receipt_id TEXT NOT NULL,
    tool_name  TEXT NOT NULL,
    payload    TEXT NOT NULL DEFAULT '',   -- JSON of the deferred action
    execute_at DATETIME NOT NULL,
    state      TEXT NOT NULL DEFAULT 'pending',  -- pending | executed | recalled
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_holds_state ON holds(state);
CREATE INDEX IF NOT EXISTS idx_holds_execute_at ON holds(execute_at);
```

### 2. Package `internal/action`

Model `internal/world/world.go` and `internal/taskruntime/ledger.go` for style (plain `*sql.DB`, string timestamps if that is the house style — match it; `%w` error wrapping; ctx-first; guard clauses).

#### Reversibility classification (pure, no DB)

```go
type Class int
const (
    Reversible   Class = iota // file edits in a git repo, world-model writes → execute + undo record
    Compensable               // send mail/message, place cancellable order → hold queue
    Irreversible              // payment, unrecoverable delete, legal commitment → approval by trust level
)

func (c Class) String() string  // "reversible" | "compensable" | "irreversible"
func ParseClass(s string) (Class, error)
```

#### Trust level

```go
type Level int  // 0 AskEvery, 1 AskFirst, 2 HoldThenAuto, 3 FullAuto
const (
    AskEvery Level = iota
    AskFirst
    HoldThenAuto
    FullAuto
)
```

#### Store

```go
type Store struct { db *sql.DB }
func NewStore(db *sql.DB) *Store
```

Methods (ctx-first, `%w` wrapping, no panic):

- `TrustLevel(ctx, class Class, contextKey string) (Level, error)` — returns the recorded level, or `AskEvery` (0) if no row exists.
- `RecordAttempt(ctx, class Class, contextKey string, verified bool) error` — upsert: increment `attempts`, increment `verified_ok` when verified, bump `updated_at`. **Promotion rule (deterministic, no model):** after this attempt, if `corrected == 0` and `verified_ok >= promotionThreshold(level)` then raise level by one, capped at the class ceiling — **Irreversible caps at `HoldThenAuto` (2)**, others cap at `FullAuto` (3). Use thresholds: level0→1 needs 1 verified, 1→2 needs 3, 2→3 needs 10.
- `RecordCorrection(ctx, class Class, contextKey string) error` — increment `corrected`, and **demote one level** (floor 0). A correction is the user reversing/rejecting an action; it must lower autonomy.
- `RecordUndo(ctx, r UndoRecord) error` — insert into `undo_journal`.
- `MarkUndone(ctx, receiptID string) error` — set `undone_at`.
- `CreateHold(ctx, h Hold) error` — insert (generate id/receipt_id if empty, match world's ID approach).
- `DueHolds(ctx, now string) ([]Hold, error)` — `state='pending' AND execute_at <= now`, ordered by execute_at.
- `MarkHoldState(ctx, id, state string) error` — whitelist state in {executed, recalled}; reject others.
- `RecallHold(ctx, id string) error` — convenience: MarkHoldState(id, "recalled") only if currently pending; error if already executed.

Types:

```go
type UndoRecord struct {
    ReceiptID string
    ToolName  string
    UndoSpec  string  // JSON
    ExpiresAt string  // optional
}
type Hold struct {
    ID, ReceiptID, ToolName, Payload, ExecuteAt, State, CreatedAt string
}
```

### 3. Tests `internal/action/action_test.go`

Same-package, table-driven, temp SQLite with migrations applied (mirror `internal/world/world_test.go` bootstrap). Cover:
- Class/Level String + Parse round-trips and invalid input.
- TrustLevel returns AskEvery for unknown (class, context).
- Promotion: RecordAttempt(verified=true) walks 0→1 after 1, 1→2 after 3 total verified, 2→3 after 10; Irreversible caps at 2 even past 10.
- Correction demotes and a subsequent attempt with corrected>0 does NOT promote.
- Undo journal insert + MarkUndone sets undone_at.
- Holds: create, DueHolds time filter, MarkHoldState whitelist (reject "bogus"), RecallHold rejects an already-executed hold.

## Out of scope

- Interceptor-chain wiring, classification of real tools (bash/world_edit/etc), hold-queue execution loop, approval UX, undo execution.
- Any change to `internal/tool`, `internal/agent`, `internal/gateway`, `internal/episode`.

## Verification (must pass)

```bash
make build-bin
make vet
make test-short
CGO_ENABLED=1 go test -tags fts5 ./internal/action/ ./internal/store/
```

Sandbox note: if Go build cache is blocked, `GOCACHE=$PWD/.gocache`. Cannot run `git commit` (sandbox); leave changes in the working tree.

## Output

Write `output-p2-action.md` at repo root: files added, the promotion/demotion logic as implemented, verification output tails, any deviations + reasons.
