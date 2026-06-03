package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/taskledger"
	"github.com/Forest-Isle/IronClaw/internal/tool"
	"github.com/Forest-Isle/IronClaw/internal/util"
)

// ApprovalFunc is called when a tool requires user approval.
// It should return true if approved, false if denied.
type ApprovalFunc func(ctx context.Context, ch channel.Channel, target channel.MessageTarget, toolName string, input string) (bool, error)

// agentContextKey is the context.Context key for storing the Agent reference.
type agentContextKey struct{}

// AgentToContext stores an Agent reference in the context.
func AgentToContext(ctx context.Context, a *Agent) context.Context {
	return context.WithValue(ctx, agentContextKey{}, a)
}

// AgentFromContext retrieves the Agent from the context.
func AgentFromContext(ctx context.Context) *Agent {
	a, _ := ctx.Value(agentContextKey{}).(*Agent)
	return a
}

// Agent is the unified agent runtime. All execution modes (simple, cognitive, graph)
// are implemented as LoopStrategy implementations.
type Agent struct {
	deps            AgentDeps
	strategy        LoopStrategy
	approvalFn      ApprovalFunc
	eventBus        EventBus
	evolutionBridge *EvolutionBridge // optional, set by gateway for evolution integration
}

// NewAgent creates a new Agent with the given dependencies, strategy, and event bus.
func NewAgent(deps AgentDeps, strategy LoopStrategy, bus EventBus) *Agent {
	return &Agent{
		deps:     deps,
		strategy: strategy,
		eventBus: bus,
	}
}

// SetStrategy replaces the current execution strategy (used for /mode switching).
func (a *Agent) SetStrategy(s LoopStrategy) { a.strategy = s }

// SetApprovalFunc sets the tool approval callback.
func (a *Agent) SetApprovalFunc(fn ApprovalFunc) { a.approvalFn = fn }

// SetEvolutionBridge wires an EvolutionBridge for forwarding events to the
// evolution engine. When nil the bridge is disabled — all evolution notifications
// become no-ops.
func (a *Agent) SetEvolutionBridge(bridge *EvolutionBridge) { a.evolutionBridge = bridge }

// SetModel updates the LLM model.
func (a *Agent) SetModel(model string) { a.deps.Core.LLMCfg.Model = model }

// Model returns the current LLM model.
func (a *Agent) Model() string { return a.deps.Core.LLMCfg.Model }

// EventBus returns the agent's event bus for external subscribers.
func (a *Agent) EventBus() EventBus { return a.eventBus }

// AgentID returns the agent's identifier.
func (a *Agent) AgentID() string { return a.deps.Core.AgentID }

// GetTools returns the tool registry.
func (a *Agent) GetTools() *tool.Registry { return a.deps.Core.Tools }

// Strategy returns the current execution strategy.
func (a *Agent) Strategy() LoopStrategy { return a.strategy }

// Sessions returns the session manager.
func (a *Agent) Sessions() *session.Manager { return a.deps.Core.Sessions }

// HandleMessage is the single entry point for all agent modes.
func (a *Agent) HandleMessage(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) error {
	ctx = AgentToContext(ctx, a)

	// Task ledger registration
	if a.deps.MultiAgent.TaskLedger != nil {
		task := taskledger.Task{
			ID:    fmt.Sprintf("req_%d", time.Now().UnixNano()),
			Kind:  taskledger.TaskKindUserRequest,
			State: taskledger.TaskStateRunning,
			Title: util.TruncateStr(msg.Text, 100),
		}
		if err := a.deps.MultiAgent.TaskLedger.Register(ctx, task); err != nil {
			slog.Warn("agent: failed to register task", "err", err)
		} else {
			defer func() {
				task.State = taskledger.TaskStateCompleted
				now := time.Now()
				task.CompletedAt = &now
				if err := a.deps.MultiAgent.TaskLedger.Update(ctx, task); err != nil {
					slog.Warn("agent: failed to complete task", "err", err)
				}
			}()
		}
	}

	sess, err := a.deps.Core.Sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	start := time.Now()
	a.eventBus.Publish(SessionStarted{SessionID: sess.ID, Channel: msg.Channel})

	// Add user message
	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Role:      "user",
		Content:   msg.Text,
		CreatedAt: time.Now(),
	})

	// Build system prompt
	systemPrompt := a.buildSystemPrompt(ctx, msg.Text)

	// Fire OnUserMessage hooks
	if a.deps.Security.HookMgr != nil && a.deps.Security.HookMgr.HasOnUserMessageHandlers() {
		msgResult, _ := a.deps.Security.HookMgr.FireOnUserMessage(ctx, hook.OnUserMessageEvent{
			Channel: msg.Channel, ChannelID: msg.ChannelID, UserID: msg.UserID, Text: msg.Text,
		})
		if len(msgResult.InjectedContext) > 0 {
			systemPrompt += "\n\n## Environment Context\n" + strings.Join(msgResult.InjectedContext, "\n")
		}
	}

	// Compress context
	if _, err := a.deps.Memory.ContextMgr.Compress(ctx, sess, systemPrompt); err != nil {
		slog.Warn("context manager compression failed", "session", sess.ID, "err", err)
	}

	// Execute strategy
	err = a.strategy.Execute(ctx, a, ch, msg, sess)

	// Save user message to memory
	if a.deps.Memory.Store != nil {
		if saveErr := a.deps.Memory.Store.Save(ctx, memory.Entry{
			ID:        fmt.Sprintf("conv_%d", time.Now().UnixNano()),
			Scope:     memory.ScopeSession,
			SessionID: sess.ID,
			Content:   msg.Text,
			Metadata:  map[string]string{"role": "user", "channel": msg.Channel},
			CreatedAt: time.Now(),
		}); saveErr != nil {
			slog.Warn("failed to save memory", "err", saveErr)
		}
	}

	// Fact extraction (async)
	if a.deps.Memory.FactExtractor != nil && a.deps.Memory.LifecycleMgr != nil {
		go a.extractFacts(context.WithoutCancel(ctx), sess.ID, msg.UserID, sess.History())
	}

	// Persist
	if err := a.deps.Core.Sessions.Persist(ctx, sess); err != nil {
		slog.Error("failed to persist session", "err", err)
	}

	a.eventBus.Publish(SessionEnded{SessionID: sess.ID, Succeeded: err == nil, DurationMs: time.Since(start).Milliseconds()})
	return err
}

// buildSystemPrompt constructs the system prompt from personality + memories + skills + profile.
func (a *Agent) buildSystemPrompt(ctx context.Context, userText string) string {
	var sb strings.Builder

	if a.deps.Core.Cfg.Personality != "" {
		sb.WriteString("## Personality\n")
		sb.WriteString(a.deps.Core.Cfg.Personality)
		sb.WriteString("\n\n")
	}
	sb.WriteString(a.deps.Core.Cfg.SystemPrompt)

	if a.deps.Core.Cfg.PersistentRules != "" {
		sb.WriteString("\n\n## Rules\n")
		sb.WriteString(a.deps.Core.Cfg.PersistentRules)
	}

	sb.WriteString("\n")
	sb.WriteString(dynamicContextMarker)
	sb.WriteString("\n")

	// Memories
	if a.deps.Memory.Store != nil {
		results, err := a.deps.Memory.Store.Search(ctx, memory.SearchQuery{
			Text:         userText,
			Limit:        5,
			ExcludeTypes: []string{"profile"},
		})
		if err == nil && len(results) > 0 {
			sb.WriteString("\n\n## Relevant memories\n")
			for _, res := range results {
				sb.WriteString("- ")
				sb.WriteString(res.Entry.Content)
				sb.WriteString("\n")
			}
		}
	}

	// Profile
	if a.deps.Memory.BaseDir != "" {
		profileContent, err := memory.LoadProfileSections(a.deps.Memory.BaseDir)
		if err == nil && profileContent != "" {
			sb.WriteString("\n\n## User Profile\n")
			sb.WriteString(profileContent)
		}
	}

	// Cold-start profile
	if a.deps.Memory.Profiler != nil {
		if coldStart := a.deps.Memory.Profiler.ColdStartPrompt(); coldStart != "" {
			sb.WriteString("\n\n")
			sb.WriteString(coldStart)
		}
	}

	// Skills
	if a.deps.MultiAgent.SkillMgr != nil {
		if section := a.deps.MultiAgent.SkillMgr.BuildPromptSection(userText); section != "" {
			sb.WriteString("\n\n")
			sb.WriteString(section)
		}
	}

	// Agents
	if a.deps.MultiAgent.AgentMgr != nil {
		if section := a.deps.MultiAgent.AgentMgr.BuildPromptSection(); section != "" {
			sb.WriteString("\n\n")
			sb.WriteString(section)
		}
	}

	return sb.String()
}

// buildToolDefs returns tool definitions for the LLM request.
func (a *Agent) buildToolDefs() []ToolDefinition {
	tools := a.deps.Core.Tools.All()
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

// executeToolCall runs a single tool through the interceptor chain and emits ToolExecuted.
func (a *Agent) executeToolCall(ctx context.Context, ch channel.Channel, sess *session.Session, target channel.MessageTarget, tc ToolUseBlock, budgetWarning string) {
	call := &tool.ToolCall{
		ToolName: tc.Name, Input: tc.Input, SessionID: sess.ID,
	}
	start := time.Now()

	result, err := a.deps.Security.Interceptor.Execute(ctx, call, func(ctx context.Context, call *tool.ToolCall) (*tool.ToolResult, error) {
		t, getErr := a.deps.Core.Tools.Get(call.ToolName)
		if getErr != nil {
			return &tool.ToolResult{Error: getErr.Error()}, nil
		}

		// Approval check (skip if pre-tool-use hook already approved)
		if t.RequiresApproval() && a.approvalFn != nil && !call.HookApproved {
			approved, approveErr := a.approvalFn(ctx, ch, target, call.ToolName, call.Input)
			if approveErr != nil || !approved {
				return &tool.ToolResult{Error: "tool execution denied by user"}, nil
			}
		}

		r, execErr := t.Execute(ctx, []byte(call.Input))
		if execErr != nil {
			return &tool.ToolResult{Error: execErr.Error()}, nil
		}
		return &tool.ToolResult{Output: r.Output}, nil
	})

	duration := time.Since(start)
	content := ""
	isError := false
	if err != nil {
		content = err.Error()
		isError = true
	} else if result != nil {
		if result.Error != "" {
			content = result.Error
			isError = true
		} else {
			content = result.Output
		}
	}

	// Fire PostToolUse hooks
	if a.deps.Security.HookMgr != nil && a.deps.Security.HookMgr.HasPostToolUseHandlers() {
		status := "success"
		if isError {
			status = "error"
		}
		a.deps.Security.HookMgr.FirePostToolUse(ctx, hook.PostToolUseEvent{
			ToolName: tc.Name,
			Input:    tc.Input,
			Output:   content,
			Status:   status,
		})
	}

	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Role:      "tool_result",
		Content:   content,
		ToolName:  tc.ID,
		CreatedAt: time.Now(),
	})

	a.eventBus.Publish(ToolExecuted{
		SessionID: sess.ID, ToolName: tc.Name, Succeeded: !isError,
		DurationMs: duration.Milliseconds(), Error: content,
	})

	if budgetWarning != "" {
		sess.UpdateLastToolResult(budgetWarning)
	}
}

// dispatchToolsParallel executes multiple independent tool calls concurrently.
// Each call goes through the full executeToolCall pipeline.
func (a *Agent) dispatchToolsParallel(ctx context.Context, ch channel.Channel, sess *session.Session, target channel.MessageTarget, calls []ToolUseBlock, budgetWarning string) {
	if len(calls) == 0 {
		return
	}
	if len(calls) == 1 {
		a.executeToolCall(ctx, ch, sess, target, calls[0], budgetWarning)
		return
	}
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func(tc ToolUseBlock) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("agent: panic in parallel tool dispatch", "tool", tc.Name, "panic", r)
				}
			}()
			a.executeToolCall(ctx, ch, sess, target, tc, budgetWarning)
		}(calls[i])
	}
	wg.Wait()
}

// extractFacts runs fact extraction and lifecycle management in the background.
func (a *Agent) extractFacts(ctx context.Context, sessID, userID string, history []session.Message) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("agent: panic in fact extraction", "panic", r)
		}
	}()
	bgCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

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
	facts, err := a.deps.Memory.FactExtractor.Extract(bgCtx, userMsg, assistantMsg)
	if err != nil {
		slog.Warn("agent: fact extraction failed", "err", err)
		return
	}
	for _, fact := range facts {
		if _, err := a.deps.Memory.LifecycleMgr.Process(bgCtx, fact, sessID, userID, memory.ScopeSession); err != nil {
			slog.Warn("agent: lifecycle process failed", "err", err, "fact", fact.Content)
		}
		if a.deps.Memory.Profiler != nil {
			a.deps.Memory.Profiler.RouteFact(bgCtx, fact)
		}
	}
}
