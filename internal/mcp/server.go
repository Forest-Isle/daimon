package mcp

import (
	"context"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// ServerOption configures the IronClaw MCP server.
type ServerOption func(*Server)

// Server wraps a mark3labs/mcp-go MCPServer with IronClaw-specific tools
// and dependency wiring.
type Server struct {
	mcpServer *mcpserver.MCPServer
	deps      ServerDeps
}

// NewServer creates a new IronClaw MCP server. Apply ServerOptions to
// configure dependencies before calling ServeStdio or ServeHTTP.
func NewServer(opts ...ServerOption) *Server {
	srv := mcpserver.NewMCPServer("ironclaw", "1.0.0",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithLogging(),
		mcpserver.WithInstructions("IronClaw AI agent — access memory, knowledge, and skills"),
	)
	s := &Server{mcpServer: srv}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ServeStdio starts the MCP server on stdin/stdout (JSON-RPC over stdio).
func (s *Server) ServeStdio(_ context.Context) error {
	slog.Info("mcp server: starting stdio transport")
	return mcpserver.ServeStdio(s.mcpServer)
}

// ServeHTTP starts the MCP server as a Streamable HTTP endpoint.
func (s *Server) ServeHTTP(_ context.Context, addr string) error {
	slog.Info("mcp server: starting HTTP transport", "addr", addr)
	httpSrv := mcpserver.NewStreamableHTTPServer(s.mcpServer)
	return httpSrv.Start(addr)
}

// RegisterTool registers a single MCP tool with the server.
func (s *Server) RegisterTool(tool mcp.Tool, handler mcpserver.ToolHandlerFunc) {
	s.mcpServer.AddTool(tool, handler)
}

// WithDeps returns a ServerOption that sets the dependency bag.
func WithDeps(deps ServerDeps) ServerOption {
	return func(s *Server) {
		s.deps = deps
	}
}
