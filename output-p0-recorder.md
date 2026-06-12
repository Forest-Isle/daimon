Implemented the episode-level replay recorder and wrote the required summary to [output-p0-recorder.md](/Users/wuqisen/dev/IronClaw/output-p0-recorder.md).

Key pieces added:
- Replay events in `internal/agent/events.go`
- Provider/tool/turn publish sites in `internal/agent/loop_common.go` and `internal/agent/agent.go`
- Daily JSONL recorder in `internal/telemetry/replay.go`
- Config defaults and example YAML keys
- Gateway lifecycle wiring
- Unit tests in `internal/telemetry/replay_test.go`

Verification passed:
- `GOCACHE=$PWD/.gocache go test ./internal/telemetry`
- `GOCACHE=$PWD/.gocache make build-bin`
- `GOCACHE=$PWD/.gocache make vet`
- `GOCACHE=$PWD/.gocache make test-short`

`make build-bin` exits successfully but Go still prints a module stat-cache warning because the external module cache is read-only in this sandbox. I did not stage, commit, or revert anything.

## Cross-Family Review (Claude → Codex)

Verified against `task-p0-recorder.md` spec:

- Three replay events (`ProviderExchange`/`ToolRoundTrip`/`TurnClosed`) defined with full payloads — `internal/agent/events.go` ("Replay Events" section).
- `publishProviderExchange` covers all provider-call paths: stream success, stream error, delta error, and the non-streaming tool_use fallback (5 call sites in `loop_common.go`).
- `TurnClosed` published at loop exit with final reply.
- `ReplayRecorder` (`internal/telemetry/replay.go`): daily rotation via lazy `openForDateLocked`, mutex, injectable clock (`now func() time.Time`) — matches spec including the rollover-test requirement.
- Config: `telemetry.replay_enabled` default **true** (independent of `telemetry.enabled`), `replay_dir` default `<appdir>/replays`; `configs/daimon.example.yaml` updated.
- Gateway wiring: separate subscription, cleaned up in `Stop`.
- Tests: `TestReplayRecorderRecordsReplayEventsFromBus` + `TestReplayRecorderRollsOverByDate` pass.

Scope-creep check: the `git diff HEAD` mix initially suggested loop-behavior changes (error returns, prompt-frame refactor), but those are the user's pre-existing uncommitted work (present before P0-B dispatch; `linear_loop.go` already handled `iterErr`). No codex scope creep found.

Re-verified by reviewer: `go test -tags fts5 ./internal/telemetry/ ./internal/agent/` PASS, `make build-bin` / `make vet` / `make test-short` PASS.

**Verdict: ACCEPTED.**