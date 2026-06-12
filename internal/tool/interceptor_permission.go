package tool

import (
	"context"
	"fmt"
	"strings"
)

// ToolNotifier sends non-blocking notifications when tools execute.
type ToolNotifier interface {
	NotifyToolExecution(ctx context.Context, call *ToolCall) error
}

// ToolApprover blocks until the user approves or denies a tool execution.
type ToolApprover interface {
	RequestApproval(ctx context.Context, call *ToolCall) (approved bool, err error)
}

type PermissionAuditSink interface {
	InsertAuditLog(ctx context.Context, sessionID, toolName, inputSummary, action, matchedRule, reason string) error
}

type PermissionDecisionRecord struct {
	SessionID    string
	ToolName     string
	Action       string
	Reason       string
	MatchedRule  string
	ChannelClass ToolChannelClass
}

type PermissionDecisionReporter interface {
	ReportPermissionDecision(ctx context.Context, record PermissionDecisionRecord)
}

// PermissionInterceptor checks permissions before tool execution.
type PermissionInterceptor struct {
	engine   *PermissionEngine
	notifier ToolNotifier
	approver ToolApprover
	audit    PermissionAuditSink
	reporter PermissionDecisionReporter
}

// PermissionInterceptorOption configures optional behaviour on a PermissionInterceptor.
type PermissionInterceptorOption func(*PermissionInterceptor)

// WithNotifier sets a notifier that fires non-blocking notifications on tool use.
func WithNotifier(n ToolNotifier) PermissionInterceptorOption {
	return func(p *PermissionInterceptor) { p.notifier = n }
}

// WithApprover sets an approver that blocks until the user approves or denies.
func WithApprover(a ToolApprover) PermissionInterceptorOption {
	return func(p *PermissionInterceptor) { p.approver = a }
}

func WithPermissionAuditSink(a PermissionAuditSink) PermissionInterceptorOption {
	return func(p *PermissionInterceptor) { p.audit = a }
}

func WithPermissionDecisionReporter(r PermissionDecisionReporter) PermissionInterceptorOption {
	return func(p *PermissionInterceptor) { p.reporter = r }
}

// NewPermissionInterceptor creates a PermissionInterceptor. Use WithNotifier and
// WithApprover options for optional behaviour; callers that don't need them
// simply omit the option.
func NewPermissionInterceptor(engine *PermissionEngine, opts ...PermissionInterceptorOption) *PermissionInterceptor {
	p := &PermissionInterceptor{engine: engine}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *PermissionInterceptor) Name() string { return "permission" }

// Intercept evaluates the tool's permissions against the policy engine.
func (p *PermissionInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if p.engine == nil {
		return next(ctx, call)
	}

	result := p.engine.EvaluateWithContext(ctx, call.ToolName, call.Input, call.Capabilities)
	action := result.Action
	p.auditDecision(ctx, call, result, string(action))
	p.reportDecision(ctx, call, result, string(action))

	switch action {
	case PermissionNone:
		return next(ctx, call)
	case PermissionNotify:
		if p.notifier != nil {
			_ = p.notifier.NotifyToolExecution(ctx, call)
		}
		return next(ctx, call)
	case PermissionApprove:
		if p.approver == nil {
			p.auditDecision(ctx, call, result, "denied")
			p.reportDecision(ctx, call, result, "denied")
			return &ToolResult{Error: "approval required but no approver configured"}, nil
		}
		if p.approver != nil {
			approved, err := p.approver.RequestApproval(ctx, call)
			if err != nil || !approved {
				p.auditDecision(ctx, call, result, "denied")
				p.reportDecision(ctx, call, result, "denied")
				return &ToolResult{Error: "execution denied by user"}, nil
			}
			p.auditDecision(ctx, call, result, "approved")
			p.reportDecision(ctx, call, result, "approved")
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

func (p *PermissionInterceptor) auditDecision(ctx context.Context, call *ToolCall, result PermissionResult, action string) {
	if p.audit == nil || call == nil {
		return
	}
	matchedRule := ""
	if result.MatchedRule != nil {
		matchedRule = fmt.Sprintf("tool=%s pattern=%s path_pattern=%s action=%s",
			result.MatchedRule.Tool, result.MatchedRule.Pattern, result.MatchedRule.PathPattern, result.MatchedRule.Action)
	}
	inputSummary := fmt.Sprintf("hash=%s len=%d channel_class=%s", hashInput(call.Input), len(call.Input), ChannelClassFromContext(ctx))
	reason := strings.TrimSpace(result.Reason)
	_ = p.audit.InsertAuditLog(ctx, call.SessionID, call.ToolName, inputSummary, action, matchedRule, reason)
}

func (p *PermissionInterceptor) reportDecision(ctx context.Context, call *ToolCall, result PermissionResult, action string) {
	if p.reporter == nil || call == nil {
		return
	}
	matchedRule := ""
	if result.MatchedRule != nil {
		matchedRule = fmt.Sprintf("tool=%s pattern=%s path_pattern=%s action=%s",
			result.MatchedRule.Tool, result.MatchedRule.Pattern, result.MatchedRule.PathPattern, result.MatchedRule.Action)
	}
	p.reporter.ReportPermissionDecision(ctx, PermissionDecisionRecord{
		SessionID:    call.SessionID,
		ToolName:     call.ToolName,
		Action:       action,
		Reason:       strings.TrimSpace(result.Reason),
		MatchedRule:  matchedRule,
		ChannelClass: ChannelClassFromContext(ctx),
	})
}
