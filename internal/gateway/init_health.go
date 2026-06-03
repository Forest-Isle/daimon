package gateway

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/health"
)

// startHealthServer starts a standalone HTTP server for health check endpoints
// (/healthz, /readyz, /health). This server is always started regardless of
// whether the dashboard is enabled, ensuring health probes are always available.
func (gw *Gateway) startHealthServer() {
	if gw.healthRegistry == nil {
		slog.Warn("health: registry not initialized, skipping health server")
		return
	}

	port := gw.Config().Health.Port
	if port <= 0 {
		port = 9090
	}

	handler := health.NewHandler(gw.healthRegistry)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	gw.healthSrv = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	go func() {
		slog.Info("health check server starting", "addr", gw.healthSrv.Addr)
		if err := gw.healthSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("health check server error", "err", err)
		}
	}()
}
