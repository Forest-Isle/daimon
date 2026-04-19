package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// SubAgentManager is the central manager for sub-agent lifecycle.
// It handles spawning sub-agents with isolated sessions, scoped tools,
// and optional model overrides.
type SubAgentManager struct {
	provider  Provider
	sessions  *session.Manager
	db        *store.DB
	memStore  memory.Store
	tools     *tool.Registry
	cfg       config.AgentConfig
	llmCfg    config.LLMConfig
	bgManager *BackgroundManager
	agentMCP  *AgentMCPManager
}

// NewSubAgentManager creates a new SubAgentManager.
func NewSubAgentManager(
	provider Provider,
	sessions *session.Manager,
	db *store.DB,
	memStore memory.Store,
	tools *tool.Registry,
	cfg config.AgentConfig,
	llmCfg config.LLMConfig,
) *SubAgentManager {
	return &SubAgentManager{
		provider: provider,
		sessions: sessions,
		db:       db,
		memStore: memStore,
		tools:    tools,
		cfg:      cfg,
		llmCfg:   llmCfg,
	}
}

// SetBackgroundManager attaches a background manager for async spawns.
func (m *SubAgentManager) SetBackgroundManager(bm *BackgroundManager) { m.bgManager = bm }

// SetAgentMCPManager attaches a per-agent MCP manager.
func (m *SubAgentManager) SetAgentMCPManager(mgr *AgentMCPManager) { m.agentMCP = mgr }

// SpawnRequest holds the parameters for spawning a sub-agent.
type SpawnRequest struct {
	Spec        *AgentSpec
	Task        string
	TaskContext string
	ParentID    string
	ParentDepth int
	ChainID     string
}

// Spawn runs a sub-agent with an isolated session, scoped tools, and optional model override.
// For background execution mode, it delegates to spawnBackground.
func (m *SubAgentManager) Spawn(ctx context.Context, req SpawnRequest) (*SubAgentResult, error) {
	start := time.Now()

	if req.Spec.ExecutionMode == ExecModeBackground {
		return m.spawnBackground(ctx, req)
	}

	sessionID := fmt.Sprintf("subagent_%s_%s", req.Spec.Name, uuid.New().String()[:8])

	scopedTools := buildScopedRegistryStandalone(m.tools, req.Spec.Tools)
	subCfg, subLLMCfg := m.buildSubConfig(req.Spec)

	subRuntime := NewRuntime(m.provider, scopedTools, m.sessions, m.db, subCfg, subLLMCfg)
	if m.memStore != nil {
		subRuntime.SetMemoryStore(m.memStore)
	}

	agentID := uuid.New().String()
	chainID := req.ChainID
	if chainID == "" {
		chainID = uuid.New().String()
	}
	subRuntime.SetAgentID(agentID)
	subRuntime.SetParentID(req.ParentID)
	subRuntime.SetDepth(req.ParentDepth + 1)
	subRuntime.SetChainID(chainID)

	userText := req.Task
	if req.TaskContext != "" {
		userText = fmt.Sprintf("Context from previous tasks:\n%s\n\nTask:\n%s", req.TaskContext, req.Task)
	}

	capture := newSubagentCapture()
	msg := channel.InboundMessage{
		Channel:   "subagent",
		ChannelID: sessionID,
		UserID:    "orchestrator",
		UserName:  "orchestrator",
		Text:      userText,
	}

	execErr := subRuntime.HandleMessage(ctx, capture, msg)

	_ = m.sessions.Delete(ctx, "subagent", sessionID)

	return m.buildResult(ctx, req.Spec.Name, capture, start, execErr)
}

// SpawnParallel runs multiple sub-agents concurrently with the given failure strategy.
func (m *SubAgentManager) SpawnParallel(ctx context.Context, reqs []SpawnRequest, strategy FailureStrategy) ([]*SubAgentResult, error) {
	results := make([]*SubAgentResult, len(reqs))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i, req := range reqs {
		wg.Add(1)
		go func(idx int, r SpawnRequest) {
			defer wg.Done()

			result, err := m.Spawn(ctx, r)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				results[idx] = &SubAgentResult{
					AgentName: r.Spec.Name,
					Status:    StatusError,
					Error:     err.Error(),
				}
				if strategy == StrategyFailFast && firstErr == nil {
					firstErr = fmt.Errorf("sub-agent %s failed: %w", r.Spec.Name, err)
					cancel()
				}
				return
			}

			results[idx] = result

			if result.Status == StatusError && strategy == StrategyFailFast && firstErr == nil {
				firstErr = fmt.Errorf("sub-agent %s failed: %s", r.Spec.Name, result.Error)
				cancel()
			}
		}(i, req)
	}

	wg.Wait()

	if strategy == StrategyFailFast && firstErr != nil {
		return results, firstErr
	}
	return results, nil
}

func (m *SubAgentManager) spawnBackground(ctx context.Context, req SpawnRequest) (*SubAgentResult, error) {
	if m.bgManager == nil {
		slog.Warn("subagent: no BackgroundManager, falling back to sync spawn", "agent", req.Spec.Name)
		syncReq := req
		syncReq.Spec = copySpec(req.Spec)
		syncReq.Spec.ExecutionMode = ExecModeSpawn
		return m.Spawn(ctx, syncReq)
	}

	runner := func(bgCtx context.Context) (*AgentResult, error) {
		spawnReq := req
		spawnReq.Spec = copySpec(req.Spec)
		spawnReq.Spec.ExecutionMode = ExecModeSpawn
		result, err := m.Spawn(bgCtx, spawnReq)
		if err != nil {
			return &AgentResult{AgentName: req.Spec.Name, Error: err}, nil
		}
		return &AgentResult{AgentName: req.Spec.Name, Output: result.Summary}, nil
	}

	agentID := m.bgManager.Spawn(ctx, req.Spec, runner)

	return &SubAgentResult{
		AgentName: req.Spec.Name,
		Status:    StatusBackground,
		Summary:   fmt.Sprintf("Background agent started: %s (ID: %s)", req.Spec.Name, agentID),
	}, nil
}

func copySpec(s *AgentSpec) *AgentSpec {
	cp := *s
	return &cp
}

func (m *SubAgentManager) buildSubConfig(spec *AgentSpec) (config.AgentConfig, config.LLMConfig) {
	subCfg := m.cfg
	if spec.MaxIterations > 0 {
		subCfg.MaxIterations = spec.MaxIterations
	}
	if spec.SystemPrompt != "" {
		subCfg.SystemPrompt = spec.SystemPrompt + subagentOutputInstruction
	} else if subCfg.SystemPrompt != "" {
		subCfg.SystemPrompt = subCfg.SystemPrompt + subagentOutputInstruction
	}

	subLLMCfg := m.llmCfg
	if spec.Model != "" {
		subLLMCfg.Model = spec.Model
	}
	if spec.MaxTokens > 0 {
		subLLMCfg.MaxTokens = spec.MaxTokens
	}
	return subCfg, subLLMCfg
}

func (m *SubAgentManager) buildResult(_ context.Context, name string, capture *subagentCapture, start time.Time, execErr error) (*SubAgentResult, error) {
	raw := capture.Collect()
	dur := time.Since(start)

	if execErr != nil {
		return &SubAgentResult{
			AgentName: name,
			Status:    StatusError,
			Output:    raw,
			Error:     execErr.Error(),
			Duration:  dur,
		}, nil
	}

	if result := extractStructuredResult(raw); result != nil {
		result.AgentName = name
		result.Duration = dur
		result.Output = raw
		return result, nil
	}

	summary := raw
	if len(summary) > 500 {
		summary = summary[:500] + "..."
	}
	return &SubAgentResult{
		AgentName: name,
		Status:    StatusSuccess,
		Summary:   summary,
		Output:    raw,
		Duration:  dur,
	}, nil
}

// buildScopedRegistryStandalone creates a tool registry scoped to the given whitelist.
// agent_* tools are always excluded to prevent recursive sub-agent calls.
// This is a standalone version — agent_tool.go has its own method version.
func buildScopedRegistryStandalone(parent *tool.Registry, whitelist []string) *tool.Registry {
	scoped := tool.NewRegistry()
	for _, t := range parent.All() {
		name := t.Name()
		if strings.HasPrefix(name, "agent_") {
			continue
		}
		if len(whitelist) > 0 {
			found := false
			for _, w := range whitelist {
				if w == name {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		scoped.Register(t)
	}
	return scoped
}

// subagentCapture implements channel.Channel by recording outbound messages.
// Named differently from captureChannel in agent_tool.go to avoid conflicts until Task 7 cleanup.
type subagentCapture struct {
	mu       sync.Mutex
	messages []string
}

func newSubagentCapture() *subagentCapture {
	return &subagentCapture{}
}

func (c *subagentCapture) Name() string { return "subagent_capture" }

func (c *subagentCapture) Start(_ context.Context, _ channel.InboundHandler) error { return nil }

func (c *subagentCapture) Send(_ context.Context, msg channel.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if msg.Text != "" {
		c.messages = append(c.messages, msg.Text)
	}
	return nil
}

func (c *subagentCapture) SendStreaming(_ context.Context, _ channel.MessageTarget) (channel.StreamUpdater, error) {
	return &subagentCaptureUpdater{ch: c}, nil
}

func (c *subagentCapture) Stop(_ context.Context) error { return nil }

func (c *subagentCapture) Collect() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		return ""
	}
	return c.messages[len(c.messages)-1]
}

type subagentCaptureUpdater struct {
	ch *subagentCapture
}

func (u *subagentCaptureUpdater) Update(_ string) error { return nil }

func (u *subagentCaptureUpdater) Finish(text string) error {
	u.ch.mu.Lock()
	defer u.ch.mu.Unlock()
	if text != "" {
		u.ch.messages = append(u.ch.messages, text)
	}
	return nil
}
