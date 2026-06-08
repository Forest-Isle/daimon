# 07. Channels and Observability

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
- Gateway injects dynamic slash-command argument completion.
- TUI has its own observability emitter and metrics emitter, surfaced in the TUI status bar after Gateway start.
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

## Observability

`internal/observability` owns OpenTelemetry setup:

- Exporters: OTLP gRPC, OTLP HTTP, stdout, noop.
- Service name and endpoint are config-driven.
- Sample rate is config-driven.
- Tool execution spans and metrics are recorded in the interceptor chain.

The TUI status bar surfaces metrics through the agent `ObservabilityEmitter`.

## Health and Rate Limit

- `internal/health` tracks named health checkers such as database and Docker.
- Gateway starts a standalone health HTTP server (`/healthz`, `/readyz`, `/health`).
- `internal/ratelimit` can rate-limit inbound agent message handling by user.

Rate-limited users receive a channel response when possible.
