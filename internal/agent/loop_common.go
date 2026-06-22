package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Forest-Isle/daimon/internal/channel"
	ierrors "github.com/Forest-Isle/daimon/internal/errors"
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/session"
)

// loopIteration performs one iteration of the agent loop: stream LLM response,
// collect tool calls, save messages, and finalize
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
) (channel.StreamUpdater, []mind.ToolUseBlock, error) {
	// Push metrics
	util := a.deps.Memory.ContextMgr.Utilization(sess, systemPrompt)
	inTok, outTok := int64(0), int64(0)
	cacheCreate, cacheRead := int64(0), int64(0)
	switch p := a.deps.Core.Provider.(type) {
	case *mind.ClaudeProvider:
		cacheCreate, cacheRead = p.GetCacheStats()
		inTok, outTok = p.GetTokenStats()
	case *mind.OpenAIProvider:
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

	req := mind.CompletionRequest{
		Model:          a.deps.Core.LLMCfg.Model,
		System:         systemPrompt,
		Messages:       BuildMessages(sess),
		Tools:          a.buildToolDefs(),
		MaxTokens:      a.deps.Core.LLMCfg.MaxTokens,
		ThinkingBudget: a.deps.Core.LLMCfg.ThinkingBudget,
	}

	modelStart := time.Now()
	a.eventBus.Publish(ModelCallStarted{
		SessionID:    sess.ID,
		Iteration:    iteration,
		Model:        req.Model,
		Provider:     a.deps.Core.LLMCfg.Provider,
		MessageCount: len(req.Messages),
		ToolCount:    len(req.Tools),
		SystemChars:  len(req.System),
		Streaming:    true,
	})
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
		slog.Error("llm stream error", "err", streamErr)
		_ = updater.Finish("Error: " + streamErr.Error())
		durationMs := time.Since(modelStart).Milliseconds()
		a.eventBus.Publish(ModelCallEnded{
			SessionID:  sess.ID,
			Iteration:  iteration,
			Model:      req.Model,
			Provider:   a.deps.Core.LLMCfg.Provider,
			Streaming:  true,
			Succeeded:  false,
			DurationMs: durationMs,
			Error:      streamErr.Error(),
		})
		publishProviderExchange(a, sess.ID, iteration, req, "", nil, "", durationMs)
		return updater, nil, streamErr
	}

	var fullText string
	var thinking string
	var signature string
	var toolCalls []mind.ToolUseBlock
	var stopReason mind.StopReason

	for {
		delta, deltaErr := stream.Next()
		if deltaErr != nil {
			stream.Close()
			slog.Error("stream delta error", "err", deltaErr)
			_ = updater.Finish("Error: " + deltaErr.Error())
			durationMs := time.Since(modelStart).Milliseconds()
			a.eventBus.Publish(ModelCallEnded{
				SessionID:  sess.ID,
				Iteration:  iteration,
				Model:      req.Model,
				Provider:   a.deps.Core.LLMCfg.Provider,
				Streaming:  true,
				Succeeded:  false,
				DurationMs: durationMs,
				Error:      deltaErr.Error(),
			})
			publishProviderExchange(a, sess.ID, iteration, req, fullText, toolCalls, "", durationMs)
			return updater, nil, deltaErr
		}

		if delta.Text != "" {
			fullText += delta.Text
			_ = updater.Update(fullText)
		}
		if delta.Thinking != "" {
			thinking += delta.Thinking
		}
		if delta.Signature != "" {
			signature = delta.Signature
		}
		if delta.ToolCall != nil {
			toolCalls = append(toolCalls, *delta.ToolCall)
		}
		if delta.Done && len(delta.ToolCalls) > 0 {
			toolCalls = delta.ToolCalls
		}

		if delta.Done {
			stopReason = delta.StopReason
			break
		}
	}
	stream.Close()
	durationMs := time.Since(modelStart).Milliseconds()
	a.eventBus.Publish(ModelCallEnded{
		SessionID:  sess.ID,
		Iteration:  iteration,
		Model:      req.Model,
		Provider:   a.deps.Core.LLMCfg.Provider,
		Streaming:  true,
		Succeeded:  true,
		DurationMs: durationMs,
		StopReason: string(stopReason),
	})
	publishProviderExchange(a, sess.ID, iteration, req, fullText, toolCalls, stopReason, durationMs)

	// Fallback: tool_use without tool calls -> re-request non-streaming
	if stopReason == mind.StopToolUse && len(toolCalls) == 0 {
		fallbackStart := time.Now()
		a.eventBus.Publish(ModelCallStarted{
			SessionID:    sess.ID,
			Iteration:    iteration,
			Model:        req.Model,
			Provider:     a.deps.Core.LLMCfg.Provider,
			MessageCount: len(req.Messages),
			ToolCount:    len(req.Tools),
			SystemChars:  len(req.System),
			Streaming:    false,
		})
		resp, completeErr := a.deps.Core.Provider.Complete(ctx, req)
		if completeErr != nil {
			slog.Error("non-streaming completion error", "err", completeErr)
			_ = updater.Finish("Error: " + completeErr.Error())
			durationMs := time.Since(fallbackStart).Milliseconds()
			a.eventBus.Publish(ModelCallEnded{
				SessionID:  sess.ID,
				Iteration:  iteration,
				Model:      req.Model,
				Provider:   a.deps.Core.LLMCfg.Provider,
				Streaming:  false,
				Succeeded:  false,
				DurationMs: durationMs,
				Error:      completeErr.Error(),
			})
			publishProviderExchange(a, sess.ID, iteration, req, "", nil, "", durationMs)
			return updater, nil, completeErr
		}
		durationMs := time.Since(fallbackStart).Milliseconds()
		a.eventBus.Publish(ModelCallEnded{
			SessionID:  sess.ID,
			Iteration:  iteration,
			Model:      req.Model,
			Provider:   a.deps.Core.LLMCfg.Provider,
			Streaming:  false,
			Succeeded:  true,
			DurationMs: durationMs,
			StopReason: string(resp.StopReason),
		})
		publishProviderExchange(a, sess.ID, iteration, req, resp.Text, resp.ToolCalls, resp.StopReason, durationMs)
		fullText = resp.Text
		thinking = resp.Thinking
		signature = resp.Signature
		toolCalls = resp.ToolCalls
	}

	// Save assistant message. A thinking block must travel with the assistant
	// turn it belongs to, so persist the message when there is text OR a
	// thinking block to carry.
	if fullText != "" || thinking != "" {
		sess.AddMessage(session.Message{
			ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			Role:      "assistant",
			Content:   fullText,
			Thinking:  thinking,
			Signature: signature,
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
		finalReply := appendStopNotice(fullText, stopReason)
		_ = updater.Finish(finalReply)
		a.eventBus.Publish(TurnClosed{SessionID: sess.ID, FinalReply: finalReply})
		return updater, nil, nil
	}

	// Finalize streaming message with tool-call status. Show the actual tools
	// being invoked (name + a short arg hint) rather than a generic message,
	// so the user can see what the agent is doing.
	statusText := formatToolCallStatus(toolCalls)
	if fullText != "" {
		statusText = fullText + "\n\n" + statusText
	}
	_ = updater.Finish(statusText)

	return updater, toolCalls, nil
}

// formatToolCallStatus renders a compact, human-readable summary of the tools
// the agent is about to invoke, one per line, e.g. "⚙ bash: go test ./...".
func formatToolCallStatus(calls []mind.ToolUseBlock) string {
	if len(calls) == 0 {
		return "Calling tools…"
	}
	var b strings.Builder
	for i, tc := range calls {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("⚙ ")
		b.WriteString(tc.Name)
		if hint := toolInputHint(tc.Input); hint != "" {
			b.WriteString(": ")
			b.WriteString(hint)
		}
	}
	return b.String()
}

// toolInputHint extracts a short hint from a tool call's raw JSON input —
// the command for bash, the path for file ops, etc. Returns "" when no
// recognizable field is present.
func toolInputHint(input string) string {
	var m map[string]any
	if json.Unmarshal([]byte(input), &m) != nil {
		return ""
	}
	for _, key := range []string{"command", "cmd", "path", "file_path", "query", "url", "pattern"} {
		if v, ok := m[key].(string); ok && v != "" {
			v = strings.TrimSpace(strings.ReplaceAll(v, "\n", " "))
			if r := []rune(v); len(r) > 80 {
				return string(r[:80]) + "…"
			}
			return v
		}
	}
	return ""
}

// noticeMaxTokens / noticeAbnormal are appended to a response the model did not
// finish cleanly, so a partial or filtered answer is never presented as
// complete.
const (
	noticeMaxTokens = "[response truncated: reached max output tokens]"
	noticeAbnormal  = "[response incomplete: the model stopped unexpectedly]"
)

// appendStopNotice flags responses that ended on a non-success stop reason
// (max tokens, content filtering, or an unrecognized finish reason). For a
// clean end_turn or tool_use stop the text is returned unchanged.
func appendStopNotice(text string, stopReason mind.StopReason) string {
	var notice string
	switch stopReason {
	case mind.StopMaxToken:
		notice = noticeMaxTokens
	case mind.StopAbnormal:
		notice = noticeAbnormal
	default:
		return text
	}
	if text == "" {
		return notice
	}
	return text + "\n\n" + notice
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

func publishProviderExchange(
	a *Agent,
	sessionID string,
	iteration int,
	req mind.CompletionRequest,
	responseText string,
	toolCalls []mind.ToolUseBlock,
	stopReason mind.StopReason,
	durationMs int64,
) {
	if a == nil || a.eventBus == nil {
		return
	}
	a.eventBus.Publish(ProviderExchange{
		SessionID:      sessionID,
		Iteration:      iteration,
		Model:          req.Model,
		Provider:       a.deps.Core.LLMCfg.Provider,
		SystemPrompt:   req.System,
		MessagesJSON:   replayMessagesJSON(req.Messages),
		ResponseText:   responseText,
		ToolCallsJSON:  replayToolCallsJSON(toolCalls),
		StopReason:     string(stopReason),
		DurationMs:     durationMs,
		ToolsJSON:      replayToolsJSON(req.Tools),
		ToolChoice:     req.ToolChoice,
		ThinkingBudget: req.ThinkingBudget,
	})
}

// replayToolsJSON marshals the tool affordances offered in the request so a
// re-run can present the same tools. Nil/empty tools omit the field entirely.
func replayToolsJSON(tools []mind.ToolDefinition) json.RawMessage {
	if len(tools) == 0 {
		return nil
	}
	return replayMarshalJSON(tools)
}

func replayMessagesJSON(messages []mind.CompletionMessage) json.RawMessage {
	if messages == nil {
		messages = []mind.CompletionMessage{}
	}
	return replayMarshalJSON(messages)
}

func replayToolCallsJSON(toolCalls []mind.ToolUseBlock) json.RawMessage {
	if toolCalls == nil {
		toolCalls = []mind.ToolUseBlock{}
	}
	return replayMarshalJSON(toolCalls)
}

func replayMarshalJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(data)
}

func replayRawJSONString(raw string) json.RawMessage {
	data := []byte(strings.TrimSpace(raw))
	if len(data) > 0 && json.Valid(data) {
		return json.RawMessage(append([]byte(nil), data...))
	}
	return replayMarshalJSON(raw)
}
