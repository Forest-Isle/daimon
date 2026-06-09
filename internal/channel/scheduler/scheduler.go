// Package scheduler implements a cron-driven channel.Channel that fires
// InboundMessage tasks on a schedule and forwards replies to a notifier channel.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"

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
	NotifyTo   string // maps to DB column "channel"
	NotifyID   string // maps to DB column "channel_id"
	Enabled    bool
	CreatedAt  string
	LastRunAt  string // maps to DB column "last_run"
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
		cron:     cron.New(cron.WithSeconds()),
		entries:  make(map[string]cron.EntryID),
	}
}

// SetNotifier sets the channel that receives forwarded replies.
// Call this after construction if the notifier wasn't available at init time.
func (s *SchedulerChannel) SetNotifier(ch channel.Channel) {
	s.notifier = ch
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
	origID := msg.ChannelID
	s.rewriteTarget(&msg)
	if s.notifier == nil {
		return fmt.Errorf("scheduler: no notifier configured")
	}
	err := s.notifier.Send(ctx, msg)
	// Clean up the active target after the reply is forwarded.
	s.activeTargets.Delete(origID)
	return err
}

// SendStreaming delegates streaming to the notifier.
func (s *SchedulerChannel) SendStreaming(ctx context.Context, target channel.MessageTarget) (channel.StreamUpdater, error) {
	s.rewriteTargetMsg(&target)
	if s.notifier == nil {
		return nil, fmt.Errorf("scheduler: no notifier configured")
	}
	return s.notifier.SendStreaming(ctx, target)
}

// Stop halts the cron scheduler and waits for running jobs to finish.
func (s *SchedulerChannel) Stop(_ context.Context) error {
	ctx := s.cron.Stop()
	<-ctx.Done()
	slog.Info("scheduler stopped")
	return nil
}

// rewriteTarget looks up the original taskID in activeTargets and rewrites
// the message's Channel/ChannelID to the real notification target.
func (s *SchedulerChannel) rewriteTarget(msg *channel.OutboundMessage) {
	if msg.Channel != "scheduler" {
		return
	}
	v := s.lookupTarget(msg.ChannelID)
	if v == "" {
		return
	}
	msg.Channel, msg.ChannelID = splitTarget(v)
}

func (s *SchedulerChannel) rewriteTargetMsg(target *channel.MessageTarget) {
	if target.Channel != "scheduler" {
		return
	}
	v := s.lookupTarget(target.ChannelID)
	if v == "" {
		return
	}
	target.Channel, target.ChannelID = splitTarget(v)
}

func (s *SchedulerChannel) lookupTarget(taskID string) string {
	v, ok := s.activeTargets.Load(taskID)
	if !ok {
		return ""
	}
	return v.(string)
}

// splitTarget splits "notifyTo:notifyID" into its two parts.
func splitTarget(s string) (ch, id string) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

// loadEnabledTasks reads all enabled tasks from the database.
// Uses column names from 001_init.sql: channel, channel_id, last_run.
func (s *SchedulerChannel) loadEnabledTasks(ctx context.Context) ([]ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, prompt, cron_expr, channel, channel_id, enabled, created_at,
		        COALESCE(last_run, ''), COALESCE(last_status, '')
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

// setLastRun updates the task's last_run timestamp.
func (s *SchedulerChannel) setLastRun(taskID string) {
	_, err := s.db.Exec(`UPDATE scheduled_tasks SET last_run = datetime('now'), last_status = 'running' WHERE id = ?`, taskID)
	if err != nil {
		slog.Warn("scheduler: failed to update last_run", "task", taskID, "err", err)
	}
}

// taskName returns a short name for the task derived from its prompt.
func taskName(prompt string) string {
	if len(prompt) > 50 {
		return prompt[:47] + "..."
	}
	return prompt
}

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
		`INSERT INTO scheduled_tasks (id, name, prompt, cron_expr, channel, channel_id) VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, taskName(prompt), t.Prompt, t.CronExpr, t.NotifyTo, t.NotifyID)
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
		`SELECT id, prompt, cron_expr, channel, channel_id, enabled, created_at,
		        COALESCE(last_run, ''), COALESCE(last_status, '')
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
		`SELECT id, prompt, cron_expr, channel, channel_id, enabled, created_at,
		        COALESCE(last_run, ''), COALESCE(last_status, '')
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
