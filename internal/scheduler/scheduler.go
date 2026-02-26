package scheduler

import (
	"context"
	"log/slog"

	"github.com/punkopunko/ironclaw/internal/store"
	"github.com/robfig/cron/v3"
)

// Scheduler manages periodic tasks using cron expressions.
type Scheduler struct {
	db   *store.DB
	cron *cron.Cron
}

func New(db *store.DB) *Scheduler {
	return &Scheduler{
		db:   db,
		cron: cron.New(cron.WithSeconds()),
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	// Load tasks from DB
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, cron_expr, prompt, channel, channel_id FROM scheduled_tasks WHERE enabled = 1`)
	if err != nil {
		slog.Error("failed to load scheduled tasks", "err", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Name, &t.CronExpr, &t.Prompt, &t.Channel, &t.ChannelID); err != nil {
			slog.Error("failed to scan task", "err", err)
			continue
		}

		task := t // capture
		_, err := s.cron.AddFunc(task.CronExpr, func() {
			slog.Info("scheduled task triggered", "name", task.Name, "id", task.ID)
			// TODO: inject message into gateway for processing
		})
		if err != nil {
			slog.Error("failed to schedule task", "name", t.Name, "err", err)
			continue
		}

		slog.Info("task scheduled", "name", t.Name, "cron", t.CronExpr)
	}

	s.cron.Start()
}

func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
	slog.Info("scheduler stopped")
}
