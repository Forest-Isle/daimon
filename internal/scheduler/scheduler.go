package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/punkopunko/ironclaw/internal/store"
	"github.com/robfig/cron/v3"
)

// TaskHandler is called when a scheduled task fires.
type TaskHandler func(ctx context.Context, task Task)

// Scheduler manages periodic tasks using cron expressions,
// with database polling to pick up dynamically added tasks.
type Scheduler struct {
	db           *store.DB
	cron         *cron.Cron
	handler      TaskHandler
	pollInterval time.Duration
	mu           sync.Mutex
	entries      map[string]cron.EntryID // taskID -> cronEntryID
	cancel       context.CancelFunc
}

func New(db *store.DB, pollInterval time.Duration) *Scheduler {
	return &Scheduler{
		db:           db,
		cron:         cron.New(cron.WithSeconds()),
		pollInterval: pollInterval,
		entries:      make(map[string]cron.EntryID),
	}
}

// SetHandler sets the callback invoked when a task triggers.
// This allows breaking circular dependencies (gateway ↔ scheduler).
func (s *Scheduler) SetHandler(h TaskHandler) {
	s.handler = h
}

// Start performs an initial sync, starts the cron runner,
// and launches a background goroutine that polls for DB changes.
func (s *Scheduler) Start(ctx context.Context) {
	pollCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	s.syncTasks(pollCtx)
	s.cron.Start()

	go s.pollLoop(pollCtx)
}

func (s *Scheduler) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.syncTasks(ctx)
		}
	}
}

// Stop cancels the poll goroutine and stops the cron runner.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	ctx := s.cron.Stop()
	<-ctx.Done()
	slog.Info("scheduler stopped")
}

// syncTasks queries the database for enabled tasks, registers new ones,
// and removes tasks that have been disabled or deleted.
func (s *Scheduler) syncTasks(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, cron_expr, prompt, channel, channel_id FROM scheduled_tasks WHERE enabled = 1`)
	if err != nil {
		slog.Error("failed to load scheduled tasks", "err", err)
		return
	}
	defer rows.Close()

	activeIDs := make(map[string]struct{})

	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Name, &t.CronExpr, &t.Prompt, &t.Channel, &t.ChannelID); err != nil {
			slog.Error("failed to scan task", "err", err)
			continue
		}
		activeIDs[t.ID] = struct{}{}
		s.registerTask(ctx, t)
	}

	// Remove tasks no longer in the active set
	s.mu.Lock()
	for id, entryID := range s.entries {
		if _, ok := activeIDs[id]; !ok {
			s.cron.Remove(entryID)
			delete(s.entries, id)
			slog.Info("task removed", "id", id)
		}
	}
	s.mu.Unlock()
}

// registerTask idempotently registers a single task into cron.
func (s *Scheduler) registerTask(ctx context.Context, task Task) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.entries[task.ID]; exists {
		return // already registered
	}

	t := task // capture for closure
	entryID, err := s.cron.AddFunc(t.CronExpr, func() {
		slog.Info("scheduled task triggered", "name", t.Name, "id", t.ID)
		if s.handler != nil {
			s.handler(context.Background(), t)
		}
		// Update last_run
		if _, err := s.db.Exec(
			`UPDATE scheduled_tasks SET last_run = ? WHERE id = ?`,
			time.Now(), t.ID,
		); err != nil {
			slog.Error("failed to update last_run", "task", t.ID, "err", err)
		}
	})
	if err != nil {
		slog.Error("failed to schedule task", "name", task.Name, "err", err)
		return
	}

	s.entries[task.ID] = entryID
	slog.Info("task scheduled", "name", task.Name, "cron", task.CronExpr)
}
