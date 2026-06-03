package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// Executor implements the ACT phase: topological scheduling + parallel execution.
type Executor struct {
	tools            *tool.Registry
	db               *store.DB
	approvalFunc     ApprovalFunc
	cfg              config.CognitiveConfig
	hookMgr          *hook.Manager
	permEngine       *tool.PermissionEngine
	interceptorChain *tool.InterceptorChain
	dashEmitter      ObservabilityEmitter
	planMode         *PlanMode // optional plan->approve->execute flow
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

// SetHookManager injects a hook manager for pre/post tool-use hooks.
func (e *Executor) SetHookManager(mgr *hook.Manager) {
	e.hookMgr = mgr
}

// SetPermissionEngine injects a permission engine for rule-based access control.
func (e *Executor) SetPermissionEngine(pe *tool.PermissionEngine) {
	e.permEngine = pe
}

// SetInterceptorChain attaches an interceptor chain for sandbox-aware tool execution.
func (e *Executor) SetInterceptorChain(chain *tool.InterceptorChain) {
	e.interceptorChain = chain
}

// SetObservabilityEmitter injects an observability event emitter for tool execution tracking.
func (e *Executor) SetObservabilityEmitter(em ObservabilityEmitter) {
	e.dashEmitter = em
}


// SetPlanMode injects a PlanMode instance for plan->approve->execute flow.
// When set, write tool executions must be approved through an active plan.
func (e *Executor) SetPlanMode(pm *PlanMode) {
	e.planMode = pm
}

// Run executes the ACT phase -- topological ordering + parallel execution.
func (e *Executor) Run(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	plan *TaskPlan,
) ([]Observation, error) {
	return e.RunWithContext(ctx, ch, sess, target, plan, nil)
}

// RunWithContext executes the ACT phase with an optional TaskContext for multi-agent collaboration.
// Uses channel-driven concurrent execution: workers pull tasks as they become ready
// (dependencies satisfied) rather than batch-await-batch. This eliminates idle time
// from straggler tasks blocking the next batch.
func (e *Executor) RunWithContext(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	plan *TaskPlan,
	taskCtx *TaskContext,
) ([]Observation, error) {
	maxParallel := e.cfg.MaxParallelTools
	if maxParallel <= 0 {
		maxParallel = 3
	}

	total := len(plan.SubTasks)
	if total == 0 {
		return nil, nil
	}

	var (
		observations []Observation
		obsMu        sync.Mutex
		doneCount    int32
	)

	// Build task index for dependency resolution
	taskIndex := make(map[string]*SubTask, total)
	for _, st := range plan.SubTasks {
		taskIndex[st.ID] = st
	}

	// readyCh feeds workers as task dependencies are satisfied.
	readyCh := make(chan *SubTask, total)
	// semaphore limits concurrent tool execution to maxParallel.
	sem := make(chan struct{}, maxParallel)

	// Seed initial ready tasks (no dependencies)
	seedReady(plan.SubTasks, readyCh)

	var wg sync.WaitGroup
	workerCount := maxParallel
	if workerCount > total {
		workerCount = total
	}
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case subtask, ok := <-readyCh:
					if !ok {
						return
					}
					// Acquire semaphore
					select {
					case sem <- struct{}{}:
					case <-ctx.Done():
						return
					}

					func() {
						defer func() {
							<-sem // Always release
							if r := recover(); r != nil {
								slog.Error("act: panic in DAG worker", "subtask", subtask.ID, "panic", r)
							}
						}()
						subtask.Status = SubTaskRunning
						obs := e.executeSubTask(ctx, ch, sess, target, subtask, plan.SubTasks, taskCtx, plan)

						obsMu.Lock()
						observations = append(observations, obs)
						n := int(atomic.AddInt32(&doneCount, 1))
						obsMu.Unlock()

						progressMsg := fmt.Sprintf("[%d/%d] %s... %s",
							n, total, subtask.Description, statusEmoji(subtask.Status))
						sendProgress(ctx, ch, target, progressMsg)

						// Feed newly-unblocked tasks to the ready channel
						feedReady(plan.SubTasks, readyCh, taskIndex)
					}()
				}
			}
		}()
	}

	wg.Wait()
	close(readyCh)

	return observations, nil
}

// seedReady pushes tasks with no unsatisfied dependencies into the ready channel.
func seedReady(tasks []*SubTask, readyCh chan<- *SubTask) {
	for _, st := range tasks {
		if st.Status != SubTaskPending {
			continue
		}
		if len(st.DependsOn) == 0 {
			readyCh <- st
		}
	}
}

// feedReady checks all pending tasks and pushes those whose dependencies
// are now satisfied into the ready channel.
func feedReady(tasks []*SubTask, readyCh chan<- *SubTask, taskIndex map[string]*SubTask) {
	for _, st := range tasks {
		if st.Status != SubTaskPending {
			continue
		}
		if len(st.DependsOn) == 0 {
			continue // already seeded
		}
		allSatisfied := true
		for _, depID := range st.DependsOn {
			dep, ok := taskIndex[depID]
			if !ok || (dep.Status != SubTaskDone && dep.Status != SubTaskSkipped) {
				allSatisfied = false
				break
			}
		}
		if allSatisfied {
			select {
			case readyCh <- st:
			default:
				// Channel buffer full — worker will pick up on next iteration
			}
		}
	}
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
) Observation {
	obs := Observation{
		SubTaskID: subtask.ID,
		ToolName:  subtask.ToolName,
		Input:     subtask.ToolInput,
	}

	// If no tool, mark as skipped (direct-reply subtask -- shouldn't reach ACT)
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

	// Route through interceptor chain when configured (e.g. sandbox enforcement)
	if e.interceptorChain != nil {
		return e.executeSubTaskViaChain(ctx, ch, sess, target, subtask, allTasks, taskCtx, plan, t)
	}

	// Track permission decision metadata
	var permAction, permReason, permRule string

	// -- PreToolUse hooks ------------------------------------------------------------
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
				return obs
			case "allow":
				skipApproval = true
				permAction = "allow"
				permReason = "hook_allow"
			}
		}
	}

	// -- Permission engine check -----------------------------------------------------
	if e.permEngine != nil {
		caps := tool.GetCapabilities(t)
		permResult := e.permEngine.Evaluate(subtask.ToolName, subtask.ToolInput, caps)
		switch permResult.Action {
		case tool.PermissionDeny:
			subtask.Status = SubTaskFailed
			obs.Denied = true
			obs.Error = "denied by permission engine: " + permResult.Reason
			session.LogToolExecution(ctx, e.db, sess.ID, subtask.ToolName, subtask.ToolInput, obs.Error, "denied", 0)
			return obs
		case tool.PermissionNone:
			skipApproval = true
			permAction = "allow"
			permReason = "rule_match"
			permRule = permResult.Reason
		}
	}

	// -- Approval check --------------------------------------------------------------
	if !skipApproval && t.RequiresApproval() && e.approvalFunc != nil {
		approved, err := e.approvalFunc(ctx, ch, target, subtask.ToolName, subtask.ToolInput)
		if err != nil || !approved {
			subtask.Status = SubTaskFailed
			obs.Denied = true
			obs.Error = "tool execution denied by user"
			session.LogToolExecution(ctx, e.db, sess.ID, subtask.ToolName, subtask.ToolInput, "", "denied", 0)
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
	if e.dashEmitter != nil {
		e.dashEmitter.EmitToolStart(sess.ID, subtask.ToolName, toolInput)
	}
	start := time.Now()
	result, execErr := t.Execute(ctx, []byte(toolInput))
	durationMs := time.Since(start).Milliseconds()
	obs.DurationMs = durationMs
	if e.dashEmitter != nil {
		e.dashEmitter.EmitToolEnd(sess.ID, subtask.ToolName, execErr == nil && result.Error == "", durationMs)
	}
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

	// -- PostToolUse hooks (fires on all outcomes; may modify output) ----------------
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
		return obs
	}

	// Store result in TaskContext if available
	if taskCtx != nil && strings.HasPrefix(subtask.ToolName, "agent_") {
		taskCtx.SetResult(subtask.ID, SubAgentResult{
			AgentName: subtask.ToolName,
			Output:    result.Output,
			Duration:  time.Duration(durationMs) * time.Millisecond,
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

	return obs
}

// executeSubTaskViaChain runs a subtask through the interceptor chain (e.g. sandbox policy).
// Preserves all post-execution behavior: TaskContext injection, session messages, logging,
// cache updates, and PostToolUse hooks.
func (e *Executor) executeSubTaskViaChain(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	subtask *SubTask,
	allTasks []*SubTask,
	taskCtx *TaskContext,
	plan *TaskPlan,
	t tool.Tool,
) Observation {
	obs := Observation{
		SubTaskID: subtask.ID,
		ToolName:  subtask.ToolName,
		Input:     subtask.ToolInput,
	}

	// Prepare tool input with TaskContext injection for agent_* tools
	toolInput := subtask.ToolInput
	if taskCtx != nil && strings.HasPrefix(subtask.ToolName, "agent_") && len(subtask.DependsOn) > 0 {
		contextStr := taskCtx.BuildContextForTask(subtask.ID, plan)
		if contextStr != "" {
			var input agentToolInput
			if err := json.Unmarshal([]byte(subtask.ToolInput), &input); err == nil {
				input.Context = contextStr
				if newInput, err := json.Marshal(input); err == nil {
					toolInput = string(newInput)
				}
			}
		}
	}

	call := &tool.ToolCall{
		ToolName:  subtask.ToolName,
		Input:     toolInput,
		SessionID: sess.ID,
	}

	if e.dashEmitter != nil {
		e.dashEmitter.EmitToolStart(sess.ID, subtask.ToolName, toolInput)
	}
	start := time.Now()
	res, err := e.interceptorChain.Execute(ctx, call, func(ctx context.Context, call *tool.ToolCall) (*tool.ToolResult, error) {
		result, execErr := t.Execute(ctx, []byte(call.Input))
		if execErr != nil {
			return &tool.ToolResult{Error: execErr.Error()}, nil
		}
		tr := &tool.ToolResult{Output: result.Output, Error: result.Error}
		if result.Metadata != nil {
			tr.Metadata = make(map[string]string, len(result.Metadata))
			for k, v := range result.Metadata {
				tr.Metadata[k] = fmt.Sprintf("%v", v)
			}
		}
		return tr, nil
	})
	durationMs := time.Since(start).Milliseconds()
	obs.DurationMs = durationMs

	if err != nil {
		if e.dashEmitter != nil {
			e.dashEmitter.EmitToolEnd(sess.ID, subtask.ToolName, false, durationMs)
		}
		subtask.Status = SubTaskFailed
		obs.Error = err.Error()
		session.LogToolExecution(ctx, e.db, sess.ID, subtask.ToolName, subtask.ToolInput, obs.Error, "error", durationMs)
		slog.Info("subtask failed (chain)", "id", subtask.ID, "tool", subtask.ToolName, "err", err)
		return obs
	}
	if res.Error != "" {
		if e.dashEmitter != nil {
			e.dashEmitter.EmitToolEnd(sess.ID, subtask.ToolName, false, durationMs)
		}
		subtask.Status = SubTaskFailed
		obs.Denied = true
		obs.Error = res.Error
		session.LogToolExecution(ctx, e.db, sess.ID, subtask.ToolName, subtask.ToolInput, obs.Error, "denied", durationMs)
		slog.Info("subtask denied (chain)", "id", subtask.ID, "tool", subtask.ToolName, "reason", res.Error)
		return obs
	}

	subtask.Status = SubTaskDone
	obs.Output = res.Output
	if res.Metadata != nil {
		md := make(map[string]any, len(res.Metadata))
		for k, v := range res.Metadata {
			md[k] = v
		}
		obs.Metadata = md
	}

	// PostToolUse hooks
	if e.hookMgr != nil && e.hookMgr.HasPostToolUseHandlers() {
		postResult, _ := e.hookMgr.FirePostToolUse(ctx, hook.PostToolUseEvent{
			ToolName:   subtask.ToolName,
			Input:      subtask.ToolInput,
			Output:     obs.Output,
			Status:     "success",
			DurationMs: durationMs,
			SessionID:  sess.ID,
		})
		if postResult.ModifiedOutput != nil {
			obs.Output = *postResult.ModifiedOutput
		}
	}

	// Store result in TaskContext for multi-agent collaboration
	if taskCtx != nil && strings.HasPrefix(subtask.ToolName, "agent_") {
		taskCtx.SetResult(subtask.ID, SubAgentResult{
			AgentName: subtask.ToolName,
			Output:    res.Output,
			Duration:  time.Duration(durationMs) * time.Millisecond,
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
		Content:   res.Output,
		ToolName:  fmt.Sprintf("tool_use_%s_%d", subtask.ID, time.Now().UnixNano()-1),
		CreatedAt: time.Now(),
	})

	if e.dashEmitter != nil {
		e.dashEmitter.EmitToolEnd(sess.ID, subtask.ToolName, true, durationMs)
	}

	session.LogToolExecution(ctx, e.db, sess.ID, subtask.ToolName, subtask.ToolInput, res.Output, "success", durationMs)
	slog.Info("subtask done (chain)", "id", subtask.ID, "tool", subtask.ToolName, "duration_ms", durationMs)

	return obs
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
	if err := ch.Send(ctx, channel.OutboundMessage{
		Channel:   target.Channel,
		ChannelID: target.ChannelID,
		Text:      msg,
	}); err != nil {
		slog.Warn("failed to send message", "err", err)
	}
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
