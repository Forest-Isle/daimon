package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// ToolCall represents a single tool invocation in the v2 middleware chain.
type ToolCall struct {
	ID    string
	Name  string
	Input string
}

// ToolResult is the result of executing a tool call through the middleware chain.
type ToolResult struct {
	ToolCallID string
	ToolName   string
	Content    string
	IsError    bool
	Duration   time.Duration
}

// LoopState carries mutable state through the tool-use loop.
type LoopState struct {
	SessionID      string
	SystemPrompt   string
	Messages       []CompletionMessage
	ToolResults    []ToolResult
	TurnCount      int
	TotalTokens    int
	ContextUsedPct float64
	LastError      error
}

// LoopResult is the final output of the agent loop.
type LoopResult struct {
	Output      string
	ToolResults []ToolResult
	TurnCount   int
	TotalTokens int
	Assertions  []AssertionResult
	Learnings   []string
}

// ToolExecutor is the function signature for executing a single tool call.
type ToolExecutor func(ctx context.Context, call ToolCall) (*ToolResult, error)

// ToolMiddleware wraps tool execution with cross-cutting behavior.
type ToolMiddleware interface {
	Wrap(next ToolExecutor) ToolExecutor
}

// ToolMiddlewareChain composes multiple ToolMiddleware instances into a pipeline.
type ToolMiddlewareChain struct {
	middlewares []ToolMiddleware
	coreExec    ToolExecutor
}

// NewToolMiddlewareChain creates a chain from the given middleware instances.
func NewToolMiddlewareChain(mws ...ToolMiddleware) *ToolMiddlewareChain {
	return &ToolMiddlewareChain{middlewares: mws}
}

// SetCoreExecutor sets the base executor that the middleware chain wraps.
func (c *ToolMiddlewareChain) SetCoreExecutor(exec ToolExecutor) {
	c.coreExec = exec
}

// Execute runs the tool call through the middleware chain, ending with the core executor.
func (c *ToolMiddlewareChain) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
	if c.coreExec == nil {
		return nil, fmt.Errorf("tool middleware chain: core executor not set")
	}
	executor := c.coreExec
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		outer := executor
		inner := c.middlewares[i]
		executor = func(ctx context.Context, call ToolCall) (*ToolResult, error) {
			return inner.Wrap(outer)(ctx, call)
		}
	}
	return executor(ctx, call)
}

// LoopHook defines lifecycle hooks that fire before, during, and after the agent loop.
type LoopHook interface {
	BeforeLoop(ctx context.Context, state *LoopState) error
	AfterTurn(ctx context.Context, state *LoopState) error
	AfterLoop(ctx context.Context, result *LoopResult) error
}

// LoopHookChain composes multiple LoopHook instances.
type LoopHookChain struct {
	hooks []LoopHook
}

// NewLoopHookChain creates a chain from the given hooks.
func NewLoopHookChain(hooks ...LoopHook) *LoopHookChain {
	return &LoopHookChain{hooks: hooks}
}

// BeforeLoop fires BeforeLoop on all hooks in registration order.
func (c *LoopHookChain) BeforeLoop(ctx context.Context, state *LoopState) error {
	for _, h := range c.hooks {
		if err := h.BeforeLoop(ctx, state); err != nil {
			return fmt.Errorf("hook %T.BeforeLoop: %w", h, err)
		}
	}
	return nil
}

// AfterTurn fires AfterTurn on all hooks. Failures are logged but do not stop the chain.
func (c *LoopHookChain) AfterTurn(ctx context.Context, state *LoopState) error {
	for _, h := range c.hooks {
		if err := h.AfterTurn(ctx, state); err != nil {
			slog.Warn("hook AfterTurn failed", "hook", fmt.Sprintf("%T", h), "error", err)
		}
	}
	return nil
}

// AfterLoop fires AfterLoop on all hooks. Failures are logged but do not stop the chain.
func (c *LoopHookChain) AfterLoop(ctx context.Context, result *LoopResult) error {
	for _, h := range c.hooks {
		if err := h.AfterLoop(ctx, result); err != nil {
			slog.Warn("hook AfterLoop failed", "hook", fmt.Sprintf("%T", h), "error", err)
		}
	}
	return nil
}

// CognitiveAgentV2 implements the single-loop cognitive model: context injection,
// tool-use LLM loop, and self-correction post-loop. It replaces the old 5-phase
// PERCEIVE->PLAN->ACT->OBSERVE->REFLECT CognitiveAgent.
type CognitiveAgentV2 struct {
	provider       Provider
	tools          *tool.Registry
	sessions       *session.Manager
	db             *store.DB
	cfg            config.AgentConfig
	llmCfg         config.LLMConfig
	contextBuilder *ContextBuilder
	toolMiddleware *ToolMiddlewareChain
	loopHooks      *LoopHookChain
	selfCorrection *SelfCorrectionEngine
	dashEmitter    DashboardEmitter
}

// NewCognitiveAgentV2 creates a CognitiveAgentV2 with the given dependencies.
func NewCognitiveAgentV2(
	provider Provider,
	tools *tool.Registry,
	sessions *session.Manager,
	db *store.DB,
	cfg config.AgentConfig,
	llmCfg config.LLMConfig,
) *CognitiveAgentV2 {
	ca := &CognitiveAgentV2{
		provider:       provider,
		tools:          tools,
		sessions:       sessions,
		db:             db,
		cfg:            cfg,
		llmCfg:         llmCfg,
		contextBuilder: NewContextBuilder(),
		toolMiddleware: NewToolMiddlewareChain(),
		loopHooks:      NewLoopHookChain(),
		selfCorrection: NewSelfCorrectionEngine(2),
	}
	ca.toolMiddleware.SetCoreExecutor(defaultToolExecutor(tools))
	return ca
}

// SetContextBuilder replaces the default context builder.
func (ca *CognitiveAgentV2) SetContextBuilder(cb *ContextBuilder) { ca.contextBuilder = cb }

// SetToolMiddleware replaces the default tool middleware chain.
func (ca *CognitiveAgentV2) SetToolMiddleware(tm *ToolMiddlewareChain) { ca.toolMiddleware = tm }

// SetLoopHooks replaces the default loop hook chain.
func (ca *CognitiveAgentV2) SetLoopHooks(lh *LoopHookChain) { ca.loopHooks = lh }

// SetDashboardEmitter attaches a dashboard event emitter.
func (ca *CognitiveAgentV2) SetDashboardEmitter(e DashboardEmitter) { ca.dashEmitter = e }

// defaultToolExecutor creates the base ToolExecutor that calls the tool registry directly.
func defaultToolExecutor(tools *tool.Registry) ToolExecutor {
	return func(ctx context.Context, call ToolCall) (*ToolResult, error) {
		start := time.Now()
		t, err := tools.Get(call.Name)
		if err != nil {
			return &ToolResult{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    fmt.Sprintf("Error: %v", err),
				IsError:    true,
				Duration:   time.Since(start),
			}, err
		}
		result, err := t.Execute(ctx, []byte(call.Input))
		duration := time.Since(start)
		if err != nil {
			return &ToolResult{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    fmt.Sprintf("Error: %v", err),
				IsError:    true,
				Duration:   duration,
			}, err
		}
		if result.Error != "" {
			return &ToolResult{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    fmt.Sprintf("Error: %s", result.Error),
				IsError:    true,
				Duration:   duration,
			}, nil
		}
		return &ToolResult{
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result.Output,
			IsError:    false,
			Duration:   duration,
		}, nil
	}
}

// Run executes the agent loop: context injection -> tool-use LLM loop -> self-correction.
func (ca *CognitiveAgentV2) Run(
	ctx context.Context,
	sessionID string,
	userMessage string,
	extraContext string,
) (*LoopResult, error) {
	systemPrompt, messages := ca.buildInitialMessages(sessionID, userMessage, extraContext)

	state := &LoopState{
		SessionID:    sessionID,
		SystemPrompt: systemPrompt,
		Messages:     messages,
	}

	// Phase 1: Context Injection
	dynamicCtx := ca.contextBuilder.Build(ctx)
	if dynamicCtx != "" && len(state.Messages) > 0 {
		state.SystemPrompt = strings.Replace(
			state.SystemPrompt,
			"{{DYNAMIC_CONTEXT}}",
			dynamicCtx,
			1,
		)
	}

	// Loop Hooks: BeforeLoop
	if err := ca.loopHooks.BeforeLoop(ctx, state); err != nil {
		return nil, fmt.Errorf("before loop: %w", err)
	}

	// Phase 2: Tool-Use Loop
	maxTurns := ca.cfg.MaxIterations
	if maxTurns <= 0 {
		maxTurns = 20
	}

	for state.TurnCount < maxTurns {
		state.TurnCount++

		// Check context utilization - trigger hooks if over 85%
		if state.ContextUsedPct > 0.85 {
			_ = ca.loopHooks.AfterTurn(ctx, state)
		}

		// Build completion request
		req := CompletionRequest{
			Model:     ca.llmCfg.Model,
			System:    state.SystemPrompt,
			Messages:  state.Messages,
			Tools:     ca.buildToolDefs(),
			MaxTokens: ca.llmCfg.MaxTokens,
		}

		// LLM completion
		response, err := ca.provider.Complete(ctx, req)
		if err != nil {
			state.LastError = err
			_ = ca.loopHooks.AfterTurn(ctx, state)
			return nil, fmt.Errorf("LLM completion (turn %d): %w", state.TurnCount, err)
		}

		// Build assistant message with tool blocks
		assistantMsg := CompletionMessage{
			Role:    "assistant",
			Content: response.Text,
		}
		for _, tc := range response.ToolCalls {
			assistantMsg.ToolBlocks = append(assistantMsg.ToolBlocks, ToolUseBlock{
				ID:    tc.ID,
				Name:  tc.Name,
				Input: tc.Input,
			})
		}
		state.Messages = append(state.Messages, assistantMsg)

		// No tool calls -> loop complete
		if len(response.ToolCalls) == 0 {
			break
		}

		// Execute tool calls through middleware chain
		for _, tc := range response.ToolCalls {
			result, err := ca.toolMiddleware.Execute(ctx, ToolCall{
				ID:    tc.ID,
				Name:  tc.Name,
				Input: tc.Input,
			})
			if err != nil {
				slog.Warn("tool execution failed", "tool", tc.Name, "error", err)
			}
			if result != nil {
				state.ToolResults = append(state.ToolResults, *result)
				// Tool results are user-role messages with ToolUseID matching the tool use
				state.Messages = append(state.Messages, CompletionMessage{
					Role:      "user",
					Content:   result.Content,
					ToolUseID: result.ToolCallID,
				})
			}
		}

		_ = ca.loopHooks.AfterTurn(ctx, state)
	}

	// Extract final output
	lastMsg := ""
	if len(state.Messages) > 0 {
		lastMsg = state.Messages[len(state.Messages)-1].Content
	}
	result := &LoopResult{
		Output:      lastMsg,
		ToolResults: state.ToolResults,
		TurnCount:   state.TurnCount,
	}

	// Phase 3: Self-Correction
	result, err := ca.selfCorrection.VerifyAndCorrect(ctx, result,
		func(ctx context.Context, failureContext string) (*LoopResult, error) {
			return ca.Run(ctx, sessionID, userMessage,
				extraContext+"\n\n"+failureContext)
		})
	if err != nil {
		return result, err
	}

	_ = ca.loopHooks.AfterLoop(ctx, result)
	return result, nil
}

// buildInitialMessages constructs the system prompt and initial message list.
func (ca *CognitiveAgentV2) buildInitialMessages(sessionID, userMessage, extraContext string) (string, []CompletionMessage) {
	sysPrompt := fmt.Sprintf(
		"You are an AI agent with tool-use capabilities.\n\n{{DYNAMIC_CONTEXT}}\n\nSession: %s\nCurrent time: %s\n\nExecute the user's task using available tools.",
		sessionID, time.Now().Format(time.RFC3339),
	)
	if extraContext != "" {
		sysPrompt += "\n\n" + extraContext
	}
	messages := []CompletionMessage{
		{Role: "user", Content: userMessage},
	}
	return sysPrompt, messages
}

// buildToolDefs converts the tool registry to a slice of ToolDefinition for the provider.
func (ca *CognitiveAgentV2) buildToolDefs() []ToolDefinition {
	tools := ca.tools.All()
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
