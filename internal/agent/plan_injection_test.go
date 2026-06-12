package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/tool"
)

func TestBuildSystemPrompt_PlanInjection(t *testing.T) {
	deps := AgentDeps{
		Core: CoreDeps{
			Cfg: config.AgentConfig{
				SystemPrompt:  "You are a test agent.",
				MaxIterations: 1,
			},
			LLMCfg: config.LLMConfig{Model: "test"},
			Tools:  tool.NewRegistry(),
		},
		Memory:     MemoryDeps{}.WithDefaults(),
		Security:   SecurityDeps{}.WithDefaults(),
		MultiAgent: MultiAgentDeps{}.WithDefaults(),
	}
	rt := NewAgent(&deps, &LinearLoop{}, NewEventBus())

	// Session without plan — no plan section
	sessNoPlan := &session.Session{Metadata: map[string]string{}}
	prompt := rt.buildSystemPrompt(context.Background(), sessNoPlan, "hello")
	if strings.Contains(prompt, "Current Plan") {
		t.Error("system prompt should NOT contain 'Current Plan' when no plan exists")
	}

	// Session with plan — plan section injected
	sessWithPlan := &session.Session{Metadata: map[string]string{
		"plan": `{"goal":"Fix bug","steps":[{"id":"1","description":"Add test","criteria":"test_run passes","status":"in_progress"}]}`,
	}}
	prompt = rt.buildSystemPrompt(context.Background(), sessWithPlan, "hello")
	if !strings.Contains(prompt, "Current Plan") {
		t.Error("system prompt should contain 'Current Plan' section")
	}
	if !strings.Contains(prompt, "Fix bug") {
		t.Error("system prompt should contain plan goal")
	}
	if !strings.Contains(prompt, "test_run passes") {
		t.Error("system prompt should contain step criteria")
	}
}

func TestBuildSystemPrompt_NilSession(t *testing.T) {
	deps := AgentDeps{
		Core: CoreDeps{
			Cfg: config.AgentConfig{
				SystemPrompt:  "You are a test agent.",
				MaxIterations: 1,
			},
			LLMCfg: config.LLMConfig{Model: "test"},
			Tools:  tool.NewRegistry(),
		},
		Memory:     MemoryDeps{}.WithDefaults(),
		Security:   SecurityDeps{}.WithDefaults(),
		MultiAgent: MultiAgentDeps{}.WithDefaults(),
	}
	rt := NewAgent(&deps, &LinearLoop{}, NewEventBus())

	// Should not panic with nil session
	prompt := rt.buildSystemPrompt(context.Background(), nil, "hello")
	if strings.Contains(prompt, "Current Plan") {
		t.Error("nil session should not produce plan section")
	}
}

func TestPlanTool_InToolDefinitions(t *testing.T) {
	deps := AgentDeps{
		Core: CoreDeps{
			Cfg: config.AgentConfig{
				SystemPrompt:  "You are a test agent.",
				MaxIterations: 1,
			},
			LLMCfg: config.LLMConfig{Model: "test"},
			Tools:  tool.NewRegistry(),
		},
		Memory:     MemoryDeps{}.WithDefaults(),
		Security:   SecurityDeps{}.WithDefaults(),
		MultiAgent: MultiAgentDeps{}.WithDefaults(),
	}
	deps.Core.Tools.Register(tool.NewPlanTool(&stubPlanStoreForTest{plans: make(map[string]string)}))
	rt := NewAgent(&deps, &LinearLoop{}, NewEventBus())

	defs := rt.buildToolDefs()
	hasPlan := false
	for _, d := range defs {
		if d.Name == "plan" {
			hasPlan = true
			break
		}
	}
	if !hasPlan {
		t.Error("plan tool should be in tool definitions")
	}
}

type stubPlanStoreForTest struct {
	plans map[string]string
}

func (s *stubPlanStoreForTest) GetPlan(sessionID string) (string, error) {
	return s.plans[sessionID], nil
}

func (s *stubPlanStoreForTest) SavePlan(sessionID string, planJSON string) error {
	s.plans[sessionID] = planJSON
	return nil
}
