package gateway

import (
	"context"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/channel"
)

func (gw *Gateway) initGraphEngine() error {
	eventStore := agent.NewSQLiteExecutionEventStore(gw.db)
	gw.graphEventStore = eventStore
	gw.heartbeat = agent.NewHeartbeatScheduler(agent.HeartbeatConfig{
		Interval: 5 * time.Minute,
		Enabled:  true,
	})
	return nil
}

func (gw *Gateway) handleGraphMessage(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) error {
	if gw.graphEventStore == nil || gw.cognitiveLoop == nil {
		return gw.agent.HandleMessage(ctx, ch, msg)
	}

	sess, err := gw.sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		return err
	}

	deps := gw.cognitiveLoop.BuildNodeDeps(ch)
	graph := agent.BuildGraphWithDeps(deps, msg.Text, msg.UserID)
	engine := agent.NewGraphEngine(graph, gw.graphEventStore)

	initialState := agent.GraphState{
		SessionID:     sess.ID,
		CurrentNode:   agent.NodePerceive,
		ExecutionPath: agent.PathDeep,
	}

	finalState, err := engine.Run(ctx, sess.ID, initialState)
	if err != nil {
		return err
	}

	if len(finalState.Events) == 0 {
		return gw.agent.HandleMessage(ctx, ch, msg)
	}

	lastEvent := finalState.Events[len(finalState.Events)-1]
	if lastEvent.OutputSnapshot == "" || lastEvent.OutputSnapshot == "{}" {
		return gw.agent.HandleMessage(ctx, ch, msg)
	}

	return ch.Send(ctx, channel.OutboundMessage{
		Channel:   msg.Channel,
		ChannelID: msg.ChannelID,
		Text:      lastEvent.OutputSnapshot,
	})
}
