package scheduler

import (
	"time"
)

// Task represents a scheduled task.
type Task struct {
	ID        string
	Name      string
	CronExpr  string
	Prompt    string
	Channel   string
	ChannelID string
	Enabled   bool
	CreatedAt time.Time
	LastRun   *time.Time
}
