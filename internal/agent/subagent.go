package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/tool"
)

// SubAgentManager is the central manager for sub-agent lifecycle.
// It handles spawning sub-agents with isolated sessions, scoped tools,
// and optional model overrides.
type SubAgentManager struct {
	deps           AgentDeps
	kernel         CognitiveKernel
	episodeEnabled bool
}

// NewSubAgentManager creates a new SubAgentManager.
func NewSubAgentManager(deps AgentDeps) *SubAgentManager {
	return &SubAgentManager{deps: deps}
}

// SetEpisodeKernel routes synchronous sub-agents through the episode cognitive
// kernel (forced Outcome 交账 + episode governance) when enabled. Default off:
// a nil kernel or enabled=false keeps the legacy LinearLoop, leaving behavior
// unchanged.
func (m *SubAgentManager) SetEpisodeKernel(kernel CognitiveKernel, enabled bool) {
	m.kernel = kernel
	m.episodeEnabled = enabled
}

// MaxSubAgentDepth bounds how deep sub-agent nesting can go (a sub-agent N
// levels down cannot spawn another). Matches Claude Code's nesting limit.
const MaxSubAgentDepth = 5

// SpawnRequest holds the parameters for spawning a sub-agent.
type SpawnRequest struct {
	Spec            *AgentSpec
	Task            string
	TaskContext     string
	ParentID        string
	ParentDepth     int
	ChainID         string
	ParentSessionID string // parent's session id; links the sub-session so its tool activity surfaces in the parent channel
}

// Spawn runs a sub-agent with an isolated session, scoped tools, and optional model override.
// For background execution mode, it delegates to spawnBackground.
func (m *SubAgentManager) Spawn(ctx context.Context, req SpawnRequest) (*SubAgentResult, error) {
	start := time.Now()

	if req.ParentDepth+1 > MaxSubAgentDepth {
		return nil, fmt.Errorf("sub-agent depth limit (%d) exceeded", MaxSubAgentDepth)
	}

	if req.Spec.ExecutionMode == ExecModeBackground {
		return m.spawnBackground(ctx, req)
	}

	sessionID := fmt.Sprintf("subagent_%s_%s", req.Spec.Name, uuid.New().String()[:8])

	scopedTools := buildScopedRegistryStandalone(m.deps.Core.Tools, req.Spec.Tools)

	subCfg, subLLMCfg := m.buildSubConfig(req.Spec)
	agentID := uuid.New().String()

	// Build sub-agent deps with overridden fields for isolation
	subDeps := m.deps
	subDeps.Core.Tools = scopedTools
	subDeps.Core.Cfg = subCfg
	subDeps.Core.LLMCfg = subLLMCfg
	subDeps.Core.AgentID = agentID

	subDeps = subDeps.WithDefaults()
	subAgent := NewAgent(&subDeps, &LinearLoop{}, NewEventBus())
	// Run the sub-agent as an episode (forced Outcome, episode governance) when the
	// episode kernel is wired and enabled; otherwise the legacy LinearLoop path is
	// unchanged. Background mode (spawnBackground) is not routed in this slice.
	if m.episodeEnabled && m.kernel != nil {
		subAgent.SetKernel(m.kernel, true)
	}

	chainID := req.ChainID
	if chainID == "" {
		chainID = uuid.New().String()
	}

	// Store parent tracking info in SubagentContext so sub-sub-agent
	// spawns (e.g. via AgentTool) can inherit the chain for tracing.
	subCtx := &SubagentContext{
		AgentID:  agentID,
		ParentID: req.ParentID,
		Depth:    req.ParentDepth + 1,
		ChainID:  chainID,
	}
	ctx = SubagentContextToCtx(ctx, subCtx)

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

	// Pre-create the sub-agent session and link it to the parent so the activity
	// reporter can walk up to the parent's channel and surface this sub-agent's
	// tool steps in the parent transcript. The session is cached, so HandleMessage
	// reuses it and the reporter (cache-first GetByID) sees the parent link.
	if req.ParentSessionID != "" {
		if subSess, sErr := m.deps.Core.Sessions.Get(ctx, "subagent", sessionID); sErr == nil && subSess != nil {
			subSess.SetParentSessionID(req.ParentSessionID)
		} else if sErr != nil {
			// Non-fatal: the sub-agent still runs, its tool activity just won't
			// surface in the parent transcript.
			slog.Debug("subagent: parent-session link failed; activity won't nest", "session", sessionID, "err", sErr)
		}
	}

	execErr := subAgent.HandleMessage(ctx, capture, msg)

	// When the sub-agent ran as an episode, its faithful Outcome.Status is stashed
	// on the agent; nil for the legacy LinearLoop path (buildResult then keeps its
	// reply-text parsing unchanged).
	kernelOutcome := subAgent.LastKernelOutcome()

	if err := m.deps.Core.Sessions.Delete(ctx, "subagent", sessionID); err != nil {
		slog.Warn("subagent: failed to delete session", "session", sessionID, "err", err)
	}

	result, err := m.buildResult(ctx, req.Spec.Name, capture, start, execErr, kernelOutcome)

	// Recovery retry: when the sub-agent produced empty output and the context
	// is still valid, attempt one recovery spawn. MaxRetries >= 0 is the normal
	// state; -1 is used internally as a sentinel to stop recursion.
	if err == nil && strings.TrimSpace(result.Output) == "" && ctx.Err() == nil && req.Spec.MaxRetries >= 0 {
		slog.Warn("subagent: empty result, attempting one recovery retry",
			"agent", req.Spec.Name)
		retrySpec := copySpec(req.Spec)
		retrySpec.MaxRetries = -1 // sentinel: no further retry in the nested call
		retryReq := req
		retryReq.Spec = retrySpec
		if retryResult, retryErr := m.Spawn(ctx, retryReq); retryErr == nil && strings.TrimSpace(retryResult.Output) != "" {
			slog.Info("subagent: recovery retry succeeded", "agent", req.Spec.Name)
			return retryResult, nil
		}
		slog.Warn("subagent: recovery retry also failed", "agent", req.Spec.Name)
	}

	return result, err
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
	if m.deps.MultiAgent.BgManager == nil {
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
		// Detached run: the delegating agent_<name> step has already returned, so
		// do not link to the parent session — forwarded activity would append
		// out of context, after the round closed. (The synchronous fallback above
		// keeps the link, since it blocks within the delegating round.)
		spawnReq.ParentSessionID = ""
		result, err := m.Spawn(bgCtx, spawnReq)
		if err != nil {
			return &AgentResult{AgentName: req.Spec.Name, Error: err}, nil
		}
		return &AgentResult{AgentName: req.Spec.Name, Output: result.Summary}, nil
	}

	agentID := m.deps.MultiAgent.BgManager.Spawn(ctx, req.Spec, runner)

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
	subCfg := m.deps.Core.Cfg
	if spec.MaxIterations > 0 {
		subCfg.MaxIterations = spec.MaxIterations
	}
	if spec.SystemPrompt != "" {
		subCfg.SystemPrompt = spec.SystemPrompt + subagentOutputInstruction
	} else if subCfg.SystemPrompt != "" {
		subCfg.SystemPrompt = subCfg.SystemPrompt + subagentOutputInstruction
	}

	subLLMCfg := m.deps.Core.LLMCfg
	if spec.Model != "" {
		subLLMCfg.Model = spec.Model
	}
	if spec.MaxTokens > 0 {
		subLLMCfg.MaxTokens = spec.MaxTokens
	}
	return subCfg, subLLMCfg
}

// buildResult shapes a SubAgentResult from the captured reply. kernelOutcome is
// the episode's forced Outcome when the sub-agent ran as an episode (nil for the
// legacy LinearLoop path): when present it is the authoritative status, so the
// faithful Outcome.Status is recorded on EpisodeStatus and projected onto the
// coarse Status — a blocked/handed_off/failed episode never reads as success.
func (m *SubAgentManager) buildResult(ctx context.Context, name string, capture *subagentCapture, start time.Time, execErr error, kernelOutcome *CognitiveOutcome) (*SubAgentResult, error) {
	raw := capture.LastMessage()
	dur := time.Since(start)

	result := m.deriveResult(ctx, name, raw, dur, execErr)

	if kernelOutcome != nil {
		result.EpisodeStatus = kernelOutcome.Status
		result.Status = coarseStatusForEpisode(kernelOutcome.Status)
		// A failed episode 交账's its reason in Summary, not Error; surface it so the
		// parent (agent_tool returns result.Error on StatusError) gets a meaningful
		// message instead of a blank one.
		if result.Status == StatusError && result.Error == "" {
			result.Error = "sub-agent episode " + kernelOutcome.Status + ": " + kernelOutcome.Summary
		}
	}
	return result, nil
}

// deriveResult infers a SubAgentResult from the reply text alone (the legacy
// path): execution error, then structured-block parse, then LLM summarization,
// then a truncated-raw fallback. The episode-status override is applied by the
// caller; this never sees kernelOutcome.
func (m *SubAgentManager) deriveResult(ctx context.Context, name, raw string, dur time.Duration, execErr error) *SubAgentResult {
	if execErr != nil {
		return &SubAgentResult{
			AgentName: name,
			Status:    StatusError,
			Output:    raw,
			Error:     execErr.Error(),
			Duration:  dur,
		}
	}

	if result := extractStructuredResult(raw); result != nil {
		result.AgentName = name
		result.Duration = dur
		result.Output = raw
		return result
	}

	if m.deps.Core.Provider != nil && raw != "" {
		if result := summarizeWithLLM(ctx, m.deps.Core.Provider, m.deps.Core.LLMCfg.Model, name, raw); result != nil {
			result.Duration = dur
			result.Output = raw
			return result
		}
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
	}
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

func (c *subagentCapture) LastMessage() string {
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
