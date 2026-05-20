package core

import (
	"context"
	"encoding/json"
	"errors"
)

// Decision captures a permission gate's verdict for a tool call.
type Decision string

const (
	DecisionAllow   Decision = "allow"
	DecisionApprove Decision = "approve" // gate needs human approval
	DecisionDeny    Decision = "deny"
)

// PermissionRequest is what a Gate inspects.
type PermissionRequest struct {
	ToolName string
	Input    json.RawMessage
	ReadOnly bool
}

// Gate decides whether a tool may execute. Returning DecisionApprove
// instructs the runner to consult the Approver below.
type Gate interface {
	Inspect(ctx context.Context, req PermissionRequest) (Decision, string, error)
}

// GateFunc adapts a function to the Gate interface.
type GateFunc func(ctx context.Context, req PermissionRequest) (Decision, string, error)

func (f GateFunc) Inspect(ctx context.Context, req PermissionRequest) (Decision, string, error) {
	return f(ctx, req)
}

// Approver handles interactive human approvals when a Gate returns
// DecisionApprove. Implementations that operate non-interactively (e.g.
// "always yes" for CI) just return true.
type Approver interface {
	Approve(ctx context.Context, req PermissionRequest, reason string) (bool, error)
}

// ApproverFunc adapts a function to the Approver interface.
type ApproverFunc func(ctx context.Context, req PermissionRequest, reason string) (bool, error)

func (f ApproverFunc) Approve(ctx context.Context, req PermissionRequest, reason string) (bool, error) {
	return f(ctx, req, reason)
}

// AllowAllGate permits every request without prompting.
type AllowAllGate struct{}

func (AllowAllGate) Inspect(context.Context, PermissionRequest) (Decision, string, error) {
	return DecisionAllow, "", nil
}

// AutoApprover answers yes to every approval prompt.
type AutoApprover struct{}

func (AutoApprover) Approve(context.Context, PermissionRequest, string) (bool, error) {
	return true, nil
}

// ErrDenied is returned by the runner when a Gate denies a tool call.
var ErrDenied = errors.New("core: tool execution denied by policy")
