package gateway

import (
	"context"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/channel/scheduler"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/taskruntime"
)

// SchedulerSubsystem wraps the SchedulerChannel for Gateway lifecycle management.
type SchedulerSubsystem struct {
	Channel *scheduler.SchedulerChannel
}

func (ss *SchedulerSubsystem) Name() string                  { return "scheduler" }
func (ss *SchedulerSubsystem) Start(_ context.Context) error { return nil }
func (ss *SchedulerSubsystem) Stop(_ context.Context) error  { return nil }

// InitScheduler creates the SchedulerChannel. notifyCh is the channel that
// receives forwarded replies (typically Telegram).
func InitScheduler(db *store.DB, notifyCh channel.Channel, ledger *taskruntime.Ledger) *SchedulerSubsystem {
	sc := scheduler.New(db, notifyCh, ledger)
	return &SchedulerSubsystem{Channel: sc}
}
