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

// StopServer shuts down a single MCP server and unregisters its tools from the registry.
func (m *Manager) StopServer(name string, registry *tool.Registry) {
	m.mu.Lock()
	c, ok := m.clients[name]
	if ok {
		delete(m.clients, name)
	}
	m.mu.Unlock()

	if ok {
		if err := c.Close(); err != nil {
			slog.Error("mcp: close client", "server", name, "err", err)
		}
	}

	prefix := "mcp_" + name + "_"
	removed := registry.UnregisterByPrefix(prefix)
	slog.Info("mcp server stopped", "server", name, "tools_removed", len(removed))
}

// RunningServers returns the names of currently running MCP servers.
func (m *Manager) RunningServers() map[string]struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]struct{}, len(m.clients))
	for name := range m.clients {
		out[name] = struct{}{}
	}
	return out
}

// SyncServers compares the desired server set against running servers.
// New servers are started, removed servers are stopped, and changed servers are restarted.
func (m *Manager) SyncServers(ctx context.Context, desired map[string]config.MCPServerConfig, registry *tool.Registry) {
	running := m.RunningServers()

	// Stop servers that are no longer in desired config.
	for name := range running {
		if _, ok := desired[name]; !ok {
			slog.Info("mcp: removing server (no longer in config)", "server", name)
			m.StopServer(name, registry)
		}
	}

	// Start servers that are new.
	for name, srv := range desired {
		if _, ok := running[name]; ok {
			continue // already running
		}
		slog.Info("mcp: starting new server", "server", name)
		if err := m.startServer(ctx, name, srv, registry); err != nil {
			slog.Error("mcp: failed to start server during sync", "server", name, "err", err)
		}
	}
}
