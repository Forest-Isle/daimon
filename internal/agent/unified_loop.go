package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

// UnifiedLoop implements a linear LLM -> tools -> LLM -> tools loop
// with concurrent execution of independent tool_use blocks.
type UnifiedLoop struct{}

// Execute runs the unified agent loop for a single inbound message.
func (UnifiedLoop) Execute(ctx context.Context, a *Agent, ch channel.Channel, msg channel.InboundMessage, sess *session.Session) error {
	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}
	systemPrompt := a.buildSystemPrompt(ctx, msg.Text)
	maxIter := a.deps.Core.Cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}

	startTime := time.Now()

	for iteration := 0; iteration < maxIter; iteration++ {
		slog.Info("unified loop iteration", "iteration", iteration, "session", sess.ID)

		// Reset speculative executor
		if a.deps.MultiAgent.Speculative != nil {
			a.deps.MultiAgent.Speculative.Reset()
		}

		// Budget pressure signal
		budgetWarning := computeBudgetPressure(iteration, maxIter, sess, systemPrompt, a.deps.Memory.ContextMgr)

		// Push metrics
		util := a.deps.Memory.ContextMgr.Utilization(sess, systemPrompt)
		inTok, outTok := int64(0), int64(0)
		cacheCreate, cacheRead := int64(0), int64(0)
		switch p := a.deps.Core.Provider.(type) {
		case *ClaudeProvider:
			cacheCreate, cacheRead = p.GetCacheStats()
			inTok, outTok = p.GetTokenStats()
		case *OpenAIProvider:
			cacheCreate, cacheRead = p.GetCacheStats()
			inTok, outTok = p.GetTokenStats()
		}
		a.eventBus.Publish(MetricsTick{
			SessionID: sess.ID, Iteration: iteration, MaxIter: maxIter,
			Utilization: util, InputTokens: inTok, OutputTokens: outTok,
			CacheCreate: cacheCreate, CacheRead: cacheRead,
			Model: a.deps.Core.LLMCfg.Model, Provider: a.deps.Core.LLMCfg.Provider,
		})

		// Each iteration creates a fresh streaming message
		updater, streamErr := ch.SendStreaming(ctx, target)
		if streamErr != nil {
			// Fallback to non-streaming
			return unifiedNonStreaming(ctx, a, ch, sess, target, systemPrompt, maxIter)
		}

		req := CompletionRequest{
			Model:     a.deps.Core.LLMCfg.Model,
			System:    systemPrompt,
			Messages:  BuildMessages(sess),
			Tools:     a.buildToolDefs(),
			MaxTokens: a.deps.Core.LLMCfg.MaxTokens,
		}

		stream, streamErr := a.deps.Core.Provider.Stream(ctx, req)
		if streamErr != nil && isContextLengthError(streamErr) {
			_ = updater.Finish("")
			if compErr := a.deps.Memory.ContextMgr.ReactiveCompress(ctx, sess, systemPrompt); compErr != nil {
				slog.Warn("reactive compress failed", "err", compErr)
			} else {
				a.eventBus.Publish(ContextCompressed{SessionID: sess.ID, Reason: "413_retry", LayersRun: 3})
				req.Messages = BuildMessages(sess)
				stream, streamErr = a.deps.Core.Provider.Stream(ctx, req)
			}
		}
		if streamErr != nil {
			_ = updater.Finish("Error: " + streamErr.Error())
			notifyEpisodeComplete(a, sess, iteration, false, startTime)
			return fmt.Errorf("llm stream: %w", streamErr)
		}

		var fullText string
		var toolCalls []ToolUseBlock
		var stopReason StopReason

		for {
			delta, deltaErr := stream.Next()
			if deltaErr != nil {
				stream.Close()
				_ = updater.Finish("Error: " + deltaErr.Error())
				notifyEpisodeComplete(a, sess, iteration, false, startTime)
				return fmt.Errorf("stream next: %w", deltaErr)
			}

			if delta.Text != "" {
				fullText += delta.Text
				_ = updater.Update(fullText)
			}
			if delta.ToolCall != nil {
				toolCalls = append(toolCalls, *delta.ToolCall)
			}
			if delta.Done && len(delta.ToolCalls) > 0 {
				toolCalls = delta.ToolCalls
			}

			// Speculative execution
			if a.deps.MultiAgent.Speculative != nil {
				if ptbSrc, ok := stream.(PendingToolBlockSource); ok {
					for _, ptb := range ptbSrc.PendingToolBlocks() {
						a.deps.MultiAgent.Speculative.TryLaunch(ctx, ptb.ToolUseID, ptb.ToolName, ptb.Input)
					}
				}
			}

			if delta.Done {
				stopReason = delta.StopReason
				break
			}
		}
		stream.Close()

		// Fallback: tool_use without tool calls -> re-request non-streaming
		if stopReason == StopToolUse && len(toolCalls) == 0 {
			resp, completeErr := a.deps.Core.Provider.Complete(ctx, req)
			if completeErr != nil {
				_ = updater.Finish("Error: " + completeErr.Error())
				notifyEpisodeComplete(a, sess, iteration, false, startTime)
				return completeErr
			}
			fullText = resp.Text
			toolCalls = resp.ToolCalls
		}

		// Save assistant message
		if fullText != "" {
			sess.AddMessage(session.Message{
				ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				Role:      "assistant",
				Content:   fullText,
				CreatedAt: time.Now(),
			})
		}

		// Save tool_use messages
		for _, tc := range toolCalls {
			sess.AddMessage(session.Message{
				ID:        tc.ID,
				Role:      "tool_use",
				ToolName:  tc.Name,
				ToolInput: tc.Input,
				CreatedAt: time.Now(),
			})
		}

		// If no tool calls, we're done
		if len(toolCalls) == 0 {
			_ = updater.Finish(fullText)
			notifyEpisodeComplete(a, sess, iteration, true, startTime)
			return nil
		}

		// Finalize streaming message with tool-call status
		statusText := "Calling tools..."
		if fullText != "" {
			statusText = fullText + "\n\nCalling tools..."
		}
		_ = updater.Finish(statusText)

		// Execute tools in parallel
		a.dispatchToolsParallel(ctx, ch, sess, target, toolCalls, budgetWarning)

		// Notify evolution engine of loop completion (per-iteration reflection)
		notifyLoopIteration(a, sess, toolCalls, iteration)
	}

	notifyEpisodeComplete(a, sess, maxIter, true, startTime)
	return nil
}

// unifiedNonStreaming handles the non-streaming fallback path.
func unifiedNonStreaming(ctx context.Context, a *Agent, ch channel.Channel, sess *session.Session, target channel.MessageTarget, systemPrompt string, maxIter int) error {
	startTime := time.Now()
	for iteration := 0; iteration < maxIter; iteration++ {
		budgetWarning := computeBudgetPressure(iteration, maxIter, sess, systemPrompt, a.deps.Memory.ContextMgr)

		req := CompletionRequest{
			Model:     a.deps.Core.LLMCfg.Model,
			System:    systemPrompt,
			Messages:  BuildMessages(sess),
			Tools:     a.buildToolDefs(),
			MaxTokens: a.deps.Core.LLMCfg.MaxTokens,
		}

		resp, err := a.deps.Core.Provider.Complete(ctx, req)
		if err != nil && isContextLengthError(err) {
			if compErr := a.deps.Memory.ContextMgr.ReactiveCompress(ctx, sess, systemPrompt); compErr != nil {
				slog.Warn("reactive compress failed", "err", compErr)
			} else {
				a.eventBus.Publish(ContextCompressed{SessionID: sess.ID, Reason: "413_retry", LayersRun: 3})
				req.Messages = BuildMessages(sess)
				resp, err = a.deps.Core.Provider.Complete(ctx, req)
			}
		}
		if err != nil {
			notifyEpisodeComplete(a, sess, iteration, false, startTime)
			return err
		}

		if resp.Text != "" {
			sess.AddMessage(session.Message{
				ID: fmt.Sprintf("msg_%d", time.Now().UnixNano()), Role: "assistant", Content: resp.Text, CreatedAt: time.Now(),
			})
		}
		for _, tc := range resp.ToolCalls {
			sess.AddMessage(session.Message{
				ID: tc.ID, Role: "tool_use", ToolName: tc.Name, ToolInput: tc.Input, CreatedAt: time.Now(),
			})
		}

		if len(resp.ToolCalls) == 0 {
			if sendErr := ch.Send(ctx, channel.OutboundMessage{
				Channel: target.Channel, ChannelID: target.ChannelID, Text: resp.Text,
			}); sendErr != nil {
				slog.Warn("failed to send message", "err", sendErr)
			}
			notifyEpisodeComplete(a, sess, iteration, true, startTime)
			return nil
		}

		// Execute tools in parallel
		a.dispatchToolsParallel(ctx, ch, sess, target, resp.ToolCalls, budgetWarning)

		// Notify evolution engine of loop completion (per-iteration reflection)
		notifyLoopIteration(a, sess, resp.ToolCalls, iteration)
	}
	notifyEpisodeComplete(a, sess, maxIter, true, startTime)
	return nil
}
