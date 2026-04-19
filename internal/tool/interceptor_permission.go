package tool

import (
	"context"
	"fmt"
)

// ToolNotifier sends non-blocking notifications when tools execute.
type ToolNotifier interface {
	NotifyToolExecution(ctx context.Context, call *ToolCall) error
}

// ToolApprover blocks until the user approves or denies a tool execution.
type ToolApprover interface {
	RequestApproval(ctx context.Context, call *ToolCall) (approved bool, err error)
}

// PermissionInterceptor checks permissions before tool execution.
type PermissionInterceptor struct {
	engine   *PermissionEngine
	notifier ToolNotifier
	approver ToolApprover
}

func NewPermissionInterceptor(engine *PermissionEngine, notifier ToolNotifier, approver ToolApprover) *PermissionInterceptor {
	return &PermissionInterceptor{engine: engine, notifier: notifier, approver: approver}
}

func (p *PermissionInterceptor) Name() string { return "permission" }

func (p *PermissionInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if p.engine == nil {
		return next(ctx, call)
	}

	result := p.engine.Evaluate(call.ToolName, call.Input, ToolCapabilities{})

	switch result.Action {
	case PermissionNone:
		return next(ctx, call)
	case PermissionNotify:
		if p.notifier != nil {
			_ = p.notifier.NotifyToolExecution(ctx, call)
		}
		return next(ctx, call)
	case PermissionApprove:
		if p.approver != nil {
			approved, err := p.approver.RequestApproval(ctx, call)
			if err != nil || !approved {
				return &ToolResult{Error: "execution denied by user"}, nil
			}
		}
		return next(ctx, call)
	case PermissionDeny:
		reason := result.Reason
		if reason == "" {
			reason = "policy"
		}
		return &ToolResult{Error: fmt.Sprintf("tool %s denied by %s", call.ToolName, reason)}, nil
	}

	return next(ctx, call)
}
