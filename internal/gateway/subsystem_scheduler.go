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
