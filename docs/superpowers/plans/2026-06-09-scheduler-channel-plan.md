# Scheduler Channel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add cron-based scheduled task execution to IronClaw via a SchedulerChannel that implements `channel.Channel`, allowing users to manage recurring tasks through Telegram commands.

**Architecture:** SchedulerChannel is a new `channel.Channel` implementation. It wraps `robfig/cron/v3` for scheduling, persists tasks in SQLite, and forwards agent replies to the real notification channel (Telegram). From Gateway's perspective, cron-fired messages are identical to user messages — same inbound handler, same agent loop.

**Tech Stack:** Go 1.25, robfig/cron/v3, SQLite (existing), channel.Channel interface (existing)

**Prerequisite:** Agent per-session mutex already merged (agent.go: sessionLocks sync.Map).

---

## Task 1: Add Dependency and SQL Migration

**Files:**
- Modify: `go.mod:22` (new require line)
- Create: `internal/store/migrations/025_scheduled_tasks.sql`

- [ ] **Step 1: Add robfig/cron/v3**

```bash
cd /Users/wuqisen/dev/IronClaw && go get github.com/robfig/cron/v3@v3.0.1
```

Expected: `go.mod` gains `github.com/robfig/cron/v3 v3.0.1` in require block.

- [ ] **Step 2: Create migration file**

```sql
-- 025_scheduled_tasks.sql: Add scheduled tasks table for SchedulerChannel
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

Write to `internal/store/migrations/025_scheduled_tasks.sql`.

- [ ] **Step 3: Verify migration compiles into binary**

```bash
go build ./internal/store/...
```

Expected: builds clean (migration is embedded via `//go:embed`).

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum internal/store/migrations/025_scheduled_tasks.sql
git commit -m "feat: add robfig/cron and scheduled_tasks migration"
```

---

## Task 2: Create SchedulerChannel Core

**Files:**
- Create: `internal/channel/scheduler/scheduler.go`

- [ ] **Step 1: Create the scheduler package with struct and ScheduledTask type**

```go
// Package scheduler implements a cron-driven channel.Channel that fires
// InboundMessage tasks on a schedule and forwards replies to a notifier channel.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// ScheduledTask is a persisted cron task.
type ScheduledTask struct {
	ID         string
	Prompt     string
	CronExpr   string
	NotifyTo   string
	NotifyID   string
	Enabled    bool
	CreatedAt  string
	LastRunAt  string
	LastStatus string
}

// SchedulerChannel implements channel.Channel for cron-driven task execution.
// Its Send() method forwards replies to a configured notifier channel.
type SchedulerChannel struct {
	db            *store.DB
	cron          *cron.Cron
	handler       channel.InboundHandler
	notifier      channel.Channel
	entries       map[string]cron.EntryID
	activeTargets sync.Map // taskID → "channel:channelID"
	mu            sync.Mutex
}

// New creates a SchedulerChannel.
func New(db *store.DB, notifier channel.Channel) *SchedulerChannel {
	return &SchedulerChannel{
		db:       db,
		notifier: notifier,
		cron: cron.New(cron.WithSeconds()),
		entries: make(map[string]cron.EntryID),
	}
}

// Name returns the channel identifier.
func (s *SchedulerChannel) Name() string { return "scheduler" }

// Start loads enabled tasks from the database, registers them with cron,
// and begins the cron scheduler. handler is called for each cron fire.
func (s *SchedulerChannel) Start(ctx context.Context, handler channel.InboundHandler) error {
	s.handler = handler

	tasks, err := s.loadEnabledTasks(ctx)
	if err != nil {
		return fmt.Errorf("scheduler: load tasks: %w", err)
	}

	for _, t := range tasks {
		s.registerCron(t)
	}

	s.cron.Start()
	slog.Info("scheduler started", "tasks", len(tasks))
	return nil
}

// Send forwards the agent's reply to the notifier channel.
func (s *SchedulerChannel) Send(ctx context.Context, msg channel.OutboundMessage) error {
	s.rewriteTarget(&msg)
	return s.notifier.Send(ctx, msg)
}

// SendStreaming delegates streaming to the notifier.
func (s *SchedulerChannel) SendStreaming(ctx context.Context, target channel.MessageTarget) (channel.StreamUpdater, error) {
	s.rewriteTargetMsg(&target)
	return s.notifier.SendStreaming(ctx, target)
}

// Stop halts the cron scheduler and waits for running jobs to finish.
func (s *SchedulerChannel) Stop(_ context.Context) error {
	ctx := s.cron.Stop()
	<-ctx.Done()
	slog.Info("scheduler stopped")
	return nil
}
```

Write to `internal/channel/scheduler/scheduler.go`.

- [ ] **Step 2: Add target rewriting helpers**

Append to `internal/channel/scheduler/scheduler.go`:

```go
// rewriteTarget looks up the original taskID in activeTargets and rewrites
// the message's Channel/ChannelID to the real notification target.
func (s *SchedulerChannel) rewriteTarget(msg *channel.OutboundMessage) {
	if msg.Channel != "scheduler" {
		return
	}
	v, ok := s.activeTargets.Load(msg.ChannelID)
	if !ok {
		return
	}
	parts := v.(string) // "notifyTo:notifyID"
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == ':' {
			msg.Channel = parts[:i]
			msg.ChannelID = parts[i+1:]
			return
		}
	}
}

func (s *SchedulerChannel) rewriteTargetMsg(target *channel.MessageTarget) {
	if target.Channel != "scheduler" {
		return
	}
	v, ok := s.activeTargets.Load(target.ChannelID)
	if !ok {
		return
	}
	parts := v.(string)
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == ':' {
			target.Channel = parts[:i]
			target.ChannelID = parts[i+1:]
			return
		}
	}
}
```

- [ ] **Step 3: Add DB load, cron register, and fireTask**

Append to `internal/channel/scheduler/scheduler.go`:

```go
// loadEnabledTasks reads all enabled tasks from the database.
func (s *SchedulerChannel) loadEnabledTasks(ctx context.Context) ([]ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, prompt, cron_expr, notify_to, notify_id, enabled, created_at,
		        COALESCE(last_run_at, ''), COALESCE(last_status, '')
		 FROM scheduled_tasks WHERE enabled = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []ScheduledTask
	for rows.Next() {
		var t ScheduledTask
		if err := rows.Scan(&t.ID, &t.Prompt, &t.CronExpr, &t.NotifyTo, &t.NotifyID,
			&t.Enabled, &t.CreatedAt, &t.LastRunAt, &t.LastStatus); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// registerCron adds a task to the cron scheduler.
func (s *SchedulerChannel) registerCron(t ScheduledTask) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.entries[t.ID]; exists {
		return
	}

	task := t // capture
	entryID, err := s.cron.AddFunc(task.CronExpr, func() {
		s.fireTask(task)
	})
	if err != nil {
		slog.Error("scheduler: failed to register cron", "task", t.ID, "expr", t.CronExpr, "err", err)
		return
	}
	s.entries[t.ID] = entryID
}

// unregisterCron removes a task from the cron scheduler.
func (s *SchedulerChannel) unregisterCron(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entryID, exists := s.entries[taskID]; exists {
		s.cron.Remove(entryID)
		delete(s.entries, taskID)
	}
}

// fireTask is called by cron when a task is due.
func (s *SchedulerChannel) fireTask(t ScheduledTask) {
	targetKey := t.NotifyTo + ":" + t.NotifyID
	s.activeTargets.Store(t.ID, targetKey)
	defer s.activeTargets.Delete(t.ID)

	s.setLastRun(t.ID)

	msg := channel.InboundMessage{
		Channel:   "scheduler",
		ChannelID: t.ID,
		Text:      t.Prompt,
	}
	if s.handler != nil {
		s.handler(context.Background(), msg)
	}
}

// setLastRun updates the task's last_run_at timestamp.
func (s *SchedulerChannel) setLastRun(taskID string) {
	_, err := s.db.Exec(`UPDATE scheduled_tasks SET last_run_at = datetime('now'), last_status = 'running' WHERE id = ?`, taskID)
	if err != nil {
		slog.Warn("scheduler: failed to update last_run_at", "task", taskID, "err", err)
	}
}
```

- [ ] **Step 4: Add management API methods**

Append to `internal/channel/scheduler/scheduler.go`:

```go
// AddTask persists a new scheduled task and registers it with cron if enabled.
func (s *SchedulerChannel) AddTask(ctx context.Context, prompt, cronExpr, notifyTo, notifyID string) (*ScheduledTask, error) {
	t := ScheduledTask{
		ID:       uuid.New().String(),
		Prompt:   prompt,
		CronExpr: cronExpr,
		NotifyTo: notifyTo,
		NotifyID: notifyID,
		Enabled:  true,
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO scheduled_tasks (id, prompt, cron_expr, notify_to, notify_id) VALUES (?, ?, ?, ?, ?)`,
		t.ID, t.Prompt, t.CronExpr, t.NotifyTo, t.NotifyID)
	if err != nil {
		return nil, fmt.Errorf("scheduler: insert task: %w", err)
	}

	s.registerCron(t)
	slog.Info("scheduler: task added", "id", t.ID, "expr", t.CronExpr)
	return &t, nil
}

// RemoveTask deletes a task and unregisters it from cron.
func (s *SchedulerChannel) RemoveTask(ctx context.Context, id string) error {
	s.unregisterCron(id)
	_, err := s.db.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("scheduler: delete task: %w", err)
	}
	slog.Info("scheduler: task removed", "id", id)
	return nil
}

// SetEnabled enables or disables a task.
func (s *SchedulerChannel) SetEnabled(ctx context.Context, id string, enabled bool) error {
	val := 0
	if enabled {
		val = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE scheduled_tasks SET enabled = ? WHERE id = ?`, val, id)
	if err != nil {
		return fmt.Errorf("scheduler: update enabled: %w", err)
	}

	if enabled {
		// Reload task from DB to register
		t, err := s.getTask(ctx, id)
		if err != nil {
			return fmt.Errorf("scheduler: get task after enable: %w", err)
		}
		s.registerCron(*t)
	} else {
		s.unregisterCron(id)
	}
	return nil
}

// RunOnce fires a task immediately without waiting for its cron schedule.
func (s *SchedulerChannel) RunOnce(ctx context.Context, id string) error {
	t, err := s.getTask(ctx, id)
	if err != nil {
		return err
	}
	go s.fireTask(*t)
	return nil
}

// ListTasks returns all scheduled tasks ordered by creation time.
func (s *SchedulerChannel) ListTasks(ctx context.Context) ([]ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, prompt, cron_expr, notify_to, notify_id, enabled, created_at,
		        COALESCE(last_run_at, ''), COALESCE(last_status, '')
		 FROM scheduled_tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []ScheduledTask
	for rows.Next() {
		var t ScheduledTask
		if err := rows.Scan(&t.ID, &t.Prompt, &t.CronExpr, &t.NotifyTo, &t.NotifyID,
			&t.Enabled, &t.CreatedAt, &t.LastRunAt, &t.LastStatus); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// getTask fetches a single task by ID.
func (s *SchedulerChannel) getTask(ctx context.Context, id string) (*ScheduledTask, error) {
	var t ScheduledTask
	err := s.db.QueryRowContext(ctx,
		`SELECT id, prompt, cron_expr, notify_to, notify_id, enabled, created_at,
		        COALESCE(last_run_at, ''), COALESCE(last_status, '')
		 FROM scheduled_tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.Prompt, &t.CronExpr, &t.NotifyTo, &t.NotifyID,
			&t.Enabled, &t.CreatedAt, &t.LastRunAt, &t.LastStatus)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("scheduler: task %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("scheduler: get task: %w", err)
	}
	return &t, nil
}
```

- [ ] **Step 5: Verify builds clean**

```bash
go build ./internal/channel/scheduler/...
```

Expected: builds clean, no unused imports.

- [ ] **Step 6: Commit**

```bash
git add internal/channel/scheduler/scheduler.go
git commit -m "feat: add SchedulerChannel core implementation"
```

---

## Task 3: Create Gateway Subsystem Wrapper

**Files:**
- Create: `internal/gateway/subsystem_scheduler.go`

- [ ] **Step 1: Create subsystem wrapper**

```go
package gateway

import (
	"context"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/channel/scheduler"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

// SchedulerSubsystem wraps the SchedulerChannel for Gateway lifecycle management.
type SchedulerSubsystem struct {
	Channel *scheduler.SchedulerChannel
}

func (ss *SchedulerSubsystem) Name() string                { return "scheduler" }
func (ss *SchedulerSubsystem) Start(_ context.Context) error { return nil }
func (ss *SchedulerSubsystem) Stop(_ context.Context) error  { return nil }

// InitScheduler creates the SchedulerChannel.
// notifyCh is the channel that receives forwarded replies (typically Telegram).
func InitScheduler(db *store.DB, notifyCh channel.Channel) *SchedulerSubsystem {
	sc := scheduler.New(db, notifyCh)
	return &SchedulerSubsystem{Channel: sc}
}
```

Write to `internal/gateway/subsystem_scheduler.go`.

- [ ] **Step 2: Verify builds**

```bash
go build ./internal/gateway/...
```

Expected: builds clean.

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/subsystem_scheduler.go
git commit -m "feat: add SchedulerSubsystem gateway wrapper"
```

---

## Task 4: Wire Scheduler into Gateway

**Files:**
- Modify: `internal/gateway/gateway.go:22-47` (Gateway struct)
- Modify: `internal/gateway/gateway.go:53-115` (New function)

- [ ] **Step 1: Add scheduler field to Gateway struct**

In `internal/gateway/gateway.go`, add after `commands` field (line 44):

```go
	scheduler   *SchedulerSubsystem
```

- [ ] **Step 2: Add InitScheduler call in New()**

In `internal/gateway/gateway.go`, in the `New` function, add after the command subsystem init (after line 104, before the subsystems assignment at line 113):

```go
	// tg is set in main.go via AddChannel — scheduler needs it for reply forwarding.
	// We pass nil here; the actual notifier is set in main.go after telegram is created.
	gw.scheduler = InitScheduler(gw.db, nil)
	gw.AddChannel(gw.scheduler.Channel)
```

Wait — we need the Telegram channel reference. Since Telegram is created in `main.go` after `gateway.New()`, we can't pass it during init. Instead, set the notifier after construction.

Replace the above with:

```go
	// SchedulerChannel notifier is wired post-construction in main.go
	// after the Telegram channel is created.
	gw.scheduler = InitScheduler(gw.db, nil)
	gw.AddChannel(gw.scheduler.Channel)
```

- [ ] **Step 3: Add SetNotifier method to SchedulerChannel**

In `internal/channel/scheduler/scheduler.go`, add:

```go
// SetNotifier sets the channel that receives forwarded replies.
// Call this after construction if the notifier wasn't available at init time.
func (s *SchedulerChannel) SetNotifier(ch channel.Channel) {
	s.notifier = ch
}
```

- [ ] **Step 4: Add SetNotifier to Gateway**

In `internal/gateway/gateway.go`, after `AddChannel` method (after line 123):

```go
// SetSchedulerNotifier wires the scheduler's reply forwarding to a real channel.
func (gw *Gateway) SetSchedulerNotifier(ch channel.Channel) {
	if gw.scheduler != nil {
		gw.scheduler.Channel.SetNotifier(ch)
	}
}
```

- [ ] **Step 5: Update main.go to wire notifier**

In `cmd/ironclaw/main.go`, in `runStart()`, add after `gw.AddChannel(tg)` (after line 306):

```go
	gw.SetSchedulerNotifier(tg)
```

- [ ] **Step 6: Verify full build**

```bash
go build ./...
```

Expected: builds clean.

- [ ] **Step 7: Commit**

```bash
git add internal/gateway/gateway.go internal/channel/scheduler/scheduler.go cmd/ironclaw/main.go
git commit -m "feat: wire SchedulerChannel into Gateway and main"
```

---

## Task 5: Create Scheduler Command Handlers

**Files:**
- Create: `internal/gateway/scheduler_commands.go`

- [ ] **Step 1: Create the scheduler command handler file**

```go
package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/channel/scheduler"
)

// handleSchedule dispatches /schedule sub-commands.
func (gw *Gateway) handleSchedule(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) (string, error) {
	if gw.scheduler == nil || gw.scheduler.Channel == nil {
		return "Scheduler is not available.", nil
	}

	sc := gw.scheduler.Channel
	args := strings.TrimPrefix(msg.Text, "/schedule")
	args = strings.TrimSpace(args)

	if args == "" || args == "help" {
		return `**Schedule Commands**
- /schedule list — list all tasks
- /schedule add <cron> <prompt> — add a task
- /schedule remove <id> — remove a task
- /schedule enable <id> — enable a disabled task
- /schedule disable <id> — disable a task
- /schedule run <id> — run a task immediately

Cron examples: "@every 1h", "0 9 * * *", "@daily"`, nil
	}

	parts := splitCmd(args)
	if len(parts) == 0 {
		return "Usage: /schedule <list|add|remove|enable|disable|run>", nil
	}

	switch parts[0] {
	case "list":
		return gw.scheduleList(ctx, sc)
	case "add":
		return gw.scheduleAdd(ctx, sc, msg, parts[1:])
	case "remove":
		return gw.scheduleRemove(ctx, sc, parts[1:])
	case "enable":
		return gw.scheduleEnable(ctx, sc, parts[1:], true)
	case "disable":
		return gw.scheduleEnable(ctx, sc, parts[1:], false)
	case "run":
		return gw.scheduleRun(ctx, sc, parts[1:])
	default:
		return fmt.Sprintf("Unknown sub-command: %s. Try /schedule help.", parts[0]), nil
	}
}

func (gw *Gateway) scheduleList(ctx context.Context, sc *scheduler.SchedulerChannel) (string, error) {
	tasks, err := sc.ListTasks(ctx)
	if err != nil {
		return "", fmt.Errorf("list tasks: %w", err)
	}
	if len(tasks) == 0 {
		return "No scheduled tasks.", nil
	}

	var b strings.Builder
	b.WriteString("**Scheduled Tasks**\n\n")
	for _, t := range tasks {
		status := "✅"
		if !t.Enabled {
			status = "⏸️"
		}
		fmt.Fprintf(&b, "%s **%s** — `%s`\n", status, truncateID(t.ID), t.CronExpr)
		fmt.Fprintf(&b, "  %s\n", t.Prompt)
		if t.LastRunAt != "" {
			fmt.Fprintf(&b, "  Last: %s (%s)\n", t.LastRunAt, t.LastStatus)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func (gw *Gateway) scheduleAdd(ctx context.Context, sc *scheduler.SchedulerChannel, msg channel.InboundMessage, args []string) (string, error) {
	if len(args) < 2 {
		return "Usage: /schedule add <cron_expr> <prompt>", nil
	}

	cronExpr := args[0]
	prompt := strings.Join(args[1:], " ")

	t, err := sc.AddTask(ctx, prompt, cronExpr, msg.Channel, msg.ChannelID)
	if err != nil {
		return "", fmt.Errorf("add task: %w", err)
	}

	return fmt.Sprintf("Task added: **%s** (`%s`): %s", truncateID(t.ID), t.CronExpr, t.Prompt), nil
}

func (gw *Gateway) scheduleRemove(ctx context.Context, sc *scheduler.SchedulerChannel, args []string) (string, error) {
	if len(args) < 1 {
		return "Usage: /schedule remove <id>", nil
	}
	if err := sc.RemoveTask(ctx, args[0]); err != nil {
		return "", fmt.Errorf("remove task: %w", err)
	}
	return fmt.Sprintf("Task removed: %s", args[0]), nil
}

func (gw *Gateway) scheduleEnable(ctx context.Context, sc *scheduler.SchedulerChannel, args []string, enable bool) (string, error) {
	if len(args) < 1 {
		action := "enable"
		if !enable {
			action = "disable"
		}
		return fmt.Sprintf("Usage: /schedule %s <id>", action), nil
	}
	if err := sc.SetEnabled(ctx, args[0], enable); err != nil {
		return "", fmt.Errorf("%s task: %w", map[bool]string{true: "enable", false: "disable"}[enable], err)
	}
	state := "enabled"
	if !enable {
		state = "disabled"
	}
	return fmt.Sprintf("Task %s now %s.", args[0], state), nil
}

func (gw *Gateway) scheduleRun(ctx context.Context, sc *scheduler.SchedulerChannel, args []string) (string, error) {
	if len(args) < 1 {
		return "Usage: /schedule run <id>", nil
	}
	if err := sc.RunOnce(ctx, args[0]); err != nil {
		return "", fmt.Errorf("run task: %w", err)
	}
	return fmt.Sprintf("Task %s triggered. Check back for results.", args[0]), nil
}

// splitCmd splits a string by spaces, respecting quoted strings.
func splitCmd(s string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func truncateID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
```

Write to `internal/gateway/scheduler_commands.go`.

- [ ] **Step 2: Verify builds**

```bash
go build ./internal/gateway/...
```

Expected: builds clean.

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/scheduler_commands.go
git commit -m "feat: add /schedule command handlers"
```

---

## Task 6: Register /schedule in Command Table

**Files:**
- Modify: `internal/gateway/subsystem_command.go:16-28`

- [ ] **Step 1: Add /schedule to command table**

In `internal/gateway/subsystem_command.go`, in `InitCommands`, add after line 25 (`"/start"`):

```go
			"/schedule": {gwRef.handleSchedule, false},
```

The result should look like:

```go
	cs.Table = commandTable{
		"/mode":    {gwRef.handleMode, false},
		"/feature": {gwRef.handleFeature, false},
		"/config":  {gwRef.handleConfig, true},
		"/compact": {gwRef.handleCompact, true},
		"/model":   {gwRef.handleModel, false},
		"/new":     {gwRef.handleReset, true},
		"/start":   {gwRef.handleReset, true},
		"/schedule": {gwRef.handleSchedule, false},
	}
```

- [ ] **Step 2: Verify builds and commands work**

```bash
go build ./...
go vet ./internal/gateway/...
```

Expected: builds clean, no vet warnings.

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/subsystem_command.go
git commit -m "feat: register /schedule command"
```

---

## Task 7: Write Tests

**Files:**
- Create: `internal/channel/scheduler/scheduler_test.go`

- [ ] **Step 1: Create test file with in-memory DB helpers**

```go
package scheduler

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	// Open with full migrations so scheduled_tasks table exists
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)

	// Run just our migration — the store package auto-migrates via embed,
	// but for test we need the full store.Open which runs all migrations.
	// Use store.Open for proper migration.
	storePath := filepath.Join(dir, "ironclaw_test.db")
	sdb, err := store.Open(storePath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { sdb.Close() })
	return sdb
}
```

Wait — `store.Open` runs ALL migrations which needs `fts5`. Tests without `-tags fts5` will fail. Use a lighter approach:

```go
package scheduler

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	_ "github.com/mattn/go-sqlite3"
)

// openTestDB creates an in-memory SQLite database with the scheduled_tasks table.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS scheduled_tasks (
		id TEXT PRIMARY KEY, prompt TEXT NOT NULL, cron_expr TEXT NOT NULL,
		notify_to TEXT NOT NULL DEFAULT 'telegram', notify_id TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		last_run_at TEXT, last_status TEXT
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// testStore wraps *sql.DB as *store.DB for the scheduler.
type testStore struct{ *sql.DB }

func (ts *testStore) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return ts.DB.QueryContext(ctx, query, args...)
}

func (ts *testStore) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return ts.DB.ExecContext(ctx, query, args...)
}

func (ts *testStore) Exec(query string, args ...any) (sql.Result, error) {
	return ts.DB.Exec(query, args...)
}

func (ts *testStore) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return ts.DB.QueryRowContext(ctx, query, args...)
}
```

Hmm, this is getting complicated because `SchedulerChannel` depends on `*store.DB`. A simpler approach: make `SchedulerChannel` accept a `DB` interface instead. But changing the design now is scope creep. Simplest approach: use `store.Open` and mark the test with a build tag, or use the temp dir approach and accept the fts5 requirement.

Actually, looking at other tests in the codebase, they use `store.Open` and run with `-tags fts5`. Let me follow that pattern.

```go
package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
```

And document that tests need `-tags fts5`. That's consistent with the rest of the codebase.

- [ ] **Step 1 continued: Write test file**

```go
package scheduler

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// mockNotifier implements channel.Channel for test assertions.
type mockNotifier struct {
	mu       sync.Mutex
	messages []channel.OutboundMessage
}

func (m *mockNotifier) Name() string       { return "mock" }
func (m *mockNotifier) Start(_ context.Context, _ channel.InboundHandler) error { return nil }
func (m *mockNotifier) Stop(_ context.Context) error { return nil }
func (m *mockNotifier) Send(_ context.Context, msg channel.OutboundMessage) error {
	m.mu.Lock()
	m.messages = append(m.messages, msg)
	m.mu.Unlock()
	return nil
}
func (m *mockNotifier) SendStreaming(_ context.Context, _ channel.MessageTarget) (channel.StreamUpdater, error) {
	return nil, nil
}
func (m *mockNotifier) lastMsg() *channel.OutboundMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 { return nil }
	return &m.messages[len(m.messages)-1]
}

func TestNew(t *testing.T) {
	db := openTestDB(t)
	notifier := &mockNotifier{}
	sc := New(db, notifier)

	if sc.Name() != "scheduler" {
		t.Errorf("expected name 'scheduler', got %q", sc.Name())
	}
}

func TestAddAndListTasks(t *testing.T) {
	db := openTestDB(t)
	notifier := &mockNotifier{}
	sc := New(db, notifier)

	ctx := context.Background()
	task, err := sc.AddTask(ctx, "check email", "@every 1h", "telegram", "12345")
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if task.ID == "" {
		t.Error("expected non-empty ID")
	}
	if task.CronExpr != "@every 1h" {
		t.Errorf("expected '@every 1h', got %q", task.CronExpr)
	}

	tasks, err := sc.ListTasks(ctx)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Prompt != "check email" {
		t.Errorf("expected 'check email', got %q", tasks[0].Prompt)
	}
}

func TestRemoveTask(t *testing.T) {
	db := openTestDB(t)
	notifier := &mockNotifier{}
	sc := New(db, notifier)

	ctx := context.Background()
	task, _ := sc.AddTask(ctx, "test", "@daily", "telegram", "123")
	if err := sc.RemoveTask(ctx, task.ID); err != nil {
		t.Fatalf("RemoveTask: %v", err)
	}

	tasks, _ := sc.ListTasks(ctx)
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks after remove, got %d", len(tasks))
	}
}

func TestEnableDisable(t *testing.T) {
	db := openTestDB(t)
	notifier := &mockNotifier{}
	sc := New(db, notifier)

	ctx := context.Background()
	task, _ := sc.AddTask(ctx, "test", "@daily", "telegram", "123")

	if err := sc.SetEnabled(ctx, task.ID, false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}

	tasks, _ := sc.ListTasks(ctx)
	if tasks[0].Enabled {
		t.Error("expected task disabled")
	}

	if err := sc.SetEnabled(ctx, task.ID, true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}

	tasks, _ = sc.ListTasks(ctx)
	if !tasks[0].Enabled {
		t.Error("expected task enabled")
	}
}

func TestCronFiresTask(t *testing.T) {
	db := openTestDB(t)
	notifier := &mockNotifier{}
	sc := New(db, notifier)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Capture the inbound message when cron fires
	var capturedMsg channel.InboundMessage
	var msgMu sync.Mutex
	handler := func(_ context.Context, msg channel.InboundMessage) {
		msgMu.Lock()
		capturedMsg = msg
		msgMu.Unlock()
	}

	// Add a task that fires every second
	task, err := sc.AddTask(ctx, "test prompt", "@every 1s", "telegram", "chat_42")
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	if err := sc.Start(ctx, handler); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for cron to fire at least once
	time.Sleep(1500 * time.Millisecond)

	msgMu.Lock()
	got := capturedMsg
	msgMu.Unlock()

	if got.Channel != "scheduler" {
		t.Errorf("expected Channel 'scheduler', got %q", got.Channel)
	}
	if got.ChannelID != task.ID {
		t.Errorf("expected ChannelID %q, got %q", task.ID, got.ChannelID)
	}
	if got.Text != "test prompt" {
		t.Errorf("expected Text 'test prompt', got %q", got.Text)
	}

	// Also verify Send rewrites target
	reply := channel.OutboundMessage{Channel: "scheduler", ChannelID: task.ID, Text: "done"}
	if err := sc.Send(ctx, reply); err != nil {
		t.Fatalf("Send: %v", err)
	}

	last := notifier.lastMsg()
	if last == nil {
		t.Fatal("expected notifier to receive forwarded message")
	}
	if last.Channel != "telegram" {
		t.Errorf("expected Channel 'telegram', got %q", last.Channel)
	}
	if last.ChannelID != "chat_42" {
		t.Errorf("expected ChannelID 'chat_42', got %q", last.ChannelID)
	}
	if last.Text != "done" {
		t.Errorf("expected Text 'done', got %q", last.Text)
	}

	sc.Stop(context.Background())
}

func TestStartNoTasks(t *testing.T) {
	db := openTestDB(t)
	notifier := &mockNotifier{}
	sc := New(db, notifier)

	ctx := context.Background()
	if err := sc.Start(ctx, func(_ context.Context, _ channel.InboundMessage) {}); err != nil {
		t.Fatalf("Start with no tasks: %v", err)
	}
	sc.Stop(context.Background())
}

func TestCompileTimeChannelInterface(t *testing.T) {
	var _ channel.Channel = (*SchedulerChannel)(nil)
}
```

Write to `internal/channel/scheduler/scheduler_test.go`.

- [ ] **Step 2: Run tests**

```bash
go test -tags fts5 -v ./internal/channel/scheduler/...
```

Expected: all tests PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/channel/scheduler/scheduler_test.go
git commit -m "test: add SchedulerChannel tests"
```

---

## Task 8: Final Build & Integration Check

- [ ] **Step 1: Full build**

```bash
go build ./...
```

Expected: zero errors.

- [ ] **Step 2: Full vet**

```bash
go vet ./...
```

Expected: zero warnings.

- [ ] **Step 3: Run all existing tests**

```bash
go test -tags fts5 -count=1 ./internal/...
```

Expected: all pass (same failures as before this feature are acceptable if pre-existing).

- [ ] **Step 4: Check race detector on new code**

```bash
go test -tags fts5 -race -v ./internal/channel/scheduler/...
```

Expected: no race conditions detected.

- [ ] **Step 5: Commit final state**

```bash
git add -A
git diff --cached --stat
git commit -m "chore: final integration check for scheduler feature"
```
