# Scheduler Channel — Design Spec

**Date:** 2026-06-09
**Status:** draft

## Problem

IronClaw currently only reacts to inbound messages from chat channels (Telegram, TUI). There is no way to schedule recurring tasks ("check email every morning") or keep the agent on standby for autonomous execution. Users want a 24/7 on-duty agent that can be driven by both scheduled cron tasks and ad-hoc Telegram messages.

## Architecture Decision

**Scheduler is implemented as a Channel** — a new `channel.Channel` implementation, peer to Telegram and TUI. From the Gateway's perspective, cron-fired messages are identical to user-typed messages. The Scheduler Channel owns cron scheduling and task persistence; it does NOT own notification delivery — its `Send()` method forwards to the real notification channel (Telegram).

### Why Channel, not a separate subsystem

- The Gateway already knows how to route inbound messages through the agent loop
- Channel abstraction handles `Start(ctx, handler)` / `Send(reply)` / `Stop(ctx)` — the scheduler needs all three
- Adding notification targets later (email, Slack) requires zero changes to scheduler code
- Session isolation is automatic: scheduler tasks get `Channel="scheduler"`, never collide with Telegram sessions

### Data flow

```
Cron fires → SchedulerChannel.fireTask()
    → InboundMessage{Channel:"scheduler", ChannelID:taskID, Text:prompt}
    → Gateway.handleInbound()
    → Agent.HandleMessage() [session="scheduler:taskID"]
    → LLM + tools
    → ch.Send(reply)
    → SchedulerChannel.Send() forwards to Telegram.Send()
    → User sees result in Telegram chat
```

Telegram messages interrupt at any time, using a separate session:
```
Telegram → Gateway.handleInbound() → Agent [session="telegram:chatID"] → reply
```

## Data Model

```sql
-- 025_scheduled_tasks.sql
CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id          TEXT PRIMARY KEY,
    prompt      TEXT NOT NULL,
    cron_expr   TEXT NOT NULL,
    notify_to   TEXT NOT NULL DEFAULT 'telegram',
    notify_id   TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    last_run_at TEXT,
    last_status TEXT
);
```

- `prompt`: natural language sent to the agent, identical to a Telegram message
- `cron_expr`: standard cron expression or `@every 1h30m` shorthand
- `notify_to` / `notify_id`: where to send results (maps to `channel.Channel.Name()` and chat ID)

## Components

### 1. SchedulerChannel (`internal/channel/scheduler/scheduler.go`)

Implements `channel.Channel`. Wraps `robfig/cron/v3`.

```go
type SchedulerChannel struct {
    db       *store.DB
    cron     *cron.Cron
    handler  channel.InboundHandler
    notifier channel.Channel   // real output channel (Telegram)
    entries  map[string]cron.EntryID
    mu       sync.Mutex
}
```

**Channel interface:**
- `Name()` → `"scheduler"`
- `Start(ctx, handler)` → load enabled tasks from DB, register with cron, start cron
- `Send(ctx, msg)` → delegate to `notifier.Send(ctx, msg)`. The `msg.ChannelID` is rewritten from the task's `notify_id` before forwarding.
- `Stop(ctx)` → stop cron, return when done
- `SendStreaming(ctx, target)` → delegate to notifier

**Management API (public methods, called by /schedule commands):**
- `AddTask(prompt, cronExpr, notifyTo, notifyID string) (*ScheduledTask, error)`
- `RemoveTask(id string) error`
- `SetEnabled(id string, enabled bool) error`
- `RunOnce(id string) error` — fires the task immediately without waiting for cron
- `ListTasks() ([]ScheduledTask, error)`

**Internal flow:**
- `fireTask(task)` — stores `task.ID → task.NotifyTo+":"+task.NotifyID` in `activeTargets sync.Map`, constructs `InboundMessage` with `Channel="scheduler"`, `ChannelID=task.ID`, `Text=task.Prompt`, calls `handler(ctx, msg)`
- Agent loop processes the message and calls `ch.Send(ctx, OutboundMessage{Channel:"scheduler", ChannelID:task.ID, Text:reply})`
- `Send()` looks up `activeTargets` by the original `ChannelID` (task.ID), rewrites `Channel`/`ChannelID` to the real target, calls `notifier.Send()`
- `last_run_at` and `last_status` updated after each execution; active target entry cleaned up

### 2. Gateway Integration (`internal/gateway/subsystem_scheduler.go`)

```go
type SchedulerSubsystem struct {
    Channel *scheduler.SchedulerChannel
}

func InitScheduler(db *store.DB, notifyCh channel.Channel) *SchedulerSubsystem { ... }
```

Wired in `gateway.New()`:
```go
gw.scheduler = InitScheduler(gw.db, tg)  // tg = telegram channel
gw.AddChannel(gw.scheduler)
```

### 3. Command Handlers (`internal/gateway/commands.go`)

`/schedule` prefix dispatches to sub-commands:

| Command | Action |
|---------|--------|
| `/schedule list` | List all tasks with status |
| `/schedule add <cron> <prompt>` | Add new task |
| `/schedule remove <id>` | Remove task by ID |
| `/schedule enable <id>` | Enable disabled task |
| `/schedule disable <id>` | Disable task (keeps in DB) |
| `/schedule run <id>` | Execute immediately |

Command handler added to `commandTable` in `InitCommands()`.

### 4. Dependencies

- `github.com/robfig/cron/v3` — new dependency, standard Go cron library
- No new database drivers, no new MCP servers, no config changes

## Session Isolation

Each scheduler task gets its own session keyed by `"scheduler:" + task.ID`. This means:
- Two concurrent cron fires do NOT share session state
- Telegram messages use `"telegram:" + chatID`, never collide with scheduler sessions
- Agent-level per-session mutex (added in PR prerequisite) prevents double-processing of any single session

## Concurrency

- `Agent.HandleMessage` is protected by per-session mutex (`sync.Map` of `*sync.Mutex` keyed by `"channel:channel_id"`)
- Scheduler uses `Channel="scheduler"` — different key namespace from Telegram
- No lock contention between scheduler and Telegram messages
- `SchedulerChannel.mu` protects the internal `entries` map during add/remove/enable operations
- `SchedulerChannel.activeTargets` (sync.Map) stores taskID→notifyTarget mappings for the duration of a task run; cleaned up on completion

## Error Handling

- Task execution failure: `last_status` set to `"error: <message>"`, agent replies with error text to Telegram
- Cron registration failure: task skipped on startup, logged, not blocking other tasks
- Notifier unavailable: `Send()` returns error, logged, task marked as errored
- DB unavailable: management commands return errors; cron loop continues with in-memory state

## Testing Strategy

- **Unit:** `SchedulerChannel` with mock notifier — verify cron fires produce correct InboundMessage
- **Unit:** Add/remove/enable/disable/list with in-memory SQLite
- **Integration:** Gateway + SchedulerChannel + mock Telegram — end-to-end cron→agent→reply flow
- **Concurrency:** Two simultaneous cron fires with different task IDs — verify isolated sessions, no deadlock

## File Manifest

| File | Action | Lines (est.) |
|------|--------|-------------|
| `internal/channel/scheduler/scheduler.go` | New | ~200 |
| `internal/channel/scheduler/scheduler_test.go` | New | ~100 |
| `internal/gateway/subsystem_scheduler.go` | New | ~40 |
| `internal/store/migrations/025_scheduled_tasks.sql` | New | ~10 |
| `go.mod` | Edit | +1 require |
| `internal/gateway/gateway.go` | Edit | +5 lines |
| `internal/gateway/commands.go` | Edit | +2 lines (command table) |
| `internal/gateway/scheduler_commands.go` | New | ~80 |
| `cmd/ironclaw/main.go` | No change | — |
