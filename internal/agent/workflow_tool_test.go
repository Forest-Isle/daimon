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
