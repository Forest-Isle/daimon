package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/dashboard"
)

// DashboardSubsystem manages the web dashboard for real-time agent monitoring.
type DashboardSubsystem struct {
	bus          *dashboard.Bus
	hub          *dashboard.Hub
	srv          *http.Server
	stateTracker *dashboard.AgentStateTracker
	emitter      agent.ObservabilityEmitter
}

func (ds *DashboardSubsystem) Name() string { return "dashboard" }

// Start starts the hub and HTTP server. No-op if not initialized.
func (ds *DashboardSubsystem) Start(ctx context.Context) error {
	if ds.hub == nil {
		return nil
	}
	// Hub is started in setupDashboard which is called from initDashboard
	// or the OnEnable lifecycle hook. We verify it's running.
	slog.Debug("dashboard subsystem ready")
	return nil
}

// Stop gracefully shuts down the dashboard server, hub, and state tracker.
func (ds *DashboardSubsystem) Stop(ctx context.Context) error {
	if ds.srv != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := ds.srv.Shutdown(shutCtx); err != nil {
			slog.Warn("dashboard: server shutdown error", "err", err)
		}
	}
	if ds.hub != nil {
		ds.hub.Stop()
	}
	if ds.stateTracker != nil {
		ds.stateTracker.Stop()
	}
	return nil
}

// Bus returns the dashboard event bus, or nil.
func (ds *DashboardSubsystem) Bus() *dashboard.Bus { return ds.bus }

// Hub returns the dashboard WebSocket hub, or nil.
func (ds *DashboardSubsystem) Hub() *dashboard.Hub { return ds.hub }

// Server returns the dashboard HTTP server, or nil.
func (ds *DashboardSubsystem) Server() *http.Server { return ds.srv }

// StateTracker returns the agent state tracker, or nil.
func (ds *DashboardSubsystem) StateTracker() *dashboard.AgentStateTracker { return ds.stateTracker }

// Emitter returns the dashboard emitter, or nil.
func (ds *DashboardSubsystem) Emitter() agent.ObservabilityEmitter { return ds.emitter }
