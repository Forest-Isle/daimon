package gateway

import (
	"context"
	"log/slog"

	"github.com/punkopunko/ironclaw/internal/channel"
)

// Router maps inbound messages to sessions.
// Currently simple 1:1 mapping (channel+channelID → session).
// Future: multi-agent routing, command parsing, etc.
type Router struct {
	handler channel.InboundHandler
}

func NewRouter(handler channel.InboundHandler) *Router {
	return &Router{handler: handler}
}

func (r *Router) Route(ctx context.Context, msg channel.InboundMessage) {
	slog.Debug("routing message", "channel", msg.Channel, "channel_id", msg.ChannelID)
	r.handler(ctx, msg)
}
