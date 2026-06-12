# Task P0-B: Episode-level replay recorder

Context: `DAIMON_BLUEPRINT.md` Phase 0, step 2 — start recording replay-grade data for every agent turn, on by default. Prerequisite Task P0-A (rename) is already applied: module is `github.com/Forest-Isle/daimon`, user dir is `~/.daimon` (helper in `internal/userdir`).
Branch `refound/daimon`; working tree has unrelated uncommitted changes — do NOT revert/stage/commit anything. No git commands that mutate state.

## Goal

Every model call and tool round-trip is recorded with FULL payloads to JSONL files under `~/.daimon/replays/`, grouped by session, rotated daily. This is the seed data for the future replay/eval harness. Existing metrics-grade telemetry (`telemetry.trace_path` + EventBus subscription) stays unchanged.

## Design (follow this; deviate only with stated reason in output)

1. **New replay-grade events** in `internal/agent/events.go` (existing events stay untouched):
   - `ProviderExchange{SessionID, Iteration int, Model, Provider string, SystemPrompt string, MessagesJSON json.RawMessage, ResponseText string, ToolCallsJSON json.RawMessage, StopReason string, DurationMs int64}` — one per provider call, full request snapshot + full response.
   - `ToolRoundTrip{SessionID string, Iteration int, ToolName string, ArgsJSON json.RawMessage, ResultJSON json.RawMessage, Succeeded bool, DurationMs int64}` — one per tool execution, full args and full (untruncated) result.
   - `TurnClosed{SessionID string, FinalReply string}` — when the loop returns the final user-facing reply.
2. **Publish sites**: `internal/agent/loop_common.go` already publishes `ModelCallStarted/Ended` at the three provider-call sites — publish `ProviderExchange` alongside `ModelCallEnded` (you have request + accumulated response there; serialize the exact messages slice and system prompt that were sent). `ToolRoundTrip` next to the existing `ToolExecuted` publish (where args/result are in scope). `TurnClosed` at loop exit where the final reply is sent.
3. **Recorder** in `internal/telemetry/` (new file `replay.go`): `ReplayRecorder` subscribes to the EventBus, filters only the three replay event types, writes JSONL lines `{ts, type, payload}` to `<dir>/YYYY-MM-DD.jsonl` (open lazily, switch file on date change, mutex like JSONLExporter). Constructor `NewReplayRecorder(dir string)`.
4. **Config**: `telemetry.replay_enabled` (bool, **default true**) and `telemetry.replay_dir` (default `<userdir>/replays`) in `internal/config`. Note: this default-true is independent of `telemetry.enabled`, which keeps gating the metrics trace only. Update `configs/daimon.example.yaml` with the two new keys + one-line comments.
5. **Wiring**: `internal/gateway/subsystem_telemetry.go` — init ReplayRecorder when enabled, close on Stop.
6. Payload size is accepted (full snapshot per call); no truncation, no compression. Daily rotation is the only size mechanism.

## Out of scope

- Replay/eval harness, scoring, any reading of these files.
- Touching plan/Reflexion or any loop behavior.
- Metrics-grade telemetry changes beyond the wiring file.

## Verification (must pass)

```bash
make build-bin
make vet
make test-short
```

Plus: a unit test in `internal/telemetry/replay_test.go` — publish one fake ProviderExchange + ToolRoundTrip + TurnClosed through a real EventBus, assert the daily file contains 3 well-formed lines with full payloads; and a date-rollover test (inject clock or make the filename function injectable).

If the sandbox blocks Go's build cache, use `GOCACHE=$PWD/.gocache`.

## Output

Write `output-p0-recorder.md` at repo root: files changed, where each publish site landed (file:line), sample JSONL line (redact nothing), verification output tails.
