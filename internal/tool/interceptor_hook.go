package tool

import (
	"context"

	"github.com/Forest-Isle/IronClaw/internal/hook"
)

type HookInterceptor struct {
	hookMgr *hook.Manager
}

func NewHookInterceptor(hookMgr *hook.Manager) *HookInterceptor {
	return &HookInterceptor{hookMgr: hookMgr}
}

func (h *HookInterceptor) Name() string { return "hook" }

func (h *HookInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if h.hookMgr == nil || !h.hookMgr.HasPreToolUseHandlers() {
		return next(ctx, call)
	}

	hookResult, hookErr := h.hookMgr.FirePreToolUse(ctx, hook.PreToolUseEvent{
		ToolName: call.ToolName,
		Input:    call.Input,
	})

	if hookErr == nil {
		switch hookResult.Action {
		case "deny":
			return &ToolResult{Error: "denied by hook: " + hookResult.Reason}, nil
		case "allow":
			call.HookApproved = true
		}
	}

	return next(ctx, call)
}
