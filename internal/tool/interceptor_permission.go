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
	tracker  *TrustTracker
}

func NewPermissionInterceptor(engine *PermissionEngine, notifier ToolNotifier, approver ToolApprover, tracker *TrustTracker) *PermissionInterceptor {
	return &PermissionInterceptor{engine: engine, notifier: notifier, approver: approver, tracker: tracker}
}

func (p *PermissionInterceptor) Name() string { return "permission" }

// Intercept evaluates the tool's permissions by first checking the engine policy
// and then applying TrustTracker-based relaxation (accumulated trust can only
// make checks less restrictive, never more).
func (p *PermissionInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if p.engine == nil {
		return next(ctx, call)
	}

	result := p.engine.Evaluate(call.ToolName, call.Input, ToolCapabilities{})
	action := result.Action

	// Apply trust-based relaxation: accumulated trust can never override a
	// hard Deny, but can relax Approve → Notify → None.
	if p.tracker != nil && action == PermissionApprove {
		trustAction := TrustLevelToPermission(p.tracker.SuggestedLevel(call.ToolName))
		if trustAction == PermissionNotify || trustAction == PermissionNone {
			action = trustAction
		}
	}

	switch action {
	case PermissionNone:
		_ = p.recordIfTracking(call.ToolName, true)
		return next(ctx, call)
	case PermissionNotify:
		if p.notifier != nil {
			_ = p.notifier.NotifyToolExecution(ctx, call)
		}
		_ = p.recordIfTracking(call.ToolName, true)
		return next(ctx, call)
	case PermissionApprove:
		if p.approver != nil {
			approved, err := p.approver.RequestApproval(ctx, call)
			if err != nil || !approved {
				_ = p.recordIfTracking(call.ToolName, false)
				return &ToolResult{Error: "execution denied by user"}, nil
			}
		}
		_ = p.recordIfTracking(call.ToolName, true)
		return next(ctx, call)
	case PermissionDeny:
		reason := result.Reason
		if reason == "" {
			reason = "policy"
		}
		_ = p.recordIfTracking(call.ToolName, false)
		return &ToolResult{Error: fmt.Sprintf("tool %s denied by %s", call.ToolName, reason)}, nil
	}

	return next(ctx, call)
}

func (p *PermissionInterceptor) recordIfTracking(toolName string, approved bool) error {
	if p.tracker == nil {
		return nil
	}
	if approved {
		p.tracker.RecordApproval(toolName)
	} else {
		p.tracker.RecordRejection(toolName)
	}
	return nil
}
