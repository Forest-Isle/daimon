package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/skill"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/taskledger"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// ApprovalFunc is called when a tool requires user approval.
// It should return true if approved, false if denied.
type ApprovalFunc func(ctx context.Context, ch channel.Channel, target channel.MessageTarget, toolName string, input string) (bool, error)

// Runtime orchestrates the agent loop: context → LLM → tools → reply.
type Runtime struct {
	provider       Provider
	tools          *tool.Registry
	sessions       *session.Manager
	db             *store.DB
	cfg            config.AgentConfig
	llmCfg         config.LLMConfig
	approvalFunc   ApprovalFunc
	memStore       memory.Store
	skillMgr       *skill.Manager
	agentMgr       *AgentManager
	orchestrator   *AgentOrchestrator
	compressor     *memory.IncrementalCompressor
	memoryBaseDir  string // base directory for file-based memory storage
	concurrentCfg  config.ConcurrentExecutionConfig
	resultStore    *tool.ResultStore
	compressionPipeline *CompressionPipeline
	tokenBudget    *TokenBudget
	hookMgr        *hook.Manager
	permEngine     *tool.PermissionEngine
	agentID   string // unique ID for this runtime instance
	parentID  string // parent agent ID (empty for top-level)
	depth     int    // nesting depth
	chainID   string // invocation chain ID
	bgManager *BackgroundManager
	promptCache *PromptCache
	agentMCP             *AgentMCPManager
	factExtractor        *memory.LLMFactExtractor
	lifecycleMgr         *memory.LifecycleManager
	profiler             *memory.Profiler
	contextManager       ContextManager
	profiler             *memory.Profiler
	speculativeExecutor  *SpeculativeExecutor
	taskLedger           taskledger.TaskLedger
	interceptorChain     *tool.InterceptorChain
}

// SetMemoryStore attaches a memory.md store to the runtime.
func (r *Runtime) SetMemoryStore(s memory.Store) { r.memStore = s }

// SetFactExtractor attaches a fact extractor to the runtime for lifecycle-managed memory writes.
func (r *Runtime) SetFactExtractor(fe *memory.LLMFactExtractor) { r.factExtractor = fe }

// SetLifecycleManager attaches a lifecycle manager for ADD/UPDATE/DELETE/NOOP decisions.
func (r *Runtime) SetLifecycleManager(lm *memory.LifecycleManager) { r.lifecycleMgr = lm }

// SetProfiler attaches a profiler for routing extracted facts to profile sections.
func (r *Runtime) SetProfiler(p *memory.Profiler) { r.profiler = p }

// SetMemoryBaseDir sets the base directory for file-based memory storage.
func (r *Runtime) SetMemoryBaseDir(dir string) { r.memoryBaseDir = dir }

// SetProfiler attaches a profiler for routing extracted facts to profile sections.
func (r *Runtime) SetProfiler(p *memory.Profiler) { r.profiler = p }

// SetSkillManager attaches a skill manager to the runtime.
func (r *Runtime) SetSkillManager(m *skill.Manager) { r.skillMgr = m }

// SetAgentManager attaches an agent manager to the runtime.
func (r *Runtime) SetAgentManager(m *AgentManager) { r.agentMgr = m }

// SetOrchestrator attaches an agent orchestrator to the runtime.
func (r *Runtime) SetOrchestrator(o *AgentOrchestrator) { r.orchestrator = o }

// Orchestrator returns the attached orchestrator, or nil.
func (r *Runtime) Orchestrator() *AgentOrchestrator { return r.orchestrator }

// SetCompressor attaches an incremental compressor to the runtime.
func (r *Runtime) SetCompressor(c *memory.IncrementalCompressor) { r.compressor = c }

// SetConcurrentConfig sets the concurrent execution configuration.
func (r *Runtime) SetConcurrentConfig(cfg config.ConcurrentExecutionConfig) { r.concurrentCfg = cfg }

// SetResultStore attaches a result store for persisting large tool outputs.
func (r *Runtime) SetResultStore(rs *tool.ResultStore) { r.resultStore = rs }

// SetCompressionPipeline attaches a compression pipeline to the runtime.
func (r *Runtime) SetCompressionPipeline(p *CompressionPipeline) { r.compressionPipeline = p }

// SetTokenBudget attaches a token budget monitor to the runtime.
func (r *Runtime) SetTokenBudget(tb *TokenBudget) { r.tokenBudget = tb }

// SetHookManager attaches a hook manager to the runtime.
func (r *Runtime) SetHookManager(m *hook.Manager) { r.hookMgr = m }

// SetPermissionEngine attaches a permission engine to the runtime.
func (r *Runtime) SetPermissionEngine(pe *tool.PermissionEngine) { r.permEngine = pe }

// AgentID returns this runtime's unique agent identifier.
func (r *Runtime) AgentID() string { return r.agentID }

// SetAgentID sets this runtime's agent identifier.
func (r *Runtime) SetAgentID(id string) { r.agentID = id }

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

// SetBackgroundManager attaches a background manager to the runtime.
func (r *Runtime) SetBackgroundManager(bm *BackgroundManager) { r.bgManager = bm }

// BackgroundManager returns the attached background manager, or nil.
func (r *Runtime) BackgroundManager() *BackgroundManager { return r.bgManager }

// SetPromptCache attaches a prompt cache to the runtime.
func (r *Runtime) SetPromptCache(pc *PromptCache) { r.promptCache = pc }

// PromptCache returns the attached prompt cache, or nil.
func (r *Runtime) PromptCache() *PromptCache { return r.promptCache }

// SetAgentMCPManager attaches a per-agent MCP manager to the runtime.
func (r *Runtime) SetAgentMCPManager(m *AgentMCPManager) { r.agentMCP = m }

// AgentMCPManager returns the attached per-agent MCP manager, or nil.
func (r *Runtime) AgentMCPManager() *AgentMCPManager { return r.agentMCP }


// SetContextManager attaches a context manager to the runtime.
func (r *Runtime) SetContextManager(cm ContextManager) { r.contextManager = cm }

// SetSpeculativeExecutor attaches a speculative executor for launching
// read-only tools during streaming.
func (r *Runtime) SetSpeculativeExecutor(se *SpeculativeExecutor) { r.speculativeExecutor = se }

// SetTaskLedger attaches a task ledger for tracking in-flight work.
func (r *Runtime) SetTaskLedger(tl taskledger.TaskLedger) { r.taskLedger = tl }

// SetInterceptorChain attaches an interceptor chain for sandbox-aware tool execution.
func (r *Runtime) SetInterceptorChain(chain *tool.InterceptorChain) { r.interceptorChain = chain }

// GetMessages returns a snapshot of the current session's message history.
// Returns nil if no session is active. Used by fork agents to inherit context.
func (r *Runtime) GetMessages(ctx context.Context, channelName, channelID string) []session.Message {
	sess, err := r.sessions.Get(ctx, channelName, channelID)
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

// GetTools returns the runtime's tool registry.
func (r *Runtime) GetTools() *tool.Registry { return r.tools }

func NewRuntime(
	provider Provider,
	tools *tool.Registry,
	sessions *session.Manager,
	db *store.DB,
	cfg config.AgentConfig,
	llmCfg config.LLMConfig,
) *Runtime {
	return &Runtime{
		provider: provider,
		tools:    tools,
		sessions: sessions,
		db:       db,
		cfg:      cfg,
		llmCfg:   llmCfg,
	}
}

// SetApprovalFunc sets the callback for tool approval requests.
func (r *Runtime) SetApprovalFunc(fn ApprovalFunc) {
	r.approvalFunc = fn
}

// HandleMessage processes an inbound message through the agent loop.
func (r *Runtime) HandleMessage(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) error {
	// Store this runtime in context so sub-agents can access the parent.
	ctx = RuntimeToContext(ctx, r)

	if r.taskLedger != nil {
		task := taskledger.Task{
			ID:    fmt.Sprintf("req_%d", time.Now().UnixNano()),
			Kind:  taskledger.TaskKindUserRequest,
			State: taskledger.TaskStateRunning,
			Title: truncateStr(msg.Text, 100),
		}
		if err := r.taskLedger.Register(ctx, task); err != nil {
			slog.Warn("runtime: failed to register task", "err", err)
		} else {
			defer func() {
				task.State = taskledger.TaskStateCompleted
				now := time.Now()
				task.CompletedAt = &now
				if err := r.taskLedger.Update(ctx, task); err != nil {
					slog.Warn("runtime: failed to complete task", "err", err)
				}
			}()
		}
	}

	sess, err := r.sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

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
	if r.hookMgr != nil && r.hookMgr.HasOnUserMessageHandlers() {
		msgResult, _ := r.hookMgr.FireOnUserMessage(ctx, hook.OnUserMessageEvent{
			Channel:   msg.Channel,
			ChannelID: msg.ChannelID,
			UserID:    msg.UserID,
			Text:      msg.Text,
		})
		if len(msgResult.InjectedContext) > 0 {
			systemPrompt += "\n\n## Environment Context\n" + strings.Join(msgResult.InjectedContext, "\n")
		}
	}

	// Token budget check — triggers compression if needed
	if r.tokenBudget != nil {
		check := r.tokenBudget.Check(sess.History(), systemPrompt)
		if check.Action > BudgetOK {
			slog.Info("token budget triggered compression",
				"usage_ratio", fmt.Sprintf("%.1f%%", check.UsageRatio*100),
				"action", check.Action,
			)
		}
	}

	// Compress context if needed
	if r.contextManager != nil {
		if _, err := r.contextManager.Compress(ctx, sess, systemPrompt); err != nil {
			slog.Warn("context manager compression failed", "session", sess.ID, "err", err)
		}
	} else if r.compressionPipeline != nil {
		if err := r.compressionPipeline.Run(ctx, sess, systemPrompt); err != nil {
			slog.Warn("compression pipeline failed", "session", sess.ID, "err", err)
		}
	} else {
		if err := CompactHistory(ctx, r.provider, sess, r.llmCfg.Model); err != nil {
			slog.Warn("history compaction failed", "session", sess.ID, "err", err)
		}
	}

	// Agent loop — each iteration gets its own streaming message so that
	// previous text/tool-status is not overwritten by the next response.
	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}

	for iteration := 0; iteration < r.cfg.MaxIterations; iteration++ {
		slog.Info("agent iteration", "iteration", iteration, "session", sess.ID)

		if r.speculativeExecutor != nil {
			r.speculativeExecutor.Reset()
		}

		// Compute budget pressure signal for this iteration
		budgetWarning := r.computeBudgetPressure(iteration, sess, systemPrompt)

		// Each iteration creates a fresh streaming message
		updater, err := ch.SendStreaming(ctx, target)
		if err != nil {
			// Fallback to non-streaming for this iteration
			return r.handleNonStreaming(ctx, ch, sess, target, systemPrompt)
		}

		req := CompletionRequest{
			Model:     r.llmCfg.Model,
			System:    systemPrompt,
			Messages:  BuildMessages(sess),
			Tools:     r.buildToolDefs(),
			MaxTokens: r.llmCfg.MaxTokens,
		}

		stream, streamErr := r.provider.Stream(ctx, req)
		if streamErr != nil && isContextLengthError(streamErr) && r.contextManager != nil {
			_ = updater.Finish("")
			if compErr := r.contextManager.ReactiveCompress(ctx, sess, systemPrompt); compErr != nil {
				slog.Warn("reactive compress failed", "err", compErr)
			} else {
				req.Messages = BuildMessages(sess)
				stream, streamErr = r.provider.Stream(ctx, req)
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
			if r.speculativeExecutor != nil {
				if ptbSrc, ok := stream.(PendingToolBlockSource); ok {
					for _, ptb := range ptbSrc.PendingToolBlocks() {
						if launched := r.speculativeExecutor.TryLaunch(ctx, ptb.ToolUseID, ptb.ToolName, ptb.Input); launched {
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
			resp, err := r.provider.Complete(ctx, req)
			if err != nil {
				_ = updater.Finish("Error: " + err.Error())
				return err
			}
			fullText = resp.Text
			toolCalls = resp.ToolCalls
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
		statusText := "🔧 Calling tools..."
		if fullText != "" {
			statusText = fullText + "\n\n🔧 Calling tools..."
		}
		_ = updater.Finish(statusText)

		// Execute tool calls
		r.executeToolsWithBudget(ctx, ch, sess, target, toolCalls, budgetWarning)
		// Next iteration will create a new streaming message for the LLM's follow-up.
	}

	// Persist session
	if err := r.sessions.Persist(ctx, sess); err != nil {
		slog.Error("failed to persist session", "err", err)
	}

	// Save user message to memory.md for future retrieval
	if r.memStore != nil {
		if err := r.memStore.Save(ctx, memory.Entry{
			SessionID: sess.ID,
			Content:   msg.Text,
			Metadata:  map[string]string{"role": "user", "channel": msg.Channel},
			CreatedAt: time.Now(),
		}); err != nil {
			slog.Warn("failed to save memory.md", "err", err)
		}
	}

	// Extract facts and run lifecycle management in the background.
	// This mirrors the cognitive agent's reflect.go behavior but for simple mode.
	// Uses a detached context with timeout so it doesn't block the response,
	// but won't run indefinitely if the LLM call hangs.
	if r.factExtractor != nil && r.lifecycleMgr != nil {
		// Capture values needed by goroutine before returning.
		sessID := sess.ID
		userID := msg.UserID
		history := sess.History()

		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

			facts, err := r.factExtractor.Extract(bgCtx, userMsg, assistantMsg)
			if err != nil {
				slog.Warn("runtime: fact extraction failed", "err", err)
				return
			}
			for _, fact := range facts {
				if _, err := r.lifecycleMgr.Process(bgCtx, fact, sessID, userID, memory.ScopeSession); err != nil {
					slog.Warn("runtime: lifecycle process failed", "err", err, "fact", fact.Content)
				}
				if r.profiler != nil {
					r.profiler.RouteFact(bgCtx, fact)
				}
			}
		}()
	}

	return nil
}

func (r *Runtime) handleNonStreaming(ctx context.Context, ch channel.Channel, sess *session.Session, target channel.MessageTarget, systemPrompt string) error {
	if r.tokenBudget != nil {
		check := r.tokenBudget.Check(sess.History(), systemPrompt)
		if check.Action > BudgetOK {
			slog.Info("token budget triggered compression (non-streaming)",
				"usage_ratio", fmt.Sprintf("%.1f%%", check.UsageRatio*100),
				"action", check.Action,
			)
		}
	}

	for iteration := 0; iteration < r.cfg.MaxIterations; iteration++ {
		budgetWarning := r.computeBudgetPressure(iteration, sess, systemPrompt)

		req := CompletionRequest{
			Model:     r.llmCfg.Model,
			System:    systemPrompt,
			Messages:  BuildMessages(sess),
			Tools:     r.buildToolDefs(),
			MaxTokens: r.llmCfg.MaxTokens,
		}

		resp, err := r.provider.Complete(ctx, req)
		if err != nil && isContextLengthError(err) && r.contextManager != nil {
			if compErr := r.contextManager.ReactiveCompress(ctx, sess, systemPrompt); compErr != nil {
				slog.Warn("reactive compress failed (non-streaming)", "err", compErr)
			} else {
				req.Messages = BuildMessages(sess)
				resp, err = r.provider.Complete(ctx, req)
			}
		}
		if err != nil {
			return err
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
			_ = ch.Send(ctx, channel.OutboundMessage{
				Channel:   target.Channel,
				ChannelID: target.ChannelID,
				Text:      resp.Text,
			})
			break
		}

		r.executeToolsWithBudget(ctx, ch, sess, target, resp.ToolCalls, budgetWarning)
	}

	if err := r.sessions.Persist(ctx, sess); err != nil {
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
	if r.promptCache != nil && r.agentID != "" {
		cacheKey := fmt.Sprintf("runtime:%s:%s", r.agentID, sha256Hex(userText)[:8])
		return r.promptCache.GetOrBuild(cacheKey, func() string {
			return r.buildSystemPromptUncached(ctx, userText)
		})
	}
	return r.buildSystemPromptUncached(ctx, userText)
}

func (r *Runtime) buildSystemPromptUncached(ctx context.Context, userText string) string {
	var sb strings.Builder

	// 1. Personality (Soul.md)
	if r.cfg.Personality != "" {
		sb.WriteString("## Personality\n")
		sb.WriteString(r.cfg.Personality)
		sb.WriteString("\n\n")
	}

	// 2. Core system prompt (Agent.md + YAML system_prompt)
	sb.WriteString(r.cfg.SystemPrompt)

	// 3. Persistent rules (Memory.md)
	if r.cfg.PersistentRules != "" {
		sb.WriteString("\n\n## Rules\n")
		sb.WriteString(r.cfg.PersistentRules)
	}

	// Cache boundary: everything above is static (cacheable), below is dynamic (per-query).
	sb.WriteString("\n")
	sb.WriteString(dynamicContextMarker)
	sb.WriteString("\n")

	// 4. Relevant memories (runtime retrieval)
	if r.memStore != nil {
		results, err := r.memStore.Search(ctx, memory.SearchQuery{
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
	}

	// 5. User profile (loaded from profile sections)
	if r.memoryBaseDir != "" {
		profileContent, err := memory.LoadProfileSections(r.memoryBaseDir)
		if err == nil && profileContent != "" {
			sb.WriteString("\n\n## User Profile\n")
			sb.WriteString(profileContent)
		}
	}

	// 5b. Cold-start profile building prompt
	if r.profiler != nil {
		if coldStart := r.profiler.ColdStartPrompt(); coldStart != "" {
			sb.WriteString("\n\n")
			sb.WriteString(coldStart)
		}
	}

	// 6. Skills
	if r.skillMgr != nil {
		if section := r.skillMgr.BuildPromptSection(userText); section != "" {
			sb.WriteString("\n\n")
			sb.WriteString(section)
			slog.Debug("skills injected into system prompt", "user_text_len", len(userText))
		}
	}

	// 7. Available agents
	if r.agentMgr != nil {
		if section := r.agentMgr.BuildPromptSection(); section != "" {
			sb.WriteString("\n\n")
			sb.WriteString(section)
		}
	}

	return sb.String()
}

func (r *Runtime) buildToolDefs() []ToolDefinition {
	tools := r.tools.All()
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
	iterationPct := float64(iteration+1) / float64(r.cfg.MaxIterations) * 100
	if iterationPct >= 90 {
		warnings = append(warnings, fmt.Sprintf(
			"🚨 Critical budget pressure: %.0f%% of iterations used. Finish current task immediately.", iterationPct))
	} else if iterationPct >= 70 {
		warnings = append(warnings, fmt.Sprintf(
			"⚠️ Budget pressure: %.0f%% of iterations used. Consider wrapping up.", iterationPct))
	}

	// Token budget pressure
	if r.tokenBudget != nil {
		check := r.tokenBudget.Check(sess.History(), systemPrompt)
		tokenPct := check.UsageRatio * 100
		if tokenPct >= 90 {
			warnings = append(warnings, fmt.Sprintf(
				"🚨 Critical token budget: %.0f%% of context window used. Be extremely concise.", tokenPct))
		} else if tokenPct >= 70 {
			warnings = append(warnings, fmt.Sprintf(
				"⚠️ Token budget pressure: %.0f%% of context window used. Consider being more concise.", tokenPct))
		}
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
	msg := err.Error()
	return strings.Contains(msg, "413") ||
		strings.Contains(msg, "context_length_exceeded") ||
		strings.Contains(msg, "maximum context length")
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
