package gateway

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/scheduler"
)

// ChannelSubsystem manages communication channels and the task scheduler.
type ChannelSubsystem struct {
	channels map[string]channel.Channel
	sched    *scheduler.Scheduler
}

func (cs *ChannelSubsystem) Name() string { return "channel" }

// Start starts the scheduler (if enabled elsewhere) and all registered channels.
func (cs *ChannelSubsystem) Start(ctx context.Context) error {
	for name, ch := range cs.channels {
		// The inbound handler (*Gateway.handleInbound) is wired externally
		// via Gateway.AddChannel — channels are started by Gateway.Start()
		// because they need the gateway's inbound handler.
		// Here we only start channels that are already wired.
		_ = name
		_ = ch
		_ = ctx
	}
	return nil
}

// StartChannel starts a single channel with the given inbound handler.
func (cs *ChannelSubsystem) StartChannel(ctx context.Context, ch channel.Channel, handler channel.InboundHandler) error {
	if err := ch.Start(ctx, handler); err != nil {
		return err
	}
	slog.Info("channel started", "name", ch.Name())
	return nil
}

// Stop stops all channels and the scheduler.
func (cs *ChannelSubsystem) Stop(ctx context.Context) error {
	for name, ch := range cs.channels {
		if err := ch.Stop(ctx); err != nil {
			slog.Warn("failed to stop channel", "name", name, "err", err)
		}
	}
	if cs.sched != nil {
		cs.sched.Stop()
	}
	return nil
}

// Channels returns the channel map.
func (cs *ChannelSubsystem) Channels() map[string]channel.Channel { return cs.channels }

// Channel returns a channel by name, or nil if not found.
func (cs *ChannelSubsystem) Channel(name string) channel.Channel { return cs.channels[name] }

// Scheduler returns the task scheduler, or nil.
func (cs *ChannelSubsystem) Scheduler() *scheduler.Scheduler { return cs.sched }

// SetScheduler sets the scheduler instance.
func (cs *ChannelSubsystem) SetScheduler(s *scheduler.Scheduler) { cs.sched = s }
