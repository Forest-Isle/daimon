package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

func TestAgentTool_SubAgentManager_Integration(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	sessions := session.NewManager(db)
	tools := tool.NewRegistry()

	mgr := NewSubAgentManager(
		&mockSubagentProvider{response: "<result>\n<status>success</status>\n<summary>Integration test passed.</summary>\n<artifacts>/tmp/output.txt</artifacts>\n</result>"},
		sessions, db, nil, tools,
		config.AgentConfig{MaxIterations: 1, SystemPrompt: "You are helpful."},
		config.LLMConfig{Model: "test-model", MaxTokens: 100},
	)

	spec := &AgentSpec{
		Name:        "integration-agent",
		Description: "An agent for integration testing",
	}
	_ = spec.Validate()

	at := NewAgentTool(spec, mgr)

	if at.Name() != "agent_integration-agent" {
		t.Errorf("name = %q, want agent_integration-agent", at.Name())
	}
	if at.Description() != "An agent for integration testing" {
		t.Errorf("description = %q", at.Description())
	}

	input, _ := json.Marshal(agentToolInput{Task: "run the integration test"})
	result, err := at.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if result.Output == "" {
		t.Error("expected non-empty output")
	}

	if !strings.Contains(result.Output, "integration-agent") {
		t.Error("output should contain agent name")
	}
	if !strings.Contains(result.Output, "success") {
		t.Error("output should contain status")
	}
	if !strings.Contains(result.Output, "Integration test passed") {
		t.Error("output should contain summary")
	}
}
