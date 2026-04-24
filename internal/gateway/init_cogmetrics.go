package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/cogmetrics"
)

// initCogMetrics creates the cognitive-metrics collector, health checker, and
// circuit breaker. It wires the collector into the evolution engine and makes
// the health checker available for the cognitive agent to record metrics.
//
// Called unconditionally after the evolution engine is set up so that eval runs
// (which disable the dashboard) still accumulate health metrics.
func (gw *Gateway) initCogMetrics() {
	if gw.cogCollector != nil {
		return
	}
	if gw.evoEngine == nil || !gw.featureEnabled("evolution") {
		return
	}
	gw.cogCollector = cogmetrics.NewCollector()
	gw.evoEngine.RegisterHook(gw.cogCollector)

	// Health checker and circuit breaker
	gw.healthChecker = cogmetrics.NewHealthChecker()
	gw.breaker = cogmetrics.NewBreaker(gw.healthChecker)

	// Register breaker action callbacks
	gw.breaker.OnAction(cogmetrics.ActionTriggerCompression, func() {
		slog.Warn("coghealth: breaker triggered compression")
	})
	gw.breaker.OnAction(cogmetrics.ActionPauseAndAskUser, func() {
		slog.Warn("coghealth: breaker requesting user intervention")
	})
	gw.breaker.OnAction(cogmetrics.ActionDegradeToSimple, func() {
		slog.Warn("coghealth: breaker degrading to simple mode")
	})

	slog.Info("cognitive health checker and circuit breaker initialized")
}
