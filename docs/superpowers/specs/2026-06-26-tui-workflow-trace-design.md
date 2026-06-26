# TUI Workflow Trace

Date: 2026-06-26

## Problem

After a conversation round completes, the TUI transcript shows only the user's
message (`› …`) and the agent's final reply (`⏺ …`). The tools that ran in
between — what was called, with what arguments, with what result — are invisible.
Tool activity surfaces only transiently in the bottom status bar
(`activeTool` / `activeToolSummary`) and is cleared the moment the tool finishes
(`model_update.go` `toolActivityMsg` handler). There is no way to look back at a
round and see its full workflow.

Goal: make each round's data flow and complete workflow visible inline in the
transcript, with raw tool output expandable on demand. Plus a light visual
cleanup of the existing panels — without a wholesale restyle.

## Scope Decisions

Resolved during brainstorming:

- **Both** an inline workflow trace and a light panel cleanup (not one or the
  other).
- **Maximum step detail**: tool name + argument summary + status + result
  summary + duration, with raw output expandable.
- **Architecture C — grouped render + global expand.** Steps carry a round
  association and render grouped/indented under their round; a single global
  hotkey toggles raw-output visibility. No per-step selection cursor.

Explicitly dropped: per-step expand via a focus cursor (Architecture B). The
bubbles `viewport` renders a static string and has no built-in sub-element
selection; a bespoke cursor layer would collide with the existing PgUp/PgDn/End
scroll and ↑/↓ input-history keys, for marginal value over a global toggle.

## Architecture

### Data flow today

```text
tool runs  ──ActivityInterceptor──▶ ReportToolActivity(call, done bool)
           ──GatewayToolActivityReporter──▶ SendToolActivity(toolName, summary, done)
           ──TUI adapter──▶ toolActivityMsg ──▶ status bar only (transient)
```

The `done=true` event **discards the tool result** (`interceptor_activity.go`
calls `ReportToolActivity(ctx, call, true)` and drops `result`). Start/done are
correlated only by tool name, which is fragile when the same tool runs twice or
tools run concurrently (sub-agents).

### Part 1 — Workflow trace

#### 1a. Plumbing: carry id + result + duration

`internal/tool/interceptor_activity.go`

Generate a per-invocation id local to `Intercept` (naturally scoped to one tool
execution, so it is correct even under concurrency). Emit it on both start and
done; carry the result, error, and duration on done.

```go
type ToolActivityEvent struct {
    ID       string        // correlates start↔done
    Done     bool
    Result   *ToolResult   // nil on start
    Err      error
    Duration time.Duration // wall time, on done
}

type ToolActivityReporter interface {
    ReportToolActivity(ctx context.Context, call *ToolCall, evt ToolActivityEvent)
}

func (a *ActivityInterceptor) Intercept(ctx, call, next) (*ToolResult, error) {
    if a.reporter == nil { return next(ctx, call) }
    id := newActivityID()
    a.reporter.ReportToolActivity(ctx, call, ToolActivityEvent{ID: id})
    start := time.Now()
    result, err := next(ctx, call)
    a.reporter.ReportToolActivity(ctx, call, ToolActivityEvent{
        ID: id, Done: true, Result: result, Err: err, Duration: time.Since(start),
    })
    return result, err
}
```

`internal/gateway/tool_activity_reporter.go`

On done, derive a **result summary** and **cap raw output**:

- Result summary: for tools with a known shape, a count
  (`"3 hits"`, `"142 lines"`, `"N bytes"`); on error, the error's first line;
  otherwise the output's first non-empty line, clamped.
- Raw output: hard-capped to ≤ 4 KB / ≤ 50 lines before sending, to bound TUI
  memory across a long session.

#### 1b. Channel interface

`internal/channel/channel.go` — widen the payload (struct instead of positional
args) so both start and done states fit one method:

```go
type ToolActivity struct {
    CallID        string
    ToolName      string
    ArgSummary    string        // on start
    Done          bool
    OK            bool          // on done: err == nil && Result.Error == ""
    ResultSummary string        // on done
    Output        string        // on done: capped raw output (for expand)
    Duration      time.Duration // on done
}

type ToolActivitySender interface {
    SendToolActivity(ctx context.Context, target MessageTarget, act ToolActivity) error
}
```

Blast radius (only TUI implements this; Telegram does not):
`channel.go`, `tui/adapter.go`, `gateway/tool_activity_reporter.go`,
`gateway/tool_routing_test.go` (mock), `tool/interceptor_activity.go` (+test).

#### 1c. TUI model

Reuse the existing `messages []chatMessage` slice and the `chatRev` render cache
— no transcript restructure.

```go
type workflowStep struct {
    callID        string
    tool          string
    arg           string
    done          bool
    ok            bool
    resultSummary string
    output        string        // capped raw, shown when expanded
    duration      time.Duration
}

// chatMessage gains:
//   role "step"
//   step *workflowStep  // non-nil when role == "step"
```

- `stepIndex map[string]int` maps callID → index in `messages` (indices are
  stable because messages are append-only).
- `stepsExpanded bool` — global expand state.
- Tool **start** → append a pending step message, record `stepIndex[callID]`,
  bump `chatRev`.
- Tool **done** → look up the step, update status/summary/output/duration in
  place, bump `chatRev`.
- Step messages **bypass the per-message `rendered` cache** (their render
  depends on `stepsExpanded`); they re-render on each static-chat rebuild. They
  are cheap one-liners when collapsed.

#### 1d. Rendering

Steps land between the user line and the agent line in chronological order, so
indentation plus a dim `│` guide groups them as that round's workflow with no
cursor layer:

```text
› 帮我查 episode 关闭逻辑

  │ ⚙ context_search · "episode close"     ✓ 0.4s · 3 hits
  │ ⚙ read episode.go                       ✓ 0.1s · 142 lines
  │ ⚙ grep "implicitClose"                  ⏵ running

⏺ episode 隐式关闭在 episode.go:142 …
```

Collapsed: one line per step — `│ ⚙ <tool> · <arg>   <status> <duration> · <result>`.
Status glyphs: `⏵` running (static — a step lives inside the `chatRev`-cached
chat string, so a per-tick animated spinner would force a chat re-render at the
tick rate; the bottom status bar already animates "what's running now"), `✓` ok
(green), `✗` error (red). Expanded (`Ctrl+T`): each step's capped raw output is
shown dim, indented under it.

#### 1e. Expand interaction

`Ctrl+T` toggles `m.stepsExpanded`, bumps `chatRev`, re-renders. When any step
exists, the status bar shows a `Ctrl+T details` hint. `Ctrl+T` is checked in
`handleChatKey` before the textarea consumes the key, mirroring `Ctrl+O`.

#### 1f. Status bar

Unchanged. It keeps showing the currently running tool ("what's running now");
the inline steps are the durable history. The existing
`activeTool`/`clearToolActivity` logic stays.

### Part 2 — Panel visual cleanup

Light and surgical (YAGNI — no full repaint, keep the calm palette):

- New step-specific styles in `styles.go`: guide line, running spinner, ok
  check (green), error mark (red), dim arg / duration / result meta.
- Consistent blank-line spacing between rounds so grouped steps read as a unit.

## Components

| Unit | Responsibility | Depends on |
| --- | --- | --- |
| `ActivityInterceptor` | emit start/done events with id, result, duration | `ToolActivityReporter` |
| `GatewayToolActivityReporter` | derive result summary, cap output, forward to channel | `ToolActivitySender` |
| `ToolActivity` (channel) | transport struct for one activity event | — |
| TUI `workflowStep` + `stepIndex` | hold/correlate per-round step state | `messages`, `chatRev` |
| TUI step render | render a step line (collapsed/expanded) under its round | step styles |

## Error Handling

- Tool error: step renders `✗` (red) with the error's first line as result
  summary; full error text available on expand. Execution is unaffected
  (reporter is informational, never blocks — preserved).
- Missing/duplicate done: correlation is by per-invocation `callID`; an orphan
  done (no matching start) is ignored.
- Oversized output: capped at the reporter before it reaches the TUI.

## Testing

- Render: step collapsed and expanded; `│` grouping between user and agent;
  result-summary derivation; output capping; running/ok/error glyphs.
- Plumbing: interceptor emits matching ids on start/done and carries
  result+duration on done; reporter derives summary and caps output.
- Interaction: `Ctrl+T` toggles expansion and invalidates the render cache
  (`chatRev` bump).

## Files Touched (~10)

- `internal/tool/interceptor_activity.go` (+ `_test.go`)
- `internal/channel/channel.go`
- `internal/gateway/tool_activity_reporter.go`
- `internal/gateway/tool_routing_test.go` (mock update)
- `internal/channel/tui/messages.go`
- `internal/channel/tui/adapter.go`
- `internal/channel/tui/model.go`
- `internal/channel/tui/model_update.go`
- `internal/channel/tui/model_view.go`
- `internal/channel/tui/styles.go`
- TUI tests (`formatter_test.go` / `model_view_test.go` as needed)

## Out of Scope

- Per-step focus cursor / per-step expand (Architecture B).
- Token usage and sub-agent activity in the trace (no channel events carry them
  today; would need separate plumbing).
- Telegram inline traces (it does not implement `ToolActivitySender`).
- Raw streaming output beyond the captured result (bash-only `ToolStreamWriter`
  path is left as-is).
