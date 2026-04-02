package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/punkopunko/ironclaw/internal/config"
	"github.com/punkopunko/ironclaw/internal/mcp"
	"github.com/punkopunko/ironclaw/internal/tool"
)

// AgentMCPConfig is the per-agent MCP server configuration in agent spec YAML.
type AgentMCPConfig struct {
	Name             string            `yaml:"name"`
	Command          string            `yaml:"command"`
	Args             []string          `yaml:"args"`
	Env              map[string]string `yaml:"env"`
	RequiresApproval bool              `yaml:"requires_approval"`
}

// AgentMCPManager manages per-agent MCP server lifecycles.
// When an agent starts, its MCP servers are initialized and their tools
// are merged into the agent's scoped registry. When the agent finishes,
// the MCP servers are stopped and their tools are removed.
type AgentMCPManager struct {
	mu            sync.Mutex
	parentManager *mcp.Manager            // shared system-wide MCP manager
	agentManagers map[string]*mcp.Manager // agentID → agent-specific MCP manager
}

// NewAgentMCPManager creates a new per-agent MCP manager.
func NewAgentMCPManager(parentManager *mcp.Manager) *AgentMCPManager {
	return &AgentMCPManager{
		parentManager: parentManager,
		agentManagers: make(map[string]*mcp.Manager),
	}
}

// InitializeForAgent starts MCP servers declared in the agent spec and
// registers their tools into the provided scoped registry.
// Returns the agent-specific MCP manager for cleanup.
func (m *AgentMCPManager) InitializeForAgent(ctx context.Context, agentID string, mcpConfigs []AgentMCPConfig, registry *tool.Registry) error {
	if len(mcpConfigs) == 0 {
		return nil
	}

	// Convert AgentMCPConfig to config.MCPServerConfig
	servers := make(map[string]config.MCPServerConfig, len(mcpConfigs))
	for _, cfg := range mcpConfigs {
		servers[cfg.Name] = config.MCPServerConfig{
			Command:          cfg.Command,
			Args:             cfg.Args,
			Env:              cfg.Env,
			RequiresApproval: cfg.RequiresApproval,
		}
	}

	// Create agent-specific MCP manager
	agentMgr := mcp.NewManager()
	if err := agentMgr.StartServers(ctx, servers, registry); err != nil {
		slog.Warn("agent MCP: some servers failed to start",
			"agent_id", agentID,
			"err", err,
		)
		// Don't fail — partial MCP is OK
	}

	m.mu.Lock()
	m.agentManagers[agentID] = agentMgr
	m.mu.Unlock()

	slog.Info("agent MCP: initialized servers",
		"agent_id", agentID,
		"server_count", len(mcpConfigs),
	)

	return nil
}

// CleanupForAgent stops all MCP servers associated with the given agent
// and removes their tools from the registry.
func (m *AgentMCPManager) CleanupForAgent(agentID string) {
	m.mu.Lock()
	agentMgr, ok := m.agentManagers[agentID]
	if ok {
		delete(m.agentManagers, agentID)
	}
	m.mu.Unlock()

	if !ok {
		return
	}

	if agentMgr != nil {
		if err := agentMgr.Close(); err != nil {
			slog.Warn("agent MCP: cleanup failed",
				"agent_id", agentID,
				"err", err,
			)
		}
	}

	slog.Info("agent MCP: cleaned up servers", "agent_id", agentID)
}

// CleanupAll stops all agent-specific MCP servers.
func (m *AgentMCPManager) CleanupAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.agentManagers))
	for id := range m.agentManagers {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		m.CleanupForAgent(id)
	}
}

// ActiveAgents returns the number of agents with active MCP servers.
func (m *AgentMCPManager) ActiveAgents() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.agentManagers)
}

// HasServersForAgent returns true if the given agent has active MCP servers.
func (m *AgentMCPManager) HasServersForAgent(agentID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.agentManagers[agentID]
	return ok
}

// AgentMCPConfigsToString returns a debug-friendly summary of MCP configs.
func AgentMCPConfigsToString(configs []AgentMCPConfig) string {
	if len(configs) == 0 {
		return "(none)"
	}
	names := make([]string, len(configs))
	for i, c := range configs {
		names[i] = c.Name
	}
	return fmt.Sprintf("[%s]", joinStrings(names, ", "))
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
