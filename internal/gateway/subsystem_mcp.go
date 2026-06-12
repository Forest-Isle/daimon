package gateway

import (
	"context"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/mcp"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/Forest-Isle/daimon/internal/userdir"
	"log/slog"
	"sync"
	"time"
)

type MCPSubsystem struct {
	Manager *mcp.Manager
}

func (ms *MCPSubsystem) Name() string                  { return "mcp" }
func (ms *MCPSubsystem) Start(_ context.Context) error { return nil }
func (ms *MCPSubsystem) Stop(_ context.Context) error {
	if ms.Manager != nil {
		return ms.Manager.Close()
	}
	return nil
}

func InitMCP() *MCPSubsystem { return &MCPSubsystem{Manager: mcp.NewManager()} }

func (ms *MCPSubsystem) StartServers(ctx context.Context, cfg *config.Config, toolsReg *tool.Registry) {
	var wg sync.WaitGroup
	for name, srv := range cfg.Tools.MCP.Servers {
		name, srv := name, srv
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ms.Manager.StartServer(ctx, name, srv, toolsReg); err != nil {
				slog.Error("mcp server failed to start", "server", name, "err", err)
			}
		}()
	}
	wg.Wait()
}

func (ms *MCPSubsystem) WatchDir(ctx context.Context, cfg *config.Config) {
	poll := cfg.Tools.MCP.PollInterval
	if poll <= 0 {
		poll = 30 * time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			desired := userdir.ScanMCPDir()
			if desired == nil {
				desired = make(map[string]config.MCPServerConfig)
			}
			for name, srv := range cfg.Tools.MCP.Servers {
				desired[name] = srv
			}
			if ms.Manager != nil {
				ms.Manager.SyncServers(ctx, desired, nil)
			}
		}
	}
}
