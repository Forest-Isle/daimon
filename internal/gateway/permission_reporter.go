package gateway

import (
	"context"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/tool"
)

type GatewayPermissionDecisionReporter struct {
	bus agent.EventBus
}

func NewGatewayPermissionDecisionReporter(bus agent.EventBus) *GatewayPermissionDecisionReporter {
	return &GatewayPermissionDecisionReporter{bus: bus}
}

func (r *GatewayPermissionDecisionReporter) ReportPermissionDecision(_ context.Context, record tool.PermissionDecisionRecord) {
	if r == nil || r.bus == nil {
		return
	}
	r.bus.Publish(agent.PermissionDecision{
		SessionID:    record.SessionID,
		ToolName:     record.ToolName,
		Action:       record.Action,
		Reason:       record.Reason,
		MatchedRule:  record.MatchedRule,
		ChannelClass: string(record.ChannelClass),
	})
}
