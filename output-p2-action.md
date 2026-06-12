# Task P2-1 Output: action layer data foundation

Implemented by Claude directly — the codex relay was exhausted (all 3 accounts
rate-limited / auth_error, repeated 502 "All accounts exhausted"). Rather than
block on relay recovery, the data-layer task (fully specified, parallel
subsystem like world) was implemented and self-reviewed in-loop.

## Files added

- `internal/store/migrations/028_action_ledger.sql` — trust_ledger, undo_journal, holds.
- `internal/action/action.go` — Class/Level types + Store.
- `internal/action/action_test.go` — 8 table-driven tests.

## Promotion / demotion logic (as implemented)

- **Default**: an unknown (class, context) pair has level `AskEvery` (0).
- **Promotion** (`RecordAttempt`, verified=true, on a clean record where
  `corrected == 0`): raises level by one when `verified_ok` reaches the
  threshold for the current level — `AskEvery→AskFirst` at 1, `AskFirst→HoldThenAuto`
  at 3, `HoldThenAuto→FullAuto` at 10. Walks one step per attempt (thresholds are
  cumulative verified counts).
- **Class ceiling**: `Irreversible` caps at `HoldThenAuto` (2) — it never reaches
  full auto, preserving a human gate (constitution rule 4). Reversible/Compensable
  cap at `FullAuto` (3).
- **Demotion** (`RecordCorrection`): increments `corrected` and lowers level by
  one (floored at AskEvery).
- **Freeze**: once `corrected > 0`, promotion is permanently blocked for that
  pair — autonomy is earned only by an unbroken verified record. (A future
  reconcile job may reset `corrected`; out of scope here.)

Holds: create (id/receipt auto-generated), `DueHolds(now)` filters
`state='pending' AND execute_at <= now`, `MarkHoldState` whitelists
{executed, recalled}, `RecallHold` only cancels still-pending holds and reports
a clear error when the hold already executed or is missing.

## Verification

```
CGO_ENABLED=1 go test -tags fts5 ./internal/action/ -v
--- PASS: TestClassStringAndParse
--- PASS: TestLevelString
--- PASS: TestTrustLevelDefaultsAskEvery
--- PASS: TestPromotionWalksThresholds
--- PASS: TestIrreversibleCapsAtHoldThenAuto
--- PASS: TestCorrectionDemotesAndFreezesPromotion
--- PASS: TestUndoJournal
--- PASS: TestHoldsLifecycle
ok  github.com/Forest-Isle/daimon/internal/action

make build-bin  → ok
make vet        → ok
go test ./internal/action/ ./internal/store/ → ok
```

## Deviations

- Implemented by Claude (not codex) due to relay outage; spec followed exactly.
- No interceptor wiring, real-tool classification, hold execution loop, approval
  UX, or undo execution — all out of scope (later Phase 2 tasks).
