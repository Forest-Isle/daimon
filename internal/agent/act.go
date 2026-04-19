package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/rl"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// Executor implements the ACT phase: topological scheduling + parallel execution.
type Executor struct {
	tools        *tool.Registry
	db           *store.DB
	approvalFunc ApprovalFunc
	cfg          config.CognitiveConfig
	rlPolicy     RLPolicy // optional RL policy
	hookMgr      *hook.Manager
	permEngine       *tool.PermissionEngine
	cache            *ToolResultCache
	interceptorChain *tool.InterceptorChain
}

// NewExecutor creates a new Executor.
func NewExecutor(tools *tool.Registry, db *store.DB, approvalFunc ApprovalFunc, cfg config.CognitiveConfig) *Executor {
	return &Executor{
		tools:        tools,
		db:           db,
		approvalFunc: approvalFunc,
		cfg:          cfg,
	}
}

// SetRLPolicy injects an optional RL policy.
func (e *Executor) SetRLPolicy(policy RLPolicy) {
	e.rlPolicy = policy
}

// SetHookManager injects a hook manager for pre/post tool-use hooks.
func (e *Executor) SetHookManager(mgr *hook.Manager) {
	e.hookMgr = mgr
}

// SetPermissionEngine injects a permission engine for rule-based access control.
func (e *Executor) SetPermissionEngine(pe *tool.PermissionEngine) {
	e.permEngine = pe
}

// SetToolCache injects a tool result cache for read-only tool deduplication.
func (e *Executor) SetToolCache(cache *ToolResultCache) {
	e.cache = cache
}

// SetInterceptorChain attaches an interceptor chain for sandbox-aware tool execution.
func (e *Executor) SetInterceptorChain(chain *tool.InterceptorChain) {
	e.interceptorChain = chain
}

// Run executes the ACT phase — topological ordering + parallel execution.
func (e *Executor) Run(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	plan *TaskPlan,
) ([]Observation, error) {
	return e.RunWithContext(ctx, ch, sess, target, plan, nil, nil, nil)
}

// RunWithContext executes the ACT phase with an optional TaskContext for multi-agent collaboration.
// rlState and collector are optional (nil when RL is disabled).
func (e *Executor) RunWithContext(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	plan *TaskPlan,
	taskCtx *TaskContext,
	rlState *rl.RLState,
	collector *EpisodeCollector,
) ([]Observation, error) {
	maxParallel := e.cfg.MaxParallelTools
	if maxParallel <= 0 {
		maxParallel = 3
	}

	total := len(plan.SubTasks)
	var observations []Observation
	var obsMu sync.Mutex

	// Build task index
	taskIndex := make(map[string]*SubTask, total)
	for _, st := range plan.SubTasks {
		taskIndex[st.ID] = st
	}

	doneCount := 0

	for {
		// Collect all pending tasks whose dependencies are all Done
		ready := collectReady(plan.SubTasks, taskIndex)
		if len(ready) == 0 {
			break
		}

		// Check if any running tasks (shouldn't happen in serial rounds, but safety)
		anyRunning := false
		for _, st := range plan.SubTasks {
			if st.Status == SubTaskRunning {
				anyRunning = true
				break
			}
		}
		if anyRunning {
			break
		}

		// Batch into maxParallel
		if len(ready) > maxParallel {
			ready = ready[:maxParallel]
		}

		// Mark as running
		for _, st := range ready {
			st.Status = SubTaskRunning
		}

		var wg sync.WaitGroup
		for _, st := range ready {
			wg.Add(1)
			go func(subtask *SubTask) {
				defer wg.Done()
				obs := e.executeSubTask(ctx, ch, sess, target, subtask, plan.SubTasks, taskCtx, plan, rlState, collector)
				obsMu.Lock()
				observations = append(observations, obs)
				doneCount++
				obsMu.Unlock()

				// Stream progress to user
				progressMsg := fmt.Sprintf("[%d/%d] %s... %s",
					doneCount, total, subtask.Description, statusEmoji(subtask.Status))
				sendProgress(ctx, ch, target, progressMsg)
			}(st)
		}
		wg.Wait()

		// Check if all remaining tasks are stuck (failed with no ready tasks)
		allDone := true
		for _, st := range plan.SubTasks {
			if st.Status == SubTaskPending || st.Status == SubTaskRunning {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}
	}

	return observations, nil
}

// executeSubTask runs a single subtask and returns its observation.
func (e *Executor) executeSubTask(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	subtask *SubTask,
	allTasks []*SubTask,
	taskCtx *TaskContext,
	plan *TaskPlan,
	rlState *rl.RLState,
	collector *EpisodeCollector,
) Observation {
	obs := Observation{
		SubTaskID: subtask.ID,
		ToolName:  subtask.ToolName,
		Input:     subtask.ToolInput,
	}

	// If no tool, mark as skipped (direct-reply subtask — shouldn't reach ACT)
	if subtask.ToolName == "" {
		subtask.Status = SubTaskDone
		obs.Output = subtask.Description
		return obs
	}

	t, err := e.tools.Get(subtask.ToolName)
	if err != nil {
		subtask.Status = SubTaskFailed
		obs.Error = "tool not found: " + subtask.ToolName
		markDownstreamSkipped(subtask.ID, obs.Error, allTasks)
		return obs
	}

	// ── Cache lookup for read-only tools ─────────────────────────────────────
	if e.cache != nil && tool.IsToolReadOnly(t) {
		if cached, ok := e.cache.Get(subtask.ToolName, subtask.ToolInput); ok {
			subtask.Status = SubTaskDone
			obs.Output = cached.Output
			slog.Info("subtask cache hit", "id", subtask.ID, "tool", subtask.ToolName)
			return obs
		}
	}

	// Track permission decision metadata
	var permAction, permReason, permRule string

	// ── PreToolUse hooks ──────────────────────────────────────────────────────
	skipApproval := false
	if e.hookMgr != nil && e.hookMgr.HasPreToolUseHandlers() {
		caps := tool.GetCapabilities(t)
		hookResult, hookErr := e.hookMgr.FirePreToolUse(ctx, hook.PreToolUseEvent{
			ToolName: subtask.ToolName,
			Input:    subtask.ToolInput,
			Capabilities: map[string]bool{
				"is_read_only":   caps.IsReadOnly,
				"is_destructive": caps.IsDestructive,
			},
		})
		if hookErr == nil {
			switch hookResult.Action {
			case "deny":
				subtask.Status = SubTaskFailed
				obs.Denied = true
				obs.Error = "denied by hook: " + hookResult.Reason
				session.LogToolExecution(ctx, e.db, sess.ID, subtask.ToolName, subtask.ToolInput, obs.Error, "denied", 0)
				e.recordBanditExperience(ctx, rlState, collector, subtask, &obs)
				return obs
			case "allow":
				skipApproval = true
				permAction = "allow"
				permReason = "hook_allow"
			}
		}
	}

	// ── Permission engine check ───────────────────────────────────────────────
	if e.permEngine != nil {
		caps := tool.GetCapabilities(t)
		permResult := e.permEngine.Evaluate(subtask.ToolName, subtask.ToolInput, caps)
		switch permResult.Action {
		case tool.PermissionDeny:
			subtask.Status = SubTaskFailed
			obs.Denied = true
			obs.Error = "denied by permission engine: " + permResult.Reason
			session.LogToolExecution(ctx, e.db, sess.ID, subtask.ToolName, subtask.ToolInput, obs.Error, "denied", 0)
			e.recordBanditExperience(ctx, rlState, collector, subtask, &obs)
			return obs
		case tool.PermissionAllow:
			skipApproval = true
			permAction = "allow"
			permReason = "rule_match"
			permRule = permResult.Reason
		}
	}

	// ── Approval check ────────────────────────────────────────────────────────
	if !skipApproval && t.RequiresApproval() && e.approvalFunc != nil {
		approved, err := e.approvalFunc(ctx, ch, target, subtask.ToolName, subtask.ToolInput)
		if err != nil || !approved {
			subtask.Status = SubTaskFailed
			obs.Denied = true
			obs.Error = "tool execution denied by user"
			session.LogToolExecution(ctx, e.db, sess.ID, subtask.ToolName, subtask.ToolInput, "", "denied", 0)
			e.recordBanditExperience(ctx, rlState, collector, subtask, &obs)
			return obs
		}
		permAction = "ask_approved"
		permReason = "user_approval"
	} else if !skipApproval && permAction == "" {
		permAction = "allow"
		permReason = "no_approval_required"
	}

	// If this is an agent_* tool and we have a TaskContext, inject predecessor outputs
	toolInput := subtask.ToolInput
	if taskCtx != nil && strings.HasPrefix(subtask.ToolName, "agent_") && len(subtask.DependsOn) > 0 {
		contextStr := taskCtx.BuildContextForTask(subtask.ID, plan)
		if contextStr != "" {
			// Parse existing tool input and inject context
			var input agentToolInput
			if err := json.Unmarshal([]byte(subtask.ToolInput), &input); err == nil {
				input.Context = contextStr
				if newInput, err := json.Marshal(input); err == nil {
					toolInput = string(newInput)
				}
			}
		}
	}

	// Execute
	start := time.Now()
	result, execErr := t.Execute(ctx, []byte(toolInput))
	durationMs := time.Since(start).Milliseconds()
	obs.DurationMs = durationMs
	obs.Metadata = result.Metadata

	// Determine execution status
	execStatus := "success"
	var execErrStr string
	if execErr != nil {
		subtask.Status = SubTaskFailed
		obs.Error = execErr.Error()
		execStatus = "error"
		execErrStr = execErr.Error()
		session.LogToolExecution(ctx, e.db, sess.ID, subtask.ToolName, subtask.ToolInput, obs.Error, "error", durationMs)
		slog.Info("subtask failed", "id", subtask.ID, "tool", subtask.ToolName, "err", execErr)
	} else if result.Error != "" {
		subtask.Status = SubTaskFailed
		obs.Error = result.Error
		execStatus = "error"
		execErrStr = result.Error
		session.LogToolExecution(ctx, e.db, sess.ID, subtask.ToolName, subtask.ToolInput, obs.Error, "error", durationMs)
		slog.Info("subtask failed", "id", subtask.ID, "tool", subtask.ToolName, "result_err", result.Error)
	} else {
		subtask.Status = SubTaskDone
		obs.Output = result.Output
	}

	// ── PostToolUse hooks (fires on all outcomes; may modify output) ───────────
	if e.hookMgr != nil && e.hookMgr.HasPostToolUseHandlers() {
		hookOutput := obs.Output
		if execStatus == "error" {
			hookOutput = obs.Error
		}
		postResult, _ := e.hookMgr.FirePostToolUse(ctx, hook.PostToolUseEvent{
			ToolName:         subtask.ToolName,
			Input:            subtask.ToolInput,
			Output:           hookOutput,
			Error:            execErrStr,
			Status:           execStatus,
			DurationMs:       durationMs,
			SessionID:        sess.ID,
			PermissionAction: permAction,
			PermissionReason: permReason,
			PermissionRule:   permRule,
		})
		if postResult.ModifiedOutput != nil && execStatus == "success" {
			obs.Output = *postResult.ModifiedOutput
		}
	}

	// Early return on execution failure (after PostToolUse hooks have fired)
	if execStatus == "error" {
		e.recordBanditExperience(ctx, rlState, collector, subtask, &obs)
		return obs
	}

	// ── Cache update ─────────────────────────────────────────────────────────
	if e.cache != nil {
		if tool.IsToolReadOnly(t) {
			e.cache.Put(subtask.ToolName, subtask.ToolInput, result)
		} else if pst, ok := t.(tool.PathScopedTool); ok {
			if paths, pathErr := pst.ExtractPaths([]byte(subtask.ToolInput)); pathErr == nil {
				for _, p := range paths {
					e.cache.InvalidatePath(p)
				}
			}
		}
	}

	// Store result in TaskContext if available
	if taskCtx != nil && strings.HasPrefix(subtask.ToolName, "agent_") {
		taskCtx.SetResult(subtask.ID, SubAgentResult{
			AgentName:  subtask.ToolName,
			Output:     result.Output,
			Error:      "",
			DurationMs: durationMs,
		})
	}

	// Record in session history
	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("tool_use_%s_%d", subtask.ID, time.Now().UnixNano()),
		Role:      "tool_use",
		ToolName:  subtask.ToolName,
		ToolInput: subtask.ToolInput,
		CreatedAt: time.Now(),
	})
	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("tool_result_%s_%d", subtask.ID, time.Now().UnixNano()),
		Role:      "tool_result",
		Content:   result.Output,
		ToolName:  fmt.Sprintf("tool_use_%s_%d", subtask.ID, time.Now().UnixNano()-1),
		CreatedAt: time.Now(),
	})

	session.LogToolExecution(ctx, e.db, sess.ID, subtask.ToolName, subtask.ToolInput, result.Output, "success", durationMs)
	slog.Info("subtask done", "id", subtask.ID, "tool", subtask.ToolName, "duration_ms", durationMs)

	// RL: record bandit experience after successful execution
	e.recordBanditExperience(ctx, rlState, collector, subtask, &obs)

	return obs
}

// collectReady returns pending tasks whose all dependencies are Done.
func collectReady(tasks []*SubTask, index map[string]*SubTask) []*SubTask {
	var ready []*SubTask
	for _, st := range tasks {
		if st.Status != SubTaskPending {
			continue
		}
		depsOK := true
		for _, depID := range st.DependsOn {
			dep, ok := index[depID]
			if !ok || dep.Status != SubTaskDone {
				depsOK = false
				break
			}
		}
		if depsOK {
			ready = append(ready, st)
		}
	}
	return ready
}

// markDownstreamSkipped marks all tasks that directly/indirectly depend on failedID as Skipped.
func markDownstreamSkipped(failedID string, reason string, allTasks []*SubTask) {
	skipped := map[string]bool{failedID: true}
	changed := true
	for changed {
		changed = false
		for _, st := range allTasks {
			if st.Status != SubTaskPending {
				continue
			}
			for _, dep := range st.DependsOn {
				if skipped[dep] {
					st.Status = SubTaskSkipped
					skipped[st.ID] = true
					changed = true
					slog.Info("subtask skipped due to upstream failure",
						"id", st.ID, "failed_dep", failedID, "reason", reason)
					break
				}
			}
		}
	}
}

// sendProgress streams a progress update to the channel (non-fatal on error).
func sendProgress(ctx context.Context, ch channel.Channel, target channel.MessageTarget, msg string) {
	if ch == nil {
		return
	}
	_ = ch.Send(ctx, channel.OutboundMessage{
		Channel:   target.Channel,
		ChannelID: target.ChannelID,
		Text:      msg,
	})
}

func statusEmoji(s SubTaskStatus) string {
	switch s {
	case SubTaskDone:
		return "done"
	case SubTaskFailed:
		return "failed"
	case SubTaskSkipped:
		return "skipped"
	default:
		return ""
	}
}

// recordBanditExperience computes tool reward and records a bandit experience.
func (e *Executor) recordBanditExperience(
	ctx context.Context,
	rlState *rl.RLState,
	collector *EpisodeCollector,
	subtask *SubTask,
	obs *Observation,
) {
	if e.rlPolicy == nil || !e.rlPolicy.IsEnabled() || rlState == nil {
		return
	}

	succeeded := subtask.Status == SubTaskDone
	reward := rl.ComputeToolReward(succeeded, obs.Denied, obs.DurationMs)

	// Update bandit arm statistics
	if err := e.rlPolicy.UpdateToolSelection(ctx, rlState, subtask.ToolName, reward); err != nil {
		slog.Warn("act: bandit update failed", "tool", subtask.ToolName, "err", err)
	}

	// Record experience in the episode collector
	if collector != nil {
		// Compute a tool index for the action vector (used for training, not selection)
		toolIdx := 0.0
		for i, t := range e.tools.All() {
			if t.Name() == subtask.ToolName {
				toolIdx = float64(i)
				break
			}
		}
		collector.Add(rl.Experience{
			State:     rlState,
			Action:    []float64{toolIdx},
			Reward:    reward,
			NextState: rlState, // same state within episode
			Done:      false,
			Level:     rl.LevelBandit,
		})
	}
}
