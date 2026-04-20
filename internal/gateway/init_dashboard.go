package gateway

import (
	"log/slog"
	"net/http"

	"github.com/Forest-Isle/IronClaw/internal/cogmetrics"
	"github.com/Forest-Isle/IronClaw/internal/dashboard"
)

func (gw *Gateway) initDashboard() error {
	if !gw.cfg.Dashboard.Enabled {
		return nil
	}

	gw.dashboardBus = dashboard.NewBus(256)
	gw.stateTracker = dashboard.NewAgentStateTracker(gw.dashboardBus)
	go gw.stateTracker.Run()

	if gw.evoEngine != nil && gw.evoEngine.IsEnabled() {
		gw.evoEngine.RegisterHook(dashboard.NewEvolutionBridge(gw.dashboardBus))
	}

	if gw.evoEngine != nil && gw.evoEngine.IsEnabled() {
		gw.cogCollector = cogmetrics.NewCollector()
		gw.evoEngine.RegisterHook(gw.cogCollector)
	}

	emitter := dashboard.NewEmitter(gw.dashboardBus)
	gw.dashEmitter = emitter
	gw.runtime.SetDashboardEmitter(emitter)
	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetDashboardEmitter(emitter)
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
	go func() {
		slog.Info("dashboard server starting", "addr", gw.cfg.Dashboard.Addr)
		if err := gw.dashboardSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("dashboard server error", "err", err)
		}
	}()

	slog.Info("dashboard initialized", "addr", gw.cfg.Dashboard.Addr)
	return nil
}
