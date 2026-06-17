package gateway

import (
	"context"
	"github.com/Forest-Isle/daimon/internal/channel"
)

type CommandSubsystem struct {
	Table commandTable
}

func (cs *CommandSubsystem) Name() string                  { return "command" }
func (cs *CommandSubsystem) Start(_ context.Context) error { return nil }
func (cs *CommandSubsystem) Stop(_ context.Context) error  { return nil }

func InitCommands(gwRef *Gateway) *CommandSubsystem {
	cs := &CommandSubsystem{}
	cs.Table = commandTable{
		"/attention": {gwRef.handleAttention, false},
		"/brief":     {gwRef.handleBrief, false},
		"/feature":   {gwRef.handleFeature, false},
		"/config":    {gwRef.handleConfig, true},
		"/compact":   {gwRef.handleCompact, true},
		"/episodes":  {gwRef.handleEpisodes, false},
		"/memory":    {gwRef.handleMemory, false},
		"/model":     {gwRef.handleModel, false},
		"/holds":     {gwRef.handleHolds, false},
		"/new":       {gwRef.handleReset, true},
		"/proposals": {gwRef.handleProposals, false},
		"/replay":    {gwRef.handleReplay, false},
		"/reset":     {gwRef.handleReset, true},
		"/resume":    {gwRef.handleResume, false},
		"/start":     {gwRef.handleReset, true},
		"/skill":     {gwRef.handleSkills, false},
		"/skills":    {gwRef.handleSkills, false},
		"/sleep":     {gwRef.handleSleep, false},
		"/schedule":  {gwRef.handleSchedule, false},
		"/selfops":   {gwRef.handleSelfops, false},
		"/tasks":     {gwRef.handleTasks, true},
		"/team":      {gwRef.handleTeam, false},
		"/throttle":  {gwRef.handleThrottle, false},
		"/trust":     {gwRef.handleTrust, false},
	}
	return cs
}

func (cs *CommandSubsystem) Dispatch(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) (string, bool) {
	if cs.Table == nil {
		return "", false
	}
	return cs.Table.dispatch(ctx, ch, msg)
}
