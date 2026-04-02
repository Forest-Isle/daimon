package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/punkopunko/ironclaw/internal/channel"
	"github.com/punkopunko/ironclaw/internal/config"
	"github.com/punkopunko/ironclaw/internal/memory"
	"github.com/punkopunko/ironclaw/internal/session"
	"github.com/punkopunko/ironclaw/internal/store"
	"github.com/punkopunko/ironclaw/internal/tool"
)

// agentToolInput is the JSON input format for AgentTool.
type agentToolInput struct {
	Task    string `json:"task"`
	Context string `json:"context,omitempty"`
}

// AgentTool wraps an AgentSpec as a tool.Tool, creating a temporary Runtime
// for each invocation that captures output via captureChannel.
type AgentTool struct {
	spec      *AgentSpec
	provider  Provider
	sessions  *session.Manager
	db        *store.DB
	memStore  memory.Store
	tools     *tool.Registry // parent registry (for scoping)
	cfg       config.AgentConfig
	llmCfg    config.LLMConfig
	breaker   *CircuitBreaker
	bgManager *BackgroundManager
}

// NewAgentTool creates a new AgentTool for the given spec.
func NewAgentTool(
	spec *AgentSpec,
	provider Provider,
	sessions *session.Manager,
	db *store.DB,
	memStore memory.Store,
	tools *tool.Registry,
	cfg config.AgentConfig,
	llmCfg config.LLMConfig,
) *AgentTool {
	return &AgentTool{
		spec:     spec,
		provider: provider,
		sessions: sessions,
		db:       db,
		memStore: memStore,
		tools:    tools,
		cfg:      cfg,
		llmCfg:   llmCfg,
		breaker:  NewCircuitBreaker(),
	}
}

// SetBackgroundManager attaches a background manager to this agent tool.
func (a *AgentTool) SetBackgroundManager(bm *BackgroundManager) { a.bgManager = bm }

func (a *AgentTool) Name() string {
	return "agent_" + a.spec.Name
}

func (a *AgentTool) Description() string {
	return a.spec.Description
}

func (a *AgentTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "The task to delegate to this agent",
			},
			"context": map[string]any{
				"type":        "string",
				"description": "Optional context from previous tasks (predecessor outputs, etc.)",
			},
		},
		"required": []string{"task"},
	}
}

func (a *AgentTool) RequiresApproval() bool {
	return a.spec.RequiresApproval
}

// Execute dispatches to executeSpawn or executeFork based on the spec's ExecutionMode.
func (a *AgentTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	// Check circuit breaker
	if err := a.breaker.Allow(); err != nil {
		return tool.Result{Error: err.Error()}, nil
	}

	var in agentToolInput
	if err := json.Unmarshal(input, &in); err != nil {
		a.breaker.RecordFailure()
		return tool.Result{Error: "invalid input: " + err.Error()}, nil
	}

	if in.Task == "" {
		a.breaker.RecordFailure()
		return tool.Result{Error: "task field is required"}, nil
	}

	// Apply timeout
	timeout := a.spec.Timeout.Duration()
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	slog.Info("agent_tool: executing sub-agent",
		"agent", a.spec.Name,
		"task_len", len(in.Task),
		"timeout", timeout,
		"mode", a.spec.ExecutionMode,
	)

	switch a.spec.ExecutionMode {
	case ExecModeFork:
		return a.executeFork(ctx, in)
	case ExecModeBackground:
		return a.executeBackground(ctx, in)
	default:
		return a.executeSpawn(ctx, in)
	}
}

// executeSpawn creates an independent Runtime and runs the sub-agent (current / default behavior).
func (a *AgentTool) executeSpawn(ctx context.Context, in agentToolInput) (tool.Result, error) {
	// Build scoped tool registry
	scopedTools := a.buildScopedRegistry()

	// Build sub-agent config with spec overrides
	subCfg := a.cfg
	subCfg.MaxIterations = a.spec.MaxIterations
	if a.spec.SystemPrompt != "" {
		subCfg.SystemPrompt = a.spec.SystemPrompt
	}

	subLLMCfg := a.llmCfg
	if a.spec.Model != "" {
		subLLMCfg.Model = a.spec.Model
	}
	if a.spec.MaxTokens > 0 {
		subLLMCfg.MaxTokens = a.spec.MaxTokens
	}

	// Create temporary Runtime
	subRuntime := NewRuntime(a.provider, scopedTools, a.sessions, a.db, subCfg, subLLMCfg)
	if a.memStore != nil {
		subRuntime.SetMemoryStore(a.memStore)
	}

	// Build user message with optional context
	userText := in.Task
	if in.Context != "" {
		userText = fmt.Sprintf("Context from previous tasks:\n%s\n\nTask:\n%s", in.Context, in.Task)
	}

	// Create captureChannel and run
	capture := newCaptureChannel()
	msg := channel.InboundMessage{
		Channel:   "agent",
		ChannelID: fmt.Sprintf("agent_%s", a.spec.Name),
		UserID:    "orchestrator",
		UserName:  "orchestrator",
		Text:      userText,
	}

	if err := subRuntime.HandleMessage(ctx, capture, msg); err != nil {
		a.breaker.RecordFailure()
		return tool.Result{Error: "sub-agent error: " + err.Error()}, nil
	}

	// Collect output from captureChannel
	output := capture.Collect()
	if output == "" {
		output = "(no output from sub-agent)"
	}

	a.breaker.RecordSuccess()
	slog.Info("agent_tool: sub-agent completed",
		"agent", a.spec.Name,
		"output_len", len(output),
	)

	return tool.Result{Output: output}, nil
}

// executeFork inherits the parent Runtime's session context and runs as a fork agent.
func (a *AgentTool) executeFork(ctx context.Context, in agentToolInput) (tool.Result, error) {
	// Get parent Runtime from context
	parentRuntime := RuntimeFromContext(ctx)
	if parentRuntime == nil {
		slog.Warn("agent_tool: fork mode requested but no parent Runtime in context, falling back to spawn",
			"agent", a.spec.Name,
		)
		return a.executeSpawn(ctx, in)
	}

	// Check fork depth using parent's SubagentContext (if any)
	parentSC := SubagentContextFromCtx(ctx)
	if err := CheckForkDepth(parentSC); err != nil {
		a.breaker.RecordFailure()
		return tool.Result{Error: "fork depth limit reached: " + err.Error()}, nil
	}

	// Build scoped tool registry
	scopedTools := a.buildScopedRegistry()

	// Build sub-agent config with spec overrides
	subCfg := a.cfg
	subCfg.MaxIterations = a.spec.MaxIterations
	if a.spec.SystemPrompt != "" {
		subCfg.SystemPrompt = a.spec.SystemPrompt
	}

	subLLMCfg := a.llmCfg
	if a.spec.Model != "" {
		subLLMCfg.Model = a.spec.Model
	}
	if a.spec.MaxTokens > 0 {
		subLLMCfg.MaxTokens = a.spec.MaxTokens
	}

	// Create sub-Runtime
	subRuntime := NewRuntime(a.provider, scopedTools, a.sessions, a.db, subCfg, subLLMCfg)
	if a.memStore != nil {
		subRuntime.SetMemoryStore(a.memStore)
	}

	// Set lineage tracking
	agentID := uuid.New().String()
	parentID := parentRuntime.AgentID()
	depth := parentRuntime.Depth() + 1
	chainID := parentRuntime.ChainID()
	if chainID == "" {
		chainID = uuid.New().String()
	}

	subRuntime.SetAgentID(agentID)
	subRuntime.SetParentID(parentID)
	subRuntime.SetDepth(depth)
	subRuntime.SetChainID(chainID)

	// Build SubagentContext
	childCtx, childCancel := context.WithCancel(ctx)
	sc := &SubagentContext{
		ToolRegistry:  scopedTools,
		Permission:    a.spec.PermissionMode,
		Cancel:        childCancel,
		AbortOnParent: true,
		Memory:        a.memStore,
		Sessions:      a.sessions,
		DB:            a.db,
		AgentID:       agentID,
		ParentID:      parentID,
		Depth:         depth,
		ChainID:       chainID,
	}

	// Inherit parent messages for fork context
	if parentSC != nil {
		sc.ParentMessages = parentSC.ParentMessages
		sc.SystemPrompt = parentSC.SystemPrompt
	}

	// Inject SubagentContext and child Runtime into child context
	childCtx = SubagentContextToCtx(childCtx, sc)
	childCtx = RuntimeToContext(childCtx, subRuntime)

	// Build user message with optional context
	userText := in.Task
	if in.Context != "" {
		userText = fmt.Sprintf("Context from previous tasks:\n%s\n\nTask:\n%s", in.Context, in.Task)
	}

	// Create captureChannel and run
	capture := newCaptureChannel()
	msg := channel.InboundMessage{
		Channel:   "agent",
		ChannelID: fmt.Sprintf("agent_%s", a.spec.Name),
		UserID:    "orchestrator",
		UserName:  "orchestrator",
		Text:      userText,
	}

	if err := subRuntime.HandleMessage(childCtx, capture, msg); err != nil {
		a.breaker.RecordFailure()
		return tool.Result{Error: "sub-agent fork error: " + err.Error()}, nil
	}

	// Collect output from captureChannel
	output := capture.Collect()
	if output == "" {
		output = "(no output from sub-agent)"
	}

	a.breaker.RecordSuccess()
	slog.Info("agent_tool: fork sub-agent completed",
		"agent", a.spec.Name,
		"agent_id", agentID,
		"depth", depth,
		"output_len", len(output),
	)

	return tool.Result{Output: output}, nil
}

// executeBackground launches the agent in the background and returns immediately
// with the background agent's ID. The caller can query the result later via
// the BackgroundManager.
func (a *AgentTool) executeBackground(ctx context.Context, in agentToolInput) (tool.Result, error) {
	if a.bgManager == nil {
		slog.Warn("agent_tool: no BackgroundManager available, falling back to spawn",
			"agent", a.spec.Name,
		)
		return a.executeSpawn(ctx, in)
	}

	runner := func(bgCtx context.Context) (*AgentResult, error) {
		start := time.Now()
		result, err := a.executeSpawn(bgCtx, in)
		duration := time.Since(start)

		if err != nil {
			return &AgentResult{
				AgentName: a.spec.Name,
				Error:     err,
				Duration:  duration,
			}, nil
		}
		if result.Error != "" {
			return &AgentResult{
				AgentName: a.spec.Name,
				Error:     fmt.Errorf("%s", result.Error),
				Duration:  duration,
			}, nil
		}
		return &AgentResult{
			AgentName: a.spec.Name,
			Output:    result.Output,
			Duration:  duration,
		}, nil
	}

	agentID := a.bgManager.Spawn(ctx, a.spec, runner)
	a.breaker.RecordSuccess()

	slog.Info("agent_tool: background agent spawned",
		"agent", a.spec.Name,
		"agent_id", agentID,
	)

	return tool.Result{
		Output: fmt.Sprintf("Background agent started: %s\nAgent ID: %s\nUse the background manager to query results.", a.spec.Name, agentID),
	}, nil
}

// buildScopedRegistry creates a new Registry containing only the tools
// allowed by the spec. agent_* tools are always excluded to prevent recursion.
func (a *AgentTool) buildScopedRegistry() *tool.Registry {
	scoped := tool.NewRegistry()
	allTools := a.tools.All()

	for _, t := range allTools {
		name := t.Name()

		// Always exclude agent_* tools to prevent recursive sub-agent calls
		if strings.HasPrefix(name, "agent_") {
			continue
		}

		// If whitelist is specified, only include listed tools
		if len(a.spec.Tools) > 0 {
			if !contains(a.spec.Tools, name) {
				continue
			}
		}

		scoped.Register(t)
	}

	return scoped
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// captureChannel implements channel.Channel by recording all outbound messages
// in memory.md. It is used to capture sub-agent output without sending to external platforms.
type captureChannel struct {
	mu       sync.Mutex
	messages []string
}

func newCaptureChannel() *captureChannel {
	return &captureChannel{}
}

func (c *captureChannel) Name() string { return "capture" }

func (c *captureChannel) Start(_ context.Context, _ channel.InboundHandler) error {
	return nil
}

func (c *captureChannel) Send(_ context.Context, msg channel.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if msg.Text != "" {
		c.messages = append(c.messages, msg.Text)
	}
	return nil
}

func (c *captureChannel) SendStreaming(_ context.Context, _ channel.MessageTarget) (channel.StreamUpdater, error) {
	return &captureUpdater{ch: c}, nil
}

func (c *captureChannel) Stop(_ context.Context) error {
	return nil
}

// Collect returns all captured messages concatenated, returning only the last
// (final) message as the sub-agent output.
func (c *captureChannel) Collect() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		return ""
	}
	// Return the last message — this is the sub-agent's final response.
	// Intermediate messages are progress/tool status updates.
	return c.messages[len(c.messages)-1]
}

// captureUpdater implements channel.StreamUpdater for captureChannel.
type captureUpdater struct {
	ch *captureChannel
}

func (u *captureUpdater) Update(_ string) error {
	// Ignore intermediate streaming updates — we only care about Finish
	return nil
}

func (u *captureUpdater) Finish(text string) error {
	u.ch.mu.Lock()
	defer u.ch.mu.Unlock()
	if text != "" {
		u.ch.messages = append(u.ch.messages, text)
	}
	return nil
}
