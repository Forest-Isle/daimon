# Task P1-1: `internal/world` foundation (data layer)

Context: `DAIMON_BLUEPRINT.md` Phase 1 — the world model is the single source of truth (§4.4). This task builds ONLY the data layer foundation. Retrieval facade, tools, and gateway wiring are later tasks. Strangler rule: do NOT touch `task_ledger` / `internal/taskruntime` / `internal/memory` — new tables alongside, old paths retire at phase end.

Branch `refound/daimon`; tree has unrelated uncommitted changes — no git mutations (`add/commit/checkout/restore/stash`) at all.

## Deliverables

### 1. Migration `internal/store/migrations/027_world_model.sql`

```sql
CREATE TABLE IF NOT EXISTS commitments (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,              -- project | promise | deadline | watch | routine
    title TEXT NOT NULL,
    body TEXT DEFAULT '',
    state TEXT NOT NULL DEFAULT 'active',  -- active | waiting | done | dropped
    due_at DATETIME,
    horizon TEXT DEFAULT '',
    source_episode TEXT DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_commitments_state ON commitments(state);
CREATE INDEX IF NOT EXISTS idx_commitments_due ON commitments(due_at);

CREATE TABLE IF NOT EXISTS journal (
    id TEXT PRIMARY KEY,
    episode_id TEXT DEFAULT '',
    kind TEXT NOT NULL,              -- outcome | decision | correction | fact
    summary TEXT NOT NULL,
    detail TEXT DEFAULT '',
    occurred_at DATETIME NOT NULL DEFAULT (datetime('now')),
    rollup_id TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_journal_episode ON journal(episode_id);
CREATE INDEX IF NOT EXISTS idx_journal_occurred ON journal(occurred_at);
```

Check how existing migrations are embedded/registered (`internal/store`) and follow that mechanism exactly.

### 2. Package `internal/world`

Model `internal/taskruntime/ledger.go` for style (plain `*sql.DB`, string timestamps OK if that is the house style — check and match).

Types:

```go
type Commitment struct {
    ID, Kind, Title, Body, State, Horizon, SourceEpisode string
    DueAt, CreatedAt, UpdatedAt string   // match house timestamp style
}

type JournalEntry struct {
    ID, EpisodeID, Kind, Summary, Detail, OccurredAt, RollupID string
}

// Mutation is one element of an episode Outcome's WorldWrites.
type Mutation struct {
    Op     string          // commitment.create | commitment.update | journal.append
    Target string          // commitment ID for update; empty for create/append
    Body   json.RawMessage // the Commitment / JournalEntry fields being written
}

type Store struct { db *sql.DB }
func NewStore(db *sql.DB) *Store
```

Methods (context first, errors wrapped `%w`, no panic):

- `Apply(ctx, episodeID string, muts []Mutation) error` — ALL mutations in one transaction; unknown `Op` → error, transaction rolls back; created journal entries / commitment updates get `source_episode`/`episode_id` stamped from the argument.
- `CreateCommitment(ctx, c Commitment) error` (generate ID if empty — match taskruntime's ID generation approach)
- `UpdateCommitment(ctx, id string, set map[string]any) error` — whitelist updatable columns (title, body, state, due_at, horizon); always bumps updated_at.
- `ListCommitments(ctx, states []string, dueBefore string) ([]Commitment, error)` — empty filters = all.
- `AppendJournal(ctx, e JournalEntry) error`
- `ListJournal(ctx, sinceOccurredAt string, limit int) ([]JournalEntry, error)` — newest first.
- `CommitmentsDigest(ctx, dueWithin string) (string, error)` — compact human-readable summary of active commitments (one line each: kind/title/state/due), for prompt composition. Cap at 20 entries.

### 3. Identity layer helper (file-based, minimal)

```go
type Identity struct { Dir string }   // e.g. ~/.daimon/world/identity
func (i Identity) Digest() string     // contents of Dir/digest.md, "" if missing
func (i Identity) EnsureDir() error   // mkdir -p
```

No editing functions yet (tools come later).

### 4. Tests `internal/world/world_test.go`

Table-driven, same-package, in-memory/temp SQLite with migrations applied (mirror `internal/taskruntime/ledger_test.go` bootstrap). Cover: Apply happy path (mixed mutation batch, transactional rollback on bad Op), commitment CRUD + state filter, journal append/list ordering, digest formatting, identity digest missing-file case.

## Out of scope

- Gateway wiring, feature flags, tools, retrieval/embedding, episode package.
- Any change to `taskruntime`, `memory`, `agent`, `session` packages.

## Verification (must pass)

```bash
make build-bin
make vet
make test-short
go test -tags fts5 ./internal/world/ ./internal/store/
```

Sandbox note: if Go build cache is blocked, `GOCACHE=$PWD/.gocache`.

## Output

Write `output-p1-world.md` at repo root: files added, any deviations from this spec with reasons, verification output tails.
