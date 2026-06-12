package agent

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/session"
)

// LinearLoop implements a standard ReAct loop: LLM → tools → LLM → ...
// with parallel dispatch of independent tool_use blocks.
type LinearLoop struct{}

// Execute runs the agent loop for a single inbound message.
func (LinearLoop) Execute(ctx context.Context, a *Agent, ch channel.Channel, msg channel.InboundMessage, sess *session.Session, frame *PromptFrame) error {
	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}
	if frame == nil {
		frame = a.buildPromptFrame(ctx, msg.Text)
	}
	maxIter := a.deps.Core.Cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}

	for iteration := 0; iteration < maxIter; iteration++ {
		slog.Info("linear loop iteration", "iteration", iteration, "session", sess.ID)

		systemPrompt := a.renderPromptFrameForIteration(ctx, frame, sess, iteration)
		updater, toolCalls, iterErr := loopIteration(ctx, a, ch, sess, target, systemPrompt, iteration, maxIter)
		if iterErr != nil {
			return iterErr
		}

		if len(toolCalls) == 0 {
			return nil
		}

		a.dispatchToolsParallel(ctx, ch, sess, target, iteration, toolCalls, computeBudgetPressure(iteration, maxIter))

		_ = updater
	}

	return nil
}
