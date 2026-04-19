package agent

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/tool"
	"golang.org/x/sync/errgroup"
)

// toolResult holds the result of a single tool execution.
type toolResult struct {
	toolUseID        string
	output           string
	status           string
	duration         int64
	toolName         string
	toolInput        string
	permissionAction string
	permissionReason string
	permissionRule   string
}

// executeTools executes a batch of tool calls, using concurrent execution for
// safe tools when enabled. Tools are partitioned by ParallelSafety level:
//   - ParallelSafe: run concurrently with all other safe tools
//   - ParallelPathScoped: run concurrently unless sharing a resource path
//   - ParallelNever: always run sequentially
func (r *Runtime) executeTools(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	toolCalls []ToolUseBlock,
) {
	// Single tool or concurrency disabled — sequential execution
	if len(toolCalls) <= 1 || !r.concurrentCfg.Enabled {
		for _, tc := range toolCalls {
			r.executeSingleTool(ctx, ch, sess, target, tc)
		}
		return
	}

	// Partition tools by parallel safety level
	var concurrent []ToolUseBlock
	var sequential []ToolUseBlock

	// Track paths claimed by path-scoped tools to detect conflicts
	claimedPaths := make(map[string]bool)

	for _, tc := range toolCalls {
		t, err := r.tools.Get(tc.Name)
		if err != nil {
			r.addToolResult(sess, tc.ID, "tool not found: "+tc.Name)
			continue
		}

		caps := tool.GetCapabilities(t)
		switch caps.ParallelSafety {
		case tool.ParallelSafe:
			concurrent = append(concurrent, tc)

		case tool.ParallelPathScoped:
			// Check for path conflicts with already-scheduled tools
			if pst, ok := t.(tool.PathScopedTool); ok {
				paths, err := pst.ExtractPaths([]byte(tc.Input))
				if err != nil || len(paths) == 0 {
					// Cannot determine paths — fall back to sequential
					sequential = append(sequential, tc)
					continue
				}
				hasConflict := false
				for _, p := range paths {
					if claimedPaths[p] {
						hasConflict = true
						break
					}
				}
				if hasConflict {
					sequential = append(sequential, tc)
				} else {
					for _, p := range paths {
						claimedPaths[p] = true
					}
					concurrent = append(concurrent, tc)
				}
			} else {
				// Declared path_scoped but doesn't implement PathScopedTool — be safe
				sequential = append(sequential, tc)
			}

		default: // ParallelNever
			sequential = append(sequential, tc)
		}
	}

	// Execute concurrent tools in parallel
	if len(concurrent) > 0 {
		maxConcurrency := r.concurrentCfg.MaxConcurrency
		if maxConcurrency <= 0 {
			maxConcurrency = 4
		}

		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(maxConcurrency)

		var mu sync.Mutex
		results := make([]toolResult, len(concurrent))

		for i, tc := range concurrent {
			i, tc := i, tc
			g.Go(func() error {
				res := r.executeToolCall(gctx, ch, sess, target, tc)
				mu.Lock()
				results[i] = res
				mu.Unlock()
				return nil // don't propagate errors — handle per-tool
			})
		}
		_ = g.Wait()

		// Apply results in original order
		for i, tc := range concurrent {
			res := results[i]
			session.LogToolExecution(ctx, r.db, sess.ID, res.toolName, res.toolInput, res.output, res.status, res.duration)
			r.addToolResult(sess, tc.ID, res.output)
			slog.Info("tool executed (concurrent)", "tool", res.toolName, "status", res.status, "duration_ms", res.duration)
		}
	}

	// Execute sequential tools one at a time
	for _, tc := range sequential {
		r.executeSingleTool(ctx, ch, sess, target, tc)
	}
}

// executeToolCall executes a single tool and returns the result without adding it to the session.
// Used by concurrent execution to collect results before applying them in order.
func (r *Runtime) executeToolCall(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	tc ToolUseBlock,
) toolResult {
	t, err := r.tools.Get(tc.Name)
	if err != nil {
		return toolResult{toolUseID: tc.ID, output: err.Error(), status: "error", toolName: tc.Name, toolInput: tc.Input}
	}

	// Check for a speculative execution result (read-only tools launched during streaming)
	if r.speculativeExecutor != nil {
		if specResult, specErr := r.speculativeExecutor.Collect(tc.ID); specResult != nil {
			var output, status string
			if specErr != nil {
				output = "error: " + specErr.Error()
				status = "error"
			} else if specResult.Error != "" {
				output = "error: " + specResult.Error
				status = "error"
			} else {
				output = specResult.Output
				status = "success"
			}
			slog.Info("speculative result used", "tool", tc.Name, "status", status)
			return toolResult{
				toolUseID: tc.ID, output: output, status: status,
				toolName: tc.Name, toolInput: tc.Input,
				permissionAction: "allow", permissionReason: "speculative_readonly",
			}
		}
	}

	// Track permission decision metadata
	var permAction, permReason, permRule string

	// Fire PreToolUse hooks
	skipApproval := false
	if r.hookMgr != nil && r.hookMgr.HasPreToolUseHandlers() {
		caps := tool.GetCapabilities(t)
		hookResult, hookErr := r.hookMgr.FirePreToolUse(ctx, hook.PreToolUseEvent{
			ToolName: tc.Name,
			Input:    tc.Input,
			Capabilities: map[string]bool{
				"is_read_only":  caps.IsReadOnly,
				"is_destructive": caps.IsDestructive,
			},
		})
		if hookErr == nil {
			switch hookResult.Action {
			case "deny":
				return toolResult{toolUseID: tc.ID, output: "denied by hook: " + hookResult.Reason, status: "denied", toolName: tc.Name, toolInput: tc.Input,
					permissionAction: "deny", permissionReason: "hook_deny", permissionRule: hookResult.Reason}
			case "allow":
				skipApproval = true
				permAction = "allow"
				permReason = "hook_allow"
			}
		}
	}

	// Permission engine check
	if r.permEngine != nil {
		caps := tool.GetCapabilities(t)
		permResult := r.permEngine.Evaluate(tc.Name, tc.Input, caps)
		switch permResult.Action {
		case tool.PermissionDeny:
			return toolResult{toolUseID: tc.ID, output: "denied by permission engine: " + permResult.Reason, status: "denied", toolName: tc.Name, toolInput: tc.Input,
				permissionAction: "deny", permissionReason: "rule_match", permissionRule: permResult.Reason}
		case tool.PermissionAllow:
			skipApproval = true
			permAction = "allow"
			permReason = "rule_match"
			permRule = permResult.Reason
		}
	}

	// Approval (serialized via channel interaction for concurrent calls)
	if !skipApproval && t.RequiresApproval() && r.approvalFunc != nil {
		approved, err := r.approvalFunc(ctx, ch, target, tc.Name, tc.Input)
		if err != nil || !approved {
			return toolResult{toolUseID: tc.ID, output: "tool execution denied by user", status: "denied", toolName: tc.Name, toolInput: tc.Input,
				permissionAction: "ask_denied", permissionReason: "user_denial"}
		}
		permAction = "ask_approved"
		permReason = "user_approval"
	} else if !skipApproval {
		// No approval needed — auto-allow
		if permAction == "" {
			permAction = "allow"
			permReason = "no_approval_required"
		}
	}

	if r.dashEmitter != nil {
		r.dashEmitter.EmitToolStart(sess.ID, tc.Name, tc.Input)
	}
	start := time.Now()
	result, err := t.Execute(ctx, []byte(tc.Input))
	duration := time.Since(start).Milliseconds()

	var output string
	status := "success"
	if err != nil {
		output = "error: " + err.Error()
		status = "error"
	} else if result.Error != "" {
		output = "error: " + result.Error
		status = "error"
	} else {
		output = result.Output
		// Persist large results to disk if enabled
		if r.resultStore != nil && r.resultStore.ShouldPersist(output) {
			stored, storeErr := r.resultStore.Store(sess.ID, tc.ID, output)
			if storeErr != nil {
				slog.Warn("failed to persist tool result", "tool", tc.Name, "err", storeErr)
			} else {
				output = stored.Preview
			}
		}
		// Compress long tool outputs
		if r.compressor != nil {
			output = r.compressor.CompressToolResult(output)
		}
	}

	// Fire PostToolUse hooks
	if r.hookMgr != nil && r.hookMgr.HasPostToolUseHandlers() {
		var errStr string
		if err != nil {
			errStr = err.Error()
		} else if result.Error != "" {
			errStr = result.Error
		}
		postResult, _ := r.hookMgr.FirePostToolUse(ctx, hook.PostToolUseEvent{
			ToolName:         tc.Name,
			Input:            tc.Input,
			Output:           output,
			Error:            errStr,
			Status:           status,
			DurationMs:       duration,
			SessionID:        sess.ID,
			PermissionAction: permAction,
			PermissionReason: permReason,
			PermissionRule:   permRule,
		})
		if postResult.ModifiedOutput != nil {
			output = *postResult.ModifiedOutput
		}
	}

	if r.dashEmitter != nil {
		r.dashEmitter.EmitToolEnd(sess.ID, tc.Name, status == "success", duration)
	}
	return toolResult{toolUseID: tc.ID, output: output, status: status, duration: duration, toolName: tc.Name, toolInput: tc.Input,
		permissionAction: permAction, permissionReason: permReason, permissionRule: permRule}
}

// executeSingleTool executes a tool and immediately adds the result to the session.
func (r *Runtime) executeSingleTool(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	tc ToolUseBlock,
) {
	res := r.executeToolCall(ctx, ch, sess, target, tc)
	session.LogToolExecution(ctx, r.db, sess.ID, res.toolName, res.toolInput, res.output, res.status, res.duration)
	r.addToolResult(sess, tc.ID, res.output)
	slog.Info("tool executed", "tool", res.toolName, "status", res.status, "duration_ms", res.duration)
}
