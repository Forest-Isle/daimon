package agent

import (
	"context"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/session"
)

// LoopStrategy defines how an Agent processes a message through its execution loop.
type LoopStrategy interface {
	// Execute runs the agent loop for a single inbound message.
	Execute(ctx context.Context, a *Agent, ch channel.Channel, msg channel.InboundMessage, sess *session.Session, frame *PromptFrame) error
}
