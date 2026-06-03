# 07. Channels, Dashboard, and Observability

Channels are adapters. They turn external input into `channel.InboundMessage` and render `channel.OutboundMessage` back to a user.

## Channel Interface

```go
type Channel interface {
    Name() string
    Start(ctx context.Context, handler InboundHandler) error
    Send(ctx context.Context, msg OutboundMessage) error
    SendStreaming(ctx context.Context, target MessageTarget) (StreamUpdater, error)
    Stop(ctx context.Context) error
}
```

Optional interfaces add capabilities:

| Interface | Purpose |
|---|---|
| `ApprovalSender` | Interactive approval for tool execution. |
| `ReflectionSender` | User choice during replan/reflection prompts. |
| `NotificationSender` | Lightweight runtime notifications. |
| `FeedbackSender` | User satisfaction feedback. |
| `ToolStreamWriter` | Real-time streaming chunks from long-running tools. |

Gateway checks these interfaces dynamically. Channels that do not support approval auto-approve through `handleApproval`.

## TUI

`internal/channel/tui` is the local interactive terminal interface built with Bubble Tea.

Important behavior:

- Each launch uses a unique channel ID so sessions do not collide.
- `tui.auto_approve` can skip approval prompts.
- TUI receives dashboard URL when dashboard is enabled.
- Gateway injects dynamic slash-command argument completion.
- TUI has its own observability emitter and metrics emitter, merged with dashboard emitter after Gateway start.
- User input cancels the in-flight request before dispatching a new one.

## Telegram and Discord

Telegram and Discord adapters both support asynchronous message handling to avoid deadlocks while waiting on approval callbacks.

Common capabilities:

- User allowlists.
- Approval requests.
- Reflection/replan decisions.
- Feedback prompts.
- Notifications.
- Streaming responses where supported by the platform.

Formatting is platform-specific:

- Telegram truncates near Telegram message limits.
- Discord truncates near Discord message limits.

## Scheduler as Channel Source

Scheduler tasks are converted into `channel.InboundMessage` with user identity `scheduler`. The scheduled prompt enters the same Gateway message path as interactive user messages.

## Dashboard Backend

`internal/dashboard/server.go` defines the dashboard mux.

Routes:

| Route | Auth | Purpose |
|---|---:|---|
| `/health` | no | JSON health status. |
| `/metrics` | no | Prometheus metrics handler. |
| `/api/agent/state` | yes if token set | Current agent state snapshot. |
| `/api/sessions` | yes if token set | Last 50 sessions. |
| `/api/sessions/{id}/messages` | yes if token set | Messages for a session. |
| `/api/sessions/{id}/tools` | yes if token set | Tool log entries for a session. |
| `/ws` | yes if token set | WebSocket event stream. |
| `/api/metrics/health` | yes if token set | Cognitive metrics snapshot. |
| `/...` | no route auth | SPA fallback for embedded static files. |

Token auth accepts either `Authorization: Bearer <token>` or `?token=<token>`.

```mermaid
flowchart LR
    Agent[Agent events] --> Tracker[AgentStateTracker]
    Agent --> Hub[WebSocket Hub]
    Store[(SQLite)] --> Sessions[/api/sessions]
    Store --> Messages[/api/sessions/{id}/messages]
    Store --> ToolLog[/api/sessions/{id}/tools]
    Cog[cogmetrics Collector] --> Health[/api/metrics/health]
    Static[embedded dashboard FS] --> SPA[SPA fallback]
```

## Embedded Dashboard Frontend

`web/src/lib/api.ts` calls the dashboard routes:

- `fetchAgentState`
- `fetchSessions`
- `fetchSessionMessages`
- `fetchSessionTools`
- `fetchMetricsHealth`

The Preact routes are:

- `/`
- `/sessions`
- `/sessions/:id`
- `/metrics`

## Observability

`internal/observability` owns OpenTelemetry setup:

- Exporters: OTLP gRPC, OTLP HTTP, stdout, noop.
- Service name and endpoint are config-driven.
- Sample rate is config-driven.
- Tool execution spans and metrics are recorded in the interceptor chain.

Prometheus metrics are exposed through `/metrics`. Cognitive metrics are collected through `internal/cogmetrics` and surfaced to dashboard/eval.

## Health and Rate Limit

- `internal/health` tracks named health checkers such as database and Docker.
- Gateway starts a health HTTP server independent of dashboard.
- `internal/ratelimit` can rate-limit inbound agent message handling by user.

Rate-limited users receive a channel response when possible.
