package agent

import (
	"context"
	"fmt"
	"github.com/Forest-Isle/IronClaw/internal/util"
	"log/slog"
	"strings"
	"time"

	ierrors "github.com/Forest-Isle/IronClaw/internal/errors"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/taskledger"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// ApprovalFunc is called when a tool requires user approval.
// It should return true if approved, false if denied.
type ApprovalFunc func(ctx context.Context, ch channel.Channel, target channel.MessageTarget, toolName string, input string) (bool, error)

// Runtime orchestrates the agent loop: context → LLM → tools → reply.
type Runtime struct {
	deps AgentDeps

	// Runtime identity (set externally after construction)
	parentID string
	depth    int
	chainID  string

	// Transient state
	replayID     string
	approvalFunc ApprovalFunc
}

// AgentID returns this runtime's unique agent identifier.
func (r *Runtime) AgentID() string { return r.deps.Core.AgentID }

// ParentID returns the parent agent's identifier.
func (r *Runtime) ParentID() string { return r.parentID }

// SetParentID sets the parent agent's identifier.
func (r *Runtime) SetParentID(id string) { r.parentID = id }

// Depth returns the nesting depth of this runtime.
func (r *Runtime) Depth() int { return r.depth }

// SetDepth sets the nesting depth.
func (r *Runtime) SetDepth(d int) { r.depth = d }

// ChainID returns the invocation chain identifier.
func (r *Runtime) ChainID() string { return r.chainID }

// SetChainID sets the invocation chain identifier.
func (r *Runtime) SetChainID(id string) { r.chainID = id }

// BackgroundManager returns the attached background manager, or nil.
func (r *Runtime) BackgroundManager() *BackgroundManager { return r.deps.MultiAgent.BgManager }

// PromptCache returns the attached prompt cache, or nil.
func (r *Runtime) PromptCache() *PromptCache { return r.deps.MultiAgent.PromptCache }

// AgentMCPManager returns the attached per-agent MCP manager, or nil.
func (r *Runtime) AgentMCPManager() *AgentMCPManager { return r.deps.MultiAgent.AgentMCP }

// Orchestrator returns the attached orchestrator, or nil.
func (r *Runtime) Orchestrator() *AgentOrchestrator { return r.deps.MultiAgent.Orchestrator }

// GetMessages returns a snapshot of the current session's message history.
// Returns nil if no session is active. Used by fork agents to inherit context.
func (r *Runtime) GetMessages(ctx context.Context, channelName, channelID string) []session.Message {
	sess, err := r.deps.Core.Sessions.Get(ctx, channelName, channelID)
	if err != nil {
		return nil
	}
	history := sess.History()
	out := make([]session.Message, len(history))
	copy(out, history)
	return out
}

// GetSystemPrompt builds and returns the current system prompt.
// Used by fork agents to reuse the parent's prompt.
func (r *Runtime) GetSystemPrompt(ctx context.Context, userText string) string {
	return r.buildSystemPrompt(ctx, userText)
}

// SetModel updates the default model used for LLM requests.
func (r *Runtime) SetModel(model string) { r.deps.Core.LLMCfg.Model = model }

// GetTools returns the runtime's tool registry.
func (r *Runtime) GetTools() *tool.Registry { return r.deps.Core.Tools }

func NewRuntime(deps AgentDeps) *Runtime {
	return &Runtime{deps: deps}
}

// SetApprovalFunc sets the callback for tool approval requests.
func (r *Runtime) SetApprovalFunc(fn ApprovalFunc) {
	r.approvalFunc = fn
}

// HandleMessage processes an inbound message through the agent loop.
func (r *Runtime) HandleMessage(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) error {
	// Store this runtime in context so sub-agents can access the parent.
	ctx = RuntimeToContext(ctx, r)

	task := taskledger.Task{
		ID:    fmt.Sprintf("req_%d", time.Now().UnixNano()),
		Kind:  taskledger.TaskKindUserRequest,
		State: taskledger.TaskStateRunning,
		Title: util.TruncateStr(msg.Text, 100),
	}
	if err := r.deps.MultiAgent.TaskLedger.Register(ctx, task); err != nil {
		slog.Warn("runtime: failed to register task", "err", err)
	} else {
		defer func() {
			task.State = taskledger.TaskStateCompleted
			now := time.Now()
			task.CompletedAt = &now
			if err := r.deps.MultiAgent.TaskLedger.Update(ctx, task); err != nil {
				slog.Warn("runtime: failed to complete task", "err", err)
			}
		}()
	}

	sess, err := r.deps.Core.Sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	replaySucceeded := false
	if r.deps.Observability.ReplayRecorder != nil {
		r.replayID = r.deps.Observability.ReplayRecorder.RecordSessionStart(ctx, sess.ID, msg.Channel, "runtime", r.deps.Core.LLMCfg.Model)
		defer func() {
			if r.deps.Observability.ReplayRecorder != nil && r.replayID != "" {
				r.deps.Observability.ReplayRecorder.RecordSessionEnd(ctx, r.replayID, replaySucceeded, len(sess.History()))
				r.replayID = ""
			}
		}()
	}

	runtimeSessionStart := time.Now()
	r.deps.Observability.Emitter.EmitSessionStart(sess.ID, msg.Channel)

	// Add user message to session
	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Role:      "user",
		Content:   msg.Text,
		CreatedAt: time.Now(),
	})

	// Build system prompt, augmented with relevant memories if available
	systemPrompt := r.buildSystemPrompt(ctx, msg.Text)

	// Fire OnUserMessage hooks
	if r.deps.Security.HookMgr != nil && r.deps.Security.HookMgr.HasOnUserMessageHandlers() {
		msgResult, _ := r.deps.Security.HookMgr.FireOnUserMessage(ctx, hook.OnUserMessageEvent{
			Channel:   msg.Channel,
			ChannelID: msg.ChannelID,
			UserID:    msg.UserID,
			Text:      msg.Text,
		})
		if len(msgResult.InjectedContext) > 0 {
			systemPrompt += "\n\n## Environment Context\n" + strings.Join(msgResult.InjectedContext, "\n")
		}
	}

	// Compress context if needed
	if _, err := r.deps.Memory.ContextMgr.Compress(ctx, sess, systemPrompt); err != nil {
		slog.Warn("context manager compression failed", "session", sess.ID, "err", err)
	}

	// Agent loop — each iteration gets its own streaming message so that
	// previous text/tool-status is not overwritten by the next response.
	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}

	for iteration := 0; iteration < r.deps.Core.Cfg.MaxIterations; iteration++ {
		slog.Info("agent iteration", "iteration", iteration, "session", sess.ID)

		if r.deps.MultiAgent.Speculative != nil {
			r.deps.MultiAgent.Speculative.Reset()
		}

		// Compute budget pressure signal for this iteration
		budgetWarning := r.computeBudgetPressure(iteration, sess, systemPrompt)

		// Push metrics to TUI status bar
		util := r.deps.Memory.ContextMgr.Utilization(sess, systemPrompt)
		m := RuntimeMetrics{
			Iteration:   iteration,
			MaxIter:     r.deps.Core.Cfg.MaxIterations,
			Utilization: util,
			Model:       r.deps.Core.LLMCfg.Model,
			Provider:    r.deps.Core.LLMCfg.Provider,
		}
		switch prov := r.deps.Core.Provider.(type) {
		case *ClaudeProvider:
			m.CacheCreate, m.CacheRead = prov.GetCacheStats()
			m.InputTokens, m.OutputTokens = prov.GetTokenStats()
		case *OpenAIProvider:
			m.CacheCreate, m.CacheRead = prov.GetCacheStats()
			m.InputTokens, m.OutputTokens = prov.GetTokenStats()
		}
		r.deps.Observability.MetricsEmitter.SendMetrics(m)
		r.deps.Observability.Emitter.EmitMetricsUpdate(sess.ID, m.Iteration, m.MaxIter, m.Utilization,
			m.InputTokens, m.OutputTokens, m.CacheCreate, m.CacheRead, m.Model, m.Provider)

		// Each iteration creates a fresh streaming message
		updater, err := ch.SendStreaming(ctx, target)
		if err != nil {
			// Fallback to non-streaming for this iteration
			err = r.handleNonStreaming(ctx, ch, sess, target, systemPrompt)
			if err == nil {
				replaySucceeded = true
			}
			return err
		}

		req := CompletionRequest{
			Model:     r.deps.Core.LLMCfg.Model,
			System:    systemPrompt,
			Messages:  BuildMessages(sess),
			Tools:     r.buildToolDefs(),
			MaxTokens: r.deps.Core.LLMCfg.MaxTokens,
		}
		if r.deps.Observability.ReplayRecorder != nil && r.replayID != "" {
			toolNames := make([]string, len(req.Tools))
			for i, td := range req.Tools {
				toolNames[i] = td.Name
			}
			r.deps.Observability.ReplayRecorder.RecordLLMRequest(ctx, r.replayID, req.Model, len(req.Messages), len(req.System), toolNames)
		}

		stream, streamErr := r.deps.Core.Provider.Stream(ctx, req)
		if streamErr != nil && isContextLengthError(streamErr) {
			_ = updater.Finish("")
			if compErr := r.deps.Memory.ContextMgr.ReactiveCompress(ctx, sess, systemPrompt); compErr != nil {
				slog.Warn("reactive compress failed", "err", compErr)
			} else {
				req.Messages = BuildMessages(sess)
				stream, streamErr = r.deps.Core.Provider.Stream(ctx, req)
			}
		}
		if streamErr != nil {
			_ = updater.Finish("Error: " + streamErr.Error())
			return fmt.Errorf("llm stream: %w", streamErr)
		}

		var fullText string
		var toolCalls []ToolUseBlock
		var stopReason StopReason

		for {
			delta, err := stream.Next()
			if err != nil {
				stream.Close()
				_ = updater.Finish("Error: " + err.Error())
				return fmt.Errorf("stream next: %w", err)
			}

			if delta.Text != "" {
				fullText += delta.Text
				_ = updater.Update(fullText)
			}

			if delta.ToolCall != nil {
				toolCalls = append(toolCalls, *delta.ToolCall)
			}
			// Collect all tool calls from the final delta
			if delta.Done && len(delta.ToolCalls) > 0 {
				toolCalls = delta.ToolCalls
			}

			// Speculative execution: launch read-only tools as their blocks complete
			if r.deps.MultiAgent.Speculative != nil {
				if ptbSrc, ok := stream.(PendingToolBlockSource); ok {
					for _, ptb := range ptbSrc.PendingToolBlocks() {
						if launched := r.deps.MultiAgent.Speculative.TryLaunch(ctx, ptb.ToolUseID, ptb.ToolName, ptb.Input); launched {
							slog.Debug("speculative launch", "tool", ptb.ToolName, "id", ptb.ToolUseID)
						}
					}
				}
			}

			if delta.Done {
				stopReason = delta.StopReason
				break
			}
		}
		stream.Close()

		// If stop reason is tool_use but we didn't capture any tool calls from stream,
		// fall back to non-streaming to get them
		if stopReason == StopToolUse && len(toolCalls) == 0 {
			resp, err := r.deps.Core.Provider.Complete(ctx, req)
			if err != nil {
				_ = updater.Finish("Error: " + err.Error())
				return err
			}
			fullText = resp.Text
			toolCalls = resp.ToolCalls
			stopReason = resp.StopReason
		}

		if r.deps.Observability.ReplayRecorder != nil && r.replayID != "" {
			var inTok, outTok int64
			switch p := r.deps.Core.Provider.(type) {
			case *ClaudeProvider:
				inTok, outTok = p.GetTokenStats()
			case *OpenAIProvider:
				inTok, outTok = p.GetTokenStats()
			}
			r.deps.Observability.ReplayRecorder.RecordLLMResponse(ctx, r.replayID, string(stopReason), inTok, outTok, len(fullText), len(toolCalls))
		}

		// Save assistant text message
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

		// If no tool calls, we're done — finalize this message
		if len(toolCalls) == 0 {
			_ = updater.Finish(fullText)
			break
		}

		// Finalize this message with tool-call status, then proceed.
		// The approval request and final answer will be separate messages.
		statusText := "Calling tools..."
		if fullText != "" {
			statusText = fullText + "\n\nCalling tools..."
		}
		_ = updater.Finish(statusText)

		// Execute tool calls
		r.executeToolsWithBudget(ctx, ch, sess, target, toolCalls, budgetWarning)
		// Next iteration will create a new streaming message for the LLM's follow-up.
	}

	// Persist session
	if err := r.deps.Core.Sessions.Persist(ctx, sess); err != nil {
		slog.Error("failed to persist session", "err", err)
	}

	// Save user message to memory.md for future retrieval
	if err := r.deps.Memory.Store.Save(ctx, memory.Entry{
		ID:        fmt.Sprintf("conv_%d", time.Now().UnixNano()),
		Scope:     memory.ScopeSession,
		SessionID: sess.ID,
		Content:   msg.Text,
		Metadata:  map[string]string{"role": "user", "channel": msg.Channel},
		CreatedAt: time.Now(),
	}); err != nil {
		slog.Warn("failed to save memory.md", "err", err)
	}

	// Extract facts and run lifecycle management in the background.
	// This mirrors the cognitive agent's reflect.go behavior but for simple mode.
	// Uses a detached context with timeout so it doesn't block the response,
	// but won't run indefinitely if the LLM call hangs.
	if r.deps.Memory.FactExtractor != nil && r.deps.Memory.LifecycleMgr != nil {
		// Capture values needed by goroutine before returning.
		sessID := sess.ID
		userID := msg.UserID
		history := sess.History()

		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("runtime: panic in fact extraction goroutine", "panic", r)
				}
			}()
			// Detach from caller ctx to prevent cancellation from killing
			// the write after the request completes, then impose our own timeout.
			bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()

			// Find the last user message and assistant response from session history.
			var userMsg, assistantMsg string
			for i := len(history) - 1; i >= 0; i-- {
				switch {
				case history[i].Role == "assistant" && assistantMsg == "":
					assistantMsg = history[i].Content
				case history[i].Role == "user" && userMsg == "":
					userMsg = history[i].Content
				}
				if userMsg != "" && assistantMsg != "" {
					break
				}
			}

			if userMsg == "" || assistantMsg == "" {
				return
			}

			facts, err := r.deps.Memory.FactExtractor.Extract(bgCtx, userMsg, assistantMsg)
			if err != nil {
				slog.Warn("runtime: fact extraction failed", "err", err)
				return
			}
			for _, fact := range facts {
				if _, err := r.deps.Memory.LifecycleMgr.Process(bgCtx, fact, sessID, userID, memory.ScopeSession); err != nil {
					slog.Warn("runtime: lifecycle process failed", "err", err, "fact", fact.Content)
				}
				if r.deps.Memory.Profiler != nil {
					r.deps.Memory.Profiler.RouteFact(bgCtx, fact)
				}
			}
		}()
	}

	r.deps.Observability.Emitter.EmitSessionEnd(sess.ID, true, time.Since(runtimeSessionStart).Milliseconds())

	replaySucceeded = true
	return nil
}

func (r *Runtime) handleNonStreaming(ctx context.Context, ch channel.Channel, sess *session.Session, target channel.MessageTarget, systemPrompt string) error {
	for iteration := 0; iteration < r.deps.Core.Cfg.MaxIterations; iteration++ {
		budgetWarning := r.computeBudgetPressure(iteration, sess, systemPrompt)

		req := CompletionRequest{
			Model:     r.deps.Core.LLMCfg.Model,
			System:    systemPrompt,
			Messages:  BuildMessages(sess),
			Tools:     r.buildToolDefs(),
			MaxTokens: r.deps.Core.LLMCfg.MaxTokens,
		}
		if r.deps.Observability.ReplayRecorder != nil && r.replayID != "" {
			toolNames := make([]string, len(req.Tools))
			for i, td := range req.Tools {
				toolNames[i] = td.Name
			}
			r.deps.Observability.ReplayRecorder.RecordLLMRequest(ctx, r.replayID, req.Model, len(req.Messages), len(req.System), toolNames)
		}

		resp, err := r.deps.Core.Provider.Complete(ctx, req)
		if err != nil && isContextLengthError(err) {
			if compErr := r.deps.Memory.ContextMgr.ReactiveCompress(ctx, sess, systemPrompt); compErr != nil {
				slog.Warn("reactive compress failed (non-streaming)", "err", compErr)
			} else {
				req.Messages = BuildMessages(sess)
				resp, err = r.deps.Core.Provider.Complete(ctx, req)
			}
		}
		if err != nil {
			return err
		}
		if r.deps.Observability.ReplayRecorder != nil && r.replayID != "" {
			var inTok, outTok int64
			switch p := r.deps.Core.Provider.(type) {
			case *ClaudeProvider:
				inTok, outTok = p.GetTokenStats()
			case *OpenAIProvider:
				inTok, outTok = p.GetTokenStats()
			}
			r.deps.Observability.ReplayRecorder.RecordLLMResponse(ctx, r.replayID, string(resp.StopReason), inTok, outTok, len(resp.Text), len(resp.ToolCalls))
		}

		if resp.Text != "" {
			sess.AddMessage(session.Message{
				ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				Role:      "assistant",
				Content:   resp.Text,
				CreatedAt: time.Now(),
			})
		}

		for _, tc := range resp.ToolCalls {
			sess.AddMessage(session.Message{
				ID:        tc.ID,
				Role:      "tool_use",
				ToolName:  tc.Name,
				ToolInput: tc.Input,
				CreatedAt: time.Now(),
			})
		}

		if len(resp.ToolCalls) == 0 {
			if err := ch.Send(ctx, channel.OutboundMessage{
				Channel:   target.Channel,
				ChannelID: target.ChannelID,
				Text:      resp.Text,
			}); err != nil {
				slog.Warn("failed to send message", "err", err)
			}
			break
		}

		r.executeToolsWithBudget(ctx, ch, sess, target, resp.ToolCalls, budgetWarning)
	}

	if err := r.deps.Core.Sessions.Persist(ctx, sess); err != nil {
		slog.Error("failed to persist session", "err", err)
	}
	return nil
}

func (r *Runtime) addToolResult(sess *session.Session, toolUseID, content string) {
	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Role:      "tool_result",
		Content:   content,
		ToolName:  toolUseID, // Store tool_use ID in ToolName for tool_result messages
		CreatedAt: time.Now(),
	})
}

// buildSystemPrompt returns the system prompt, structured as:
// Personality → core system prompt → persistent rules → memories → skills.
func (r *Runtime) buildSystemPrompt(ctx context.Context, userText string) string {
	// Check prompt cache for sub-agents
	if r.deps.MultiAgent.PromptCache != nil && r.deps.Core.AgentID != "" {
		cacheKey := fmt.Sprintf("runtime:%s:%s", r.deps.Core.AgentID, sha256Hex(userText)[:8])
		return r.deps.MultiAgent.PromptCache.GetOrBuild(cacheKey, func() string {
			return r.buildSystemPromptUncached(ctx, userText)
		})
	}
	return r.buildSystemPromptUncached(ctx, userText)
}

func (r *Runtime) buildSystemPromptUncached(ctx context.Context, userText string) string {
	var sb strings.Builder

	// 1. Personality (Soul.md)
	if r.deps.Core.Cfg.Personality != "" {
		sb.WriteString("## Personality\n")
		sb.WriteString(r.deps.Core.Cfg.Personality)
		sb.WriteString("\n\n")
	}

	// 2. Core system prompt (Agent.md + YAML system_prompt)
	sb.WriteString(r.deps.Core.Cfg.SystemPrompt)

	// 3. Persistent rules (Memory.md)
	if r.deps.Core.Cfg.PersistentRules != "" {
		sb.WriteString("\n\n## Rules\n")
		sb.WriteString(r.deps.Core.Cfg.PersistentRules)
	}

	// Cache boundary: everything above is static (cacheable), below is dynamic (per-query).
	sb.WriteString("\n")
	sb.WriteString(dynamicContextMarker)
	sb.WriteString("\n")

	// 4. Relevant memories (runtime retrieval)
	results, err := r.deps.Memory.Store.Search(ctx, memory.SearchQuery{
		Text:         userText,
		Limit:        5,
		ExcludeTypes: []string{"profile"},
	})
	if err != nil {
		slog.Warn("memory.md search failed", "err", err)
	} else if len(results) > 0 {
		sb.WriteString("\n\n## Relevant memories\n")
		for _, res := range results {
			sb.WriteString("- ")
			sb.WriteString(res.Entry.Content)
			sb.WriteString("\n")
		}
	}

	// 5. User profile (loaded from profile sections)
	if r.deps.Memory.BaseDir != "" {
		profileContent, err := memory.LoadProfileSections(r.deps.Memory.BaseDir)
		if err == nil && profileContent != "" {
			sb.WriteString("\n\n## User Profile\n")
			sb.WriteString(profileContent)
		}
	}

	// 5b. Cold-start profile building prompt
	if r.deps.Memory.Profiler != nil {
		if coldStart := r.deps.Memory.Profiler.ColdStartPrompt(); coldStart != "" {
			sb.WriteString("\n\n")
			sb.WriteString(coldStart)
		}
	}

	// 6. Skills
	if r.deps.MultiAgent.SkillMgr != nil {
		if section := r.deps.MultiAgent.SkillMgr.BuildPromptSection(userText); section != "" {
			sb.WriteString("\n\n")
			sb.WriteString(section)
			slog.Debug("skills injected into system prompt", "user_text_len", len(userText))
		}
	}

	// 7. Available agents
	if r.deps.MultiAgent.AgentMgr != nil {
		if section := r.deps.MultiAgent.AgentMgr.BuildPromptSection(); section != "" {
			sb.WriteString("\n\n")
			sb.WriteString(section)
		}
	}

	return sb.String()
}

func (r *Runtime) buildToolDefs() []ToolDefinition {
	tools := r.deps.Core.Tools.All()
	defs := make([]ToolDefinition, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}

// computeBudgetPressure generates a warning string based on iteration and token usage pressure.
// Returns empty string if no warning is needed.
func (r *Runtime) computeBudgetPressure(iteration int, sess *session.Session, systemPrompt string) string {
	var warnings []string

	// Iteration budget pressure
	iterationPct := float64(iteration+1) / float64(r.deps.Core.Cfg.MaxIterations) * 100
	if iterationPct >= 90 {
		warnings = append(warnings, fmt.Sprintf(
			"[!] Critical budget pressure: %.0f%% of iterations used. Finish current task immediately.", iterationPct))
	} else if iterationPct >= 70 {
		warnings = append(warnings, fmt.Sprintf(
			"[!] Budget pressure: %.0f%% of iterations used. Consider wrapping up.", iterationPct))
	}



	if len(warnings) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(warnings, "\n")
}

// executeToolsWithBudget wraps executeTools, appending a budget pressure signal
// to the last tool result if one is present.
func (r *Runtime) executeToolsWithBudget(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	toolCalls []ToolUseBlock,
	budgetWarning string,
) {
	if budgetWarning == "" {
		r.executeTools(ctx, ch, sess, target, toolCalls)
		return
	}

	// Execute tools normally
	r.executeTools(ctx, ch, sess, target, toolCalls)

	// Append budget warning to the last tool_result message (O(1) in-place update)
	if sess.UpdateLastToolResult(budgetWarning) {
		slog.Debug("budget pressure signal injected", "warning", budgetWarning)
	}
}

func isContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	// Use structured error kind whenever available.
	if ierrors.IsKind(err, ierrors.KindContextLength) {
		return true
	}
	// Fallback: check raw error string for API-level indicators.
	msg := err.Error()
	return strings.Contains(msg, "413") ||
		strings.Contains(msg, "context_length_exceeded") ||
		strings.Contains(msg, "maximum context length")
}
