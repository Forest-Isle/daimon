package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/dashboard"
	"github.com/Forest-Isle/IronClaw/internal/ratelimit"
)

func (gw *Gateway) initDashboard() error {
	if !gw.featureEnabled("dashboard") {
		return nil
	}
	return gw.setupDashboard()
}

// setupDashboard performs the actual dashboard initialization.
// Separated from initDashboard so that lifecycle hooks can call it
// without going through featureEnabled() (which would deadlock since
// Registry.Enable holds the mutex when invoking OnEnable).
func (gw *Gateway) setupDashboard() error {
	gw.dashboardBus = dashboard.NewBus(256)
	gw.stateTracker = dashboard.NewAgentStateTracker(gw.dashboardBus)
	go gw.stateTracker.Run()

	// Use featureEnabled to check registry state, not evoEngine.IsEnabled()
	// (which reads static config and misses runtime-enabled evolution).
	if gw.evoEngine != nil && gw.featureEnabled("evolution") {
		gw.evoEngine.RegisterHook(dashboard.NewEvolutionBridge(gw.dashboardBus))
	}

	// Ensure the cog-metrics collector exists (idempotent — initCogMetrics
	// already ran during the normal init sequence; this covers the hot-reload
	// path where setupDashboard is called via OnEnable after a fresh start).
	gw.initCogMetrics()

	emitter := dashboard.NewEmitter(gw.dashboardBus)
	gw.dashEmitter = emitter
	gw.runtime.SetDashboardEmitter(emitter)
	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetDashboardEmitter(emitter)
	}
	if gw.subAgentMgr != nil {
		gw.subAgentMgr.SetDashboardEmitter(emitter)
	}
	if gw.contextMgr != nil {
		gw.contextMgr.SetDashboardEmitter(emitter)
	}

	gw.dashboardHub = dashboard.NewHub(gw.dashboardBus)
	go gw.dashboardHub.Run()

	gw.dashboardSrv = dashboard.NewServer(gw.cfg.Dashboard, dashboard.ServerDeps{
		DB:        gw.db,
		Hub:       gw.dashboardHub,
		Tracker:   gw.stateTracker,
		Collector: gw.cogCollector,
		StaticFS:  dashboard.WebDistFS(),
	})

	// Apply rate limit middleware if the limiter is configured
	if gw.rateLimiter != nil {
		wrapped := ratelimit.RateLimitMiddleware(gw.rateLimiter, nil)(gw.dashboardSrv.Handler)
		gw.dashboardSrv.Handler = wrapped
	}

	go func() {
		slog.Info("dashboard server starting", "addr", gw.cfg.Dashboard.Addr)
		if err := gw.dashboardSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("dashboard server error", "err", err)
		}
	}()

	slog.Info("dashboard initialized", "addr", gw.cfg.Dashboard.Addr)
	return nil
}

func (gw *Gateway) startDashboard() error {
	if gw.dashboardHub != nil {
		return nil
	}
	return gw.setupDashboard()
}

func (gw *Gateway) stopDashboard() error {
	if gw.dashboardSrv != nil {
		ctx, cancel := context.WithTimeout(gw.initCtx, 5*time.Second)
		defer cancel()
		if err := gw.dashboardSrv.Shutdown(ctx); err != nil {
			slog.Warn("dashboard shutdown error", "err", err)
		}
		gw.dashboardSrv = nil
	}
	if gw.dashboardHub != nil {
		gw.dashboardHub.Stop()
		gw.dashboardHub = nil
	}
	if gw.stateTracker != nil {
		gw.stateTracker.Stop()
		gw.stateTracker = nil
	}
	gw.dashboardBus = nil
	gw.dashEmitter = nil
	gw.cogCollector = nil
	slog.Info("dashboard stopped")
	return nil
}
