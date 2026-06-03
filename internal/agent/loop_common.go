package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	ierrors "github.com/Forest-Isle/IronClaw/internal/errors"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

// loopIteration performs one iteration of the agent loop: stream LLM response,
// collect tool calls (with speculative execution), save messages, and finalize
// the streaming updater. Returns the updater, tool calls collected, and any error.
func loopIteration(
	ctx context.Context,
	a *Agent,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	systemPrompt string,
	iteration int,
	maxIter int,
) (channel.StreamUpdater, []ToolUseBlock, error) {
	// Reset speculative executor
	if a.deps.MultiAgent.Speculative != nil {
		a.deps.MultiAgent.Speculative.Reset()
	}

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

	updater, streamErr := ch.SendStreaming(ctx, target)
	if streamErr != nil {
		return nil, nil, fmt.Errorf("send streaming: %w", streamErr)
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
		return updater, nil, fmt.Errorf("llm stream: %w", streamErr)
	}

	var fullText string
	var toolCalls []ToolUseBlock
	var stopReason StopReason

	for {
		delta, deltaErr := stream.Next()
		if deltaErr != nil {
			stream.Close()
			_ = updater.Finish("Error: " + deltaErr.Error())
			return updater, nil, fmt.Errorf("stream next: %w", deltaErr)
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
			return updater, nil, completeErr
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
		return updater, nil, nil
	}

	// Finalize streaming message with tool-call status
	statusText := "Calling tools..."
	if fullText != "" {
		statusText = fullText + "\n\nCalling tools..."
	}
	_ = updater.Finish(statusText)

	return updater, toolCalls, nil
}

// loopIterationNonStreaming performs one iteration in non-streaming mode.
func loopIterationNonStreaming(
	ctx context.Context,
	a *Agent,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	systemPrompt string,
	iteration int,
	maxIter int,
) ([]ToolUseBlock, error) {
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
		return nil, err
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
		return nil, nil
	}

	return resp.ToolCalls, nil
}

// computeBudgetPressure generates a warning string based on iteration pressure.
func computeBudgetPressure(iteration, maxIter int) string {
	var warnings []string
	iterationPct := float64(iteration+1) / float64(maxIter) * 100
	if iterationPct >= 90 {
		warnings = append(warnings, fmt.Sprintf("[!] Critical budget pressure: %.0f%% of iterations used.", iterationPct))
	} else if iterationPct >= 70 {
		warnings = append(warnings, fmt.Sprintf("[!] Budget pressure: %.0f%% of iterations used.", iterationPct))
	}
	if len(warnings) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(warnings, "\n")
}

// isContextLengthError returns true if the error is related to the LLM context
// window being exceeded (e.g., prompt too long, context_length_exceeded).
func isContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	if ierrors.IsKind(err, ierrors.KindContextLength) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context_length_exceeded") ||
		strings.Contains(msg, "prompt is too long") ||
		strings.Contains(msg, "too many tokens") ||
		strings.Contains(msg, "413") ||
		strings.Contains(msg, "request too large") ||
		strings.Contains(msg, "payload too large")
}
