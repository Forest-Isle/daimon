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
	gw.dashboard.bus = dashboard.NewBus(256)
	gw.dashboard.stateTracker = dashboard.NewAgentStateTracker(gw.dashboard.bus)
	go gw.dashboard.stateTracker.Run()

	// Use featureEnabled to check registry state, not evoEngine.IsEnabled()
	// (which reads static config and misses runtime-enabled evolution).
	if gw.evolution.engine != nil && gw.featureEnabled("evolution") {
		gw.evolution.engine.RegisterHook(dashboard.NewEvolutionBridge(gw.dashboard.bus))
	}

	// Ensure the cog-metrics collector exists (idempotent — initCogMetrics
	// already ran during the normal init sequence; this covers the hot-reload
	// path where setupDashboard is called via OnEnable after a fresh start).
	gw.initCogMetrics()

	emitter := dashboard.NewEmitter(gw.dashboard.bus)
	gw.dashboard.emitter = emitter
	gw.runtime.SetDashboardEmitter(emitter)
	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetDashboardEmitter(emitter)
	}
	if gw.tasks.subAgentMgr != nil {
		gw.tasks.subAgentMgr.SetDashboardEmitter(emitter)
	}
	if gw.contextMgr != nil {
		gw.contextMgr.SetDashboardEmitter(emitter)
	}

	gw.dashboard.hub = dashboard.NewHub(gw.dashboard.bus)
	go gw.dashboard.hub.Run()

	gw.dashboard.srv = dashboard.NewServer(gw.cfg.Dashboard, dashboard.ServerDeps{
		DB:        gw.db,
		Hub:       gw.dashboard.hub,
		Tracker:   gw.dashboard.stateTracker,
		Collector: gw.evolution.cogCollector,
		StaticFS:  dashboard.WebDistFS(),
	})

	// Apply rate limit middleware if the limiter is configured
	if gw.observability.rateLimiter != nil {
		wrapped := ratelimit.RateLimitMiddleware(gw.observability.rateLimiter, nil)(gw.dashboard.srv.Handler)
		gw.dashboard.srv.Handler = wrapped
	}

	go func() {
		slog.Info("dashboard server starting", "addr", gw.cfg.Dashboard.Addr)
		if err := gw.dashboard.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("dashboard server error", "err", err)
		}
	}()

	slog.Info("dashboard initialized", "addr", gw.cfg.Dashboard.Addr)
	return nil
}

func (gw *Gateway) startDashboard() error {
	if gw.dashboard.hub != nil {
		return nil
	}
	return gw.setupDashboard()
}

func (gw *Gateway) stopDashboard() error {
	if gw.dashboard.srv != nil {
		ctx, cancel := context.WithTimeout(gw.initCtx, 5*time.Second)
		defer cancel()
		if err := gw.dashboard.srv.Shutdown(ctx); err != nil {
			slog.Warn("dashboard shutdown error", "err", err)
		}
		gw.dashboard.srv = nil
	}
	if gw.dashboard.hub != nil {
		gw.dashboard.hub.Stop()
		gw.dashboard.hub = nil
	}
	if gw.dashboard.stateTracker != nil {
		gw.dashboard.stateTracker.Stop()
		gw.dashboard.stateTracker = nil
	}
	gw.dashboard.bus = nil
	gw.dashboard.emitter = nil
	gw.evolution.cogCollector = nil
	slog.Info("dashboard stopped")
	return nil
}
