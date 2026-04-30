package gateway

import (
	"context"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/channel"
)

func (gw *Gateway) initGraphEngine() error {
	eventStore := agent.NewSQLiteExecutionEventStore(gw.db)
	graph := agent.BuildDefaultGraph()
	gw.graphEngine = agent.NewGraphEngine(graph, eventStore)
	gw.heartbeat = agent.NewHeartbeatScheduler(agent.HeartbeatConfig{
		Interval: 5 * time.Minute,
		Enabled:  true,
	})
	return nil
}

func (gw *Gateway) handleGraphMessage(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) error {
	if gw.graphEngine == nil {
		return gw.runtime.HandleMessage(ctx, ch, msg)
	}

	sess, err := gw.sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		return err
	}

	initialState := agent.GraphState{
		SessionID:     sess.ID,
		CurrentNode:   agent.NodePerceive,
		ExecutionPath: agent.PathDeep,
	}

	finalState, err := gw.graphEngine.Run(ctx, sess.ID, initialState)
	if err != nil {
		return err
	}

	if len(finalState.Events) == 0 {
		return gw.runtime.HandleMessage(ctx, ch, msg)
	}

	lastEvent := finalState.Events[len(finalState.Events)-1]
	return ch.Send(ctx, channel.OutboundMessage{
		Channel:   msg.Channel,
		ChannelID: msg.ChannelID,
		Text:      lastEvent.OutputSnapshot,
	})
}
