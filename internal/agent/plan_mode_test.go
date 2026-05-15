package agent

import (
	"context"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/channel"
)

type mockPlanProvider struct {
	resp *CompletionResponse
	err  error
	req  CompletionRequest
}

func (m *mockPlanProvider) Complete(_ context.Context, req CompletionRequest) (*CompletionResponse, error) {
	m.req = req
	return m.resp, m.err
}

func (m *mockPlanProvider) Stream(_ context.Context, _ CompletionRequest) (StreamIterator, error) {
	return nil, nil
}

type mockApprovalChannel struct {
	approved bool
	called   bool
	toolName string
	input    string
}

func (m *mockApprovalChannel) Name() string { return "mock" }
func (m *mockApprovalChannel) Start(context.Context, channel.InboundHandler) error {
	return nil
}
func (m *mockApprovalChannel) Send(context.Context, channel.OutboundMessage) error {
	return nil
}
func (m *mockApprovalChannel) SendStreaming(context.Context, channel.MessageTarget) (channel.StreamUpdater, error) {
	return nil, nil
}
func (m *mockApprovalChannel) Stop(context.Context) error { return nil }
func (m *mockApprovalChannel) SendApprovalRequest(_ context.Context, _ channel.MessageTarget, toolName string, input string) (bool, error) {
	m.called = true
	m.toolName = toolName
	m.input = input
	return m.approved, nil
}

type mockBasicChannel struct{}

func (m *mockBasicChannel) Name() string { return "basic" }
func (m *mockBasicChannel) Start(context.Context, channel.InboundHandler) error {
	return nil
}
func (m *mockBasicChannel) Send(context.Context, channel.OutboundMessage) error {
	return nil
}
func (m *mockBasicChannel) SendStreaming(context.Context, channel.MessageTarget) (channel.StreamUpdater, error) {
	return nil, nil
}
func (m *mockBasicChannel) Stop(context.Context) error { return nil }

func TestPlanModeGeneratePlanFromGoal(t *testing.T) {
	provider := &mockPlanProvider{
		resp: &CompletionResponse{
			Text: `{
				"goal":"add plan mode",
				"steps":[
					{"description":"inspect files","tool_name":"file_read","reason":"understand code","is_write":false},
					{"description":"write plan mode","tool_name":"file_write","reason":"create implementation","is_write":true}
				],
				"tools_needed":["file_read","file_write"]
			}`,
		},
	}
	pm := NewPlanMode(provider, nil, false)

	plan, err := pm.GeneratePlan(context.Background(), "add plan mode", "agent context")
	if err != nil {
		t.Fatalf("GeneratePlan error: %v", err)
	}
	if plan.Goal != "add plan mode" {
		t.Fatalf("unexpected goal: %q", plan.Goal)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(plan.Steps))
	}
	if !plan.Steps[1].IsWrite {
		t.Fatal("expected second step to be write")
	}
	if provider.req.System != PlanGenerationPrompt {
		t.Fatal("expected plan generation prompt to be used")
	}
	if pm.activePlan == nil || pm.activePlan.ID != plan.ID {
		t.Fatal("expected generated plan to become active")
	}
}

func TestPlanModeInterceptWriteToolWithoutPlan(t *testing.T) {
	pm := NewPlanMode(nil, nil, false)

	allowed, msg, err := pm.InterceptTool(context.Background(), "file_write", []byte(`{"path":"a.go"}`))
	if err != nil {
		t.Fatalf("InterceptTool error: %v", err)
	}
	if allowed {
		t.Fatal("expected write tool to be blocked without plan")
	}
	if msg == "" {
		t.Fatal("expected explanatory message")
	}
}

func TestPlanModeInterceptApprovedTool(t *testing.T) {
	pm := NewPlanMode(nil, nil, false)
	plan := &Plan{
		ID:          "plan-1",
		Goal:        "edit file",
		ToolsNeeded: []string{"file_edit"},
	}
	pm.activePlan = plan
	pm.approvedTools = map[string]bool{"file_edit": true}
	pm.activePlan.Approved = true

	allowed, msg, err := pm.InterceptTool(context.Background(), "file_edit", []byte(`{"path":"a.go"}`))
	if err != nil {
		t.Fatalf("InterceptTool error: %v", err)
	}
	if !allowed {
		t.Fatalf("expected approved tool to pass, got message: %s", msg)
	}
	if !pm.CheckTool("file_edit") {
		t.Fatal("expected CheckTool to allow approved tool")
	}
}

func TestPlanModeCompletePlanResetsState(t *testing.T) {
	pm := NewPlanMode(nil, nil, false)
	pm.activePlan = &Plan{ID: "plan-1", Approved: true}
	pm.approvedTools = map[string]bool{"file_write": true}

	pm.CompletePlan("plan-1")

	if pm.activePlan != nil {
		t.Fatal("expected active plan to be cleared")
	}
	if pm.CheckTool("file_write") {
		t.Fatal("expected approvals to be cleared")
	}
}

func TestPlanModeApprovalFlow(t *testing.T) {
	pm := NewPlanMode(nil, nil, false)
	plan := &Plan{
		ID:          "plan-1",
		Goal:        "implement feature",
		ToolsNeeded: []string{"file_write", "worktree_merge"},
		Steps: []PlanStep{
			{Description: "write code", ToolName: "file_write", Reason: "implementation", IsWrite: true},
		},
	}
	pm.activePlan = plan
	ch := &mockApprovalChannel{approved: true}

	approved, err := pm.RequestApproval(context.Background(), plan, ch, channel.MessageTarget{Channel: "tui", ChannelID: "1"})
	if err != nil {
		t.Fatalf("RequestApproval error: %v", err)
	}
	if !approved {
		t.Fatal("expected plan to be approved")
	}
	if !ch.called {
		t.Fatal("expected approval channel to be used")
	}
	if ch.toolName != "plan_mode" {
		t.Fatalf("unexpected approval tool name: %q", ch.toolName)
	}
	if !pm.activePlan.Approved {
		t.Fatal("expected active plan to be marked approved")
	}
	if !pm.CheckTool("file_write") || !pm.CheckTool("worktree_merge") {
		t.Fatal("expected approved tools to be whitelisted")
	}
}

func TestPlanModeApprovalAutoApprovesWithoutInteractiveChannel(t *testing.T) {
	pm := NewPlanMode(nil, nil, false)
	plan := &Plan{
		ID:          "plan-2",
		Goal:        "implement feature",
		ToolsNeeded: []string{"file_write"},
	}
	pm.activePlan = plan

	approved, err := pm.RequestApproval(context.Background(), plan, &mockBasicChannel{}, channel.MessageTarget{})
	if err != nil {
		t.Fatalf("RequestApproval error: %v", err)
	}
	if !approved {
		t.Fatal("expected auto-approval for non-interactive channel")
	}
	if !pm.CheckTool("file_write") {
		t.Fatal("expected approved tool after auto-approval")
	}
}
