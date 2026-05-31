package agent

import (
	"context"
	"fmt"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// HookMiddleware wraps tool execution with pre/post tool hook dispatch.
type HookMiddleware struct {
	hookMgr *hook.Manager
}

// NewHookMiddleware creates a middleware that fires hook events around tool execution.
func NewHookMiddleware(hm *hook.Manager) *HookMiddleware {
	return &HookMiddleware{hookMgr: hm}
}

// Wrap returns a ToolExecutor that fires PreToolUse before and PostToolUse after.
func (m *HookMiddleware) Wrap(next ToolExecutor) ToolExecutor {
	return func(ctx context.Context, call ToolCall) (*ToolResult, error) {
		if m.hookMgr != nil {
			event := hook.PreToolUseEvent{
				ToolName: call.Name,
				Input:    call.Input,
			}
			if _, err := m.hookMgr.FirePreToolUse(ctx, event); err != nil {
				return nil, fmt.Errorf("pre_tool_use hook: %w", err)
			}
		}
		result, err := next(ctx, call)
		if m.hookMgr != nil {
			status := "success"
			if err != nil || (result != nil && result.IsError) {
				status = "error"
			}
			output := ""
			if result != nil {
				output = result.Content
			}
			durationMs := int64(0)
			if result != nil {
				durationMs = result.Duration.Milliseconds()
			}
			postEvent := hook.PostToolUseEvent{
				ToolName:   call.Name,
				Input:      call.Input,
				Output:     output,
				DurationMs: durationMs,
				Status:     status,
			}
			if err != nil {
				postEvent.Error = err.Error()
			}
			_, _ = m.hookMgr.FirePostToolUse(ctx, postEvent)
		}
		return result, err
	}
}

// PermissionMiddleware enforces tool permission policies before execution.
type PermissionMiddleware struct {
	permEngine   *tool.PermissionEngine
	approvalFunc ApprovalFunc
}

// NewPermissionMiddleware creates a middleware that checks permissions before tool execution.
func NewPermissionMiddleware(pe *tool.PermissionEngine, af ApprovalFunc) *PermissionMiddleware {
	return &PermissionMiddleware{permEngine: pe, approvalFunc: af}
}

// Wrap returns a ToolExecutor that evaluates permissions before delegating.
func (m *PermissionMiddleware) Wrap(next ToolExecutor) ToolExecutor {
	return func(ctx context.Context, call ToolCall) (*ToolResult, error) {
		if m.permEngine != nil {
			result := m.permEngine.Evaluate(call.Name, call.Input, tool.ToolCapabilities{})
			switch result.Action {
			case tool.PermissionDeny:
				return nil, fmt.Errorf("tool %s: permission denied", call.Name)
			case tool.PermissionApprove:
				if m.approvalFunc != nil {
					approved, err := m.approvalFunc(ctx, nil, channel.MessageTarget{}, call.Name, call.Input)
					if err != nil || !approved {
						return nil, fmt.Errorf("tool %s: approval denied", call.Name)
					}
				}
			}
		}
		return next(ctx, call)
	}
}

// SandboxMiddleware dispatches tool execution through the security sandbox interceptor chain.
type SandboxMiddleware struct {
	interceptorChain *tool.InterceptorChain
}

// NewSandboxMiddleware creates a middleware that routes through the sandbox interceptor chain.
func NewSandboxMiddleware(ic *tool.InterceptorChain) *SandboxMiddleware {
	return &SandboxMiddleware{interceptorChain: ic}
}

// Wrap returns a ToolExecutor that delegates to the interceptor chain when available.
func (m *SandboxMiddleware) Wrap(next ToolExecutor) ToolExecutor {
	return func(ctx context.Context, call ToolCall) (*ToolResult, error) {
		if m.interceptorChain != nil {
			icCall := &tool.ToolCall{
				ToolName: call.Name,
				Input:    call.Input,
			}
			icResult, err := m.interceptorChain.Execute(ctx, icCall, func(ctx context.Context, tcall *tool.ToolCall) (*tool.ToolResult, error) {
				res, err := next(ctx, call)
				if err != nil {
					return nil, err
				}
				ierr := ""
				if res.IsError {
					ierr = res.Content
				}
				return &tool.ToolResult{
					Output: res.Content,
					Error:  ierr,
				}, nil
			})
			if err != nil {
				return nil, err
			}
			if icResult == nil {
				return nil, nil
			}
			return &ToolResult{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    icResult.Output,
				IsError:    icResult.Error != "",
			}, nil
		}
		return next(ctx, call)
	}
}
