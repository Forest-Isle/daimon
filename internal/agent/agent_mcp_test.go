package agent

import (
	"testing"
)

func TestAgentMCPManager_Lifecycle(t *testing.T) {
	mgr := NewAgentMCPManager(nil) // nil parent OK for unit tests

	if mgr.ActiveAgents() != 0 {
		t.Errorf("expected 0 active agents, got %d", mgr.ActiveAgents())
	}

	if mgr.HasServersForAgent("agent-1") {
		t.Error("should not have servers for nonexistent agent")
	}
}

func TestAgentMCPManager_CleanupAll(t *testing.T) {
	mgr := NewAgentMCPManager(nil)

	// Manually insert fake managers to test cleanup
	mgr.mu.Lock()
	mgr.agentManagers["a1"] = nil
	mgr.agentManagers["a2"] = nil
	mgr.mu.Unlock()

	if mgr.ActiveAgents() != 2 {
		t.Errorf("expected 2 active agents, got %d", mgr.ActiveAgents())
	}

	mgr.CleanupAll()

	if mgr.ActiveAgents() != 0 {
		t.Errorf("expected 0 active agents after cleanup, got %d", mgr.ActiveAgents())
	}
}

func TestAgentMCPManager_CleanupNonexistent(t *testing.T) {
	mgr := NewAgentMCPManager(nil)
	// Should not panic
	mgr.CleanupForAgent("nonexistent")
}

func TestAgentMCPManager_HasServersForAgent(t *testing.T) {
	mgr := NewAgentMCPManager(nil)

	mgr.mu.Lock()
	mgr.agentManagers["agent-x"] = nil
	mgr.mu.Unlock()

	if !mgr.HasServersForAgent("agent-x") {
		t.Error("expected true for existing agent")
	}
	if mgr.HasServersForAgent("agent-y") {
		t.Error("expected false for nonexistent agent")
	}
}

func TestAgentMCPConfigsToString(t *testing.T) {
	result := AgentMCPConfigsToString(nil)
	if result != "(none)" {
		t.Errorf("expected '(none)', got %q", result)
	}

	result = AgentMCPConfigsToString([]AgentMCPConfig{
		{Name: "fs"},
		{Name: "db"},
	})
	if result != "[fs, db]" {
		t.Errorf("expected '[fs, db]', got %q", result)
	}
}

func TestAgentMCPConfig_Fields(t *testing.T) {
	cfg := AgentMCPConfig{
		Name:             "workspace",
		Command:          "mcp-filesystem",
		Args:             []string{"/home/user"},
		Env:              map[string]string{"HOME": "/home/user"},
		RequiresApproval: true,
	}

	if cfg.Name != "workspace" {
		t.Errorf("expected name 'workspace', got %q", cfg.Name)
	}
	if cfg.Command != "mcp-filesystem" {
		t.Errorf("expected command 'mcp-filesystem', got %q", cfg.Command)
	}
	if !cfg.RequiresApproval {
		t.Error("expected RequiresApproval to be true")
	}
}
