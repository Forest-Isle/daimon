package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/Forest-Isle/daimon/internal/workflow"
)

func TestWorkflowToolExecutesAgentWorkflow(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "workflow-tool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	provider := &mockSubagentProvider{response: "```json\n{\"status\":\"success\",\"summary\":\"Task done\",\"artifacts\":[\"artifact.txt\"]}\n```"}
	sessions := session.NewManager(db)
	tools := tool.NewRegistry()
	cfg := config.AgentConfig{MaxIterations: 2}
	llmCfg := config.LLMConfig{Model: "test-model", MaxTokens: 100}
	deps := AgentDeps{
		Core: CoreDeps{
			Provider: provider,
			Sessions: sessions,
			DB:       db,
			Tools:    tools,
			Cfg:      cfg,
			LLMCfg:   llmCfg,
		},
	}.WithDefaults()
	subMgr := NewSubAgentManager(deps)
	agentMgr := NewAgentManager(provider, sessions, db, nil, tools, cfg, llmCfg)
	if err := agentMgr.Add(&AgentSpec{Name: "researcher", Description: "researches", MaxIterations: 2}); err != nil {
		t.Fatal(err)
	}

	wt := NewWorkflowTool(agentMgr, subMgr, workflow.NewMemoryCache())
	payload, _ := json.Marshal(map[string]string{"spec": `
name: agent-workflow
stages:
  - id: research
    steps:
      - id: gather
        type: agent
        agent: researcher
        task: gather facts
`})
	result, err := wt.Execute(context.Background(), payload)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != "" {
		t.Fatalf("workflow tool returned error: %s", result.Error)
	}
	if !strings.Contains(result.Output, `"workflow_name": "agent-workflow"`) ||
		!strings.Contains(result.Output, "Task done") ||
		!strings.Contains(result.Output, "artifact.txt") {
		t.Fatalf("workflow output = %s", result.Output)
	}
}

func TestWorkflowUnknownAgentListsAvailable(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "workflow-unknown.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	provider := &mockSubagentProvider{response: "```json\n{\"status\":\"success\",\"summary\":\"x\"}\n```"}
	sessions := session.NewManager(db)
	tools := tool.NewRegistry()
	cfg := config.AgentConfig{MaxIterations: 2}
	llmCfg := config.LLMConfig{Model: "test-model", MaxTokens: 100}
	deps := AgentDeps{Core: CoreDeps{Provider: provider, Sessions: sessions, DB: db, Tools: tools, Cfg: cfg, LLMCfg: llmCfg}}.WithDefaults()
	subMgr := NewSubAgentManager(deps)
	agentMgr := NewAgentManager(provider, sessions, db, nil, tools, cfg, llmCfg)
	if err := agentMgr.Add(&AgentSpec{Name: "researcher", Description: "researches", MaxIterations: 2}); err != nil {
		t.Fatal(err)
	}

	wt := NewWorkflowTool(agentMgr, subMgr, workflow.NewMemoryCache())
	payload, _ := json.Marshal(map[string]string{"spec": `
name: agent-workflow
stages:
  - id: research
    steps:
      - id: gather
        type: agent
        agent: ghostwriter
        task: gather facts
`})
	result, err := wt.Execute(context.Background(), payload)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	combined := result.Output + result.Error
	if !strings.Contains(combined, "researcher") {
		t.Fatalf("unknown-agent error should list available agents; got: %s", combined)
	}
	if !strings.Contains(combined, "agent_dispatch") {
		t.Fatalf("unknown-agent error should hint agent_dispatch; got: %s", combined)
	}
}

func TestWorkflowToolWithParentSession(t *testing.T) {
	// Regression guard for parent-session linkage: an agent step must still run
	// cleanly when a parent session id rides on ctx (the path that forwards the
	// worker's activity into the parent transcript). Exercises the parent-link
	// threading added to the workflow runner.
	db, err := store.Open(filepath.Join(t.TempDir(), "workflow-parent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	provider := &mockSubagentProvider{response: "```json\n{\"status\":\"success\",\"summary\":\"Task done\"}\n```"}
	sessions := session.NewManager(db)
	tools := tool.NewRegistry()
	cfg := config.AgentConfig{MaxIterations: 2}
	llmCfg := config.LLMConfig{Model: "test-model", MaxTokens: 100}
	deps := AgentDeps{Core: CoreDeps{Provider: provider, Sessions: sessions, DB: db, Tools: tools, Cfg: cfg, LLMCfg: llmCfg}}.WithDefaults()
	subMgr := NewSubAgentManager(deps)
	agentMgr := NewAgentManager(provider, sessions, db, nil, tools, cfg, llmCfg)
	if err := agentMgr.Add(&AgentSpec{Name: "researcher", Description: "researches", MaxIterations: 2}); err != nil {
		t.Fatal(err)
	}

	wt := NewWorkflowTool(agentMgr, subMgr, workflow.NewMemoryCache())
	payload, _ := json.Marshal(map[string]string{"spec": `
name: agent-workflow
stages:
  - id: research
    steps:
      - id: gather
        type: agent
        agent: researcher
        task: gather facts
`})

	ctx := tool.WithSessionID(context.Background(), "parent-sess")
	result, err := wt.Execute(ctx, payload)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != "" {
		t.Fatalf("workflow tool returned error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "Task done") {
		t.Fatalf("workflow output = %s", result.Output)
	}
}
