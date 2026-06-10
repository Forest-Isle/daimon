package gateway

import (
	"context"
	"github.com/Forest-Isle/IronClaw/internal/channel"
)

type CommandSubsystem struct {
	Table commandTable
}

func (cs *CommandSubsystem) Name() string                { return "command" }
func (cs *CommandSubsystem) Start(_ context.Context) error { return nil }
func (cs *CommandSubsystem) Stop(_ context.Context) error  { return nil }

func InitCommands(gwRef *Gateway) *CommandSubsystem {
	cs := &CommandSubsystem{}
	cs.Table = commandTable{
		"/feature": {gwRef.handleFeature, false},
		"/config":  {gwRef.handleConfig, true},
		"/compact": {gwRef.handleCompact, true},
		"/model":   {gwRef.handleModel, false},
		"/new":     {gwRef.handleReset, true},
		"/start":    {gwRef.handleReset, true},
		"/schedule": {gwRef.handleSchedule, false},
	}
	return cs
}

func (cs *CommandSubsystem) Dispatch(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) (string, bool) {
	if cs.Table == nil { return "", false }
	return cs.Table.dispatch(ctx, ch, msg)
}
