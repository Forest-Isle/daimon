package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/punkopunko/ironclaw/internal/config"
	"github.com/punkopunko/ironclaw/internal/tool"
)

// Manager manages multiple MCP server connections.
type Manager struct {
	clients map[string]client.MCPClient
	mu      sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{
		clients: make(map[string]client.MCPClient),
	}
}

// StartServers connects to each configured MCP server, discovers tools,
// and registers them in the tool registry. Individual server failures
// are logged but do not block other servers.
func (m *Manager) StartServers(ctx context.Context, servers map[string]config.MCPServerConfig, registry *tool.Registry) error {
	var errs []error

	for name, srv := range servers {
		if err := m.startServer(ctx, name, srv, registry); err != nil {
			slog.Error("mcp server failed to start", "server", name, "err", err)
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("some MCP servers failed: %v", errs)
	}
	return nil
}

func (m *Manager) startServer(ctx context.Context, name string, srv config.MCPServerConfig, registry *tool.Registry) error {
	// Build env slice from map
	env := make([]string, 0, len(srv.Env))
	for k, v := range srv.Env {
		env = append(env, k+"="+v)
	}

	// Create stdio client
	c, err := client.NewStdioMCPClient(srv.Command, env, srv.Args...)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	// Initialize MCP handshake
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "ironclaw",
		Version: "1.0.0",
	}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		c.Close()
		return fmt.Errorf("initialize: %w", err)
	}

	// Discover tools
	toolsResp, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		c.Close()
		return fmt.Errorf("list tools: %w", err)
	}

	// Register each tool as an adapter
	for _, t := range toolsResp.Tools {
		adapter := NewToolAdapter(c, name, t, srv.RequiresApproval)
		registry.Register(adapter)
		slog.Info("mcp tool registered", "name", adapter.Name())
	}

	m.mu.Lock()
	m.clients[name] = c
	m.mu.Unlock()

	slog.Info("mcp server started", "server", name, "tools", len(toolsResp.Tools))
	return nil
}

// Close shuts down all MCP server connections.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, c := range m.clients {
		if err := c.Close(); err != nil {
			slog.Error("failed to close mcp client", "server", name, "err", err)
		}
	}
	m.clients = make(map[string]client.MCPClient)
	return nil
}
