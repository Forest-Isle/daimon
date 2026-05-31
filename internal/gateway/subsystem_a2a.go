package gateway

import (
	"context"
	"log/slog"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/a2a"
)

// A2ASubsystem manages the Agent-to-Agent protocol server.
type A2ASubsystem struct {
	server *a2a.Server
}

func (as *A2ASubsystem) Name() string { return "a2a" }

// Start is a no-op — the A2A server is started on demand via feature lifecycle hooks.
func (as *A2ASubsystem) Start(_ context.Context) error { return nil }

// Stop gracefully shuts down the A2A server.
func (as *A2ASubsystem) Stop(ctx context.Context) error {
	if as.server != nil {
		shutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := as.server.Stop(shutCtx); err != nil {
			slog.Warn("a2a: server stop error", "err", err)
			return err
		}
		slog.Info("a2a: server stopped")
	}
	return nil
}

// Server returns the A2A server, or nil.
func (as *A2ASubsystem) Server() *a2a.Server { return as.server }
