package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/cogmetrics"
)

// initCogMetrics creates the cognitive-metrics collector, health checker, and
// circuit breaker. It wires the collector into the evolution engine and makes
// the health checker available for the cognitive agent to record metrics.
//
// Called unconditionally after the evolution engine is set up so that all run
// modes still accumulate health metrics.
func (gw *Gateway) initCogMetrics() {
	if gw.evolution.cogCollector != nil {
		return
	}
	if gw.evolution.engine == nil || !gw.featureEnabled("evolution") {
		return
	}
	gw.evolution.cogCollector = cogmetrics.NewCollector()
	gw.evolution.engine.RegisterHook(gw.evolution.cogCollector)

	// Health checker and circuit breaker
	gw.evolution.healthChecker = cogmetrics.NewHealthChecker()
	gw.evolution.breaker = cogmetrics.NewBreaker(gw.evolution.healthChecker)

	// Register breaker action callbacks
	gw.evolution.breaker.OnAction(cogmetrics.ActionTriggerCompression, func() {
		slog.Warn("coghealth: breaker triggered compression")
	})
	gw.evolution.breaker.OnAction(cogmetrics.ActionPauseAndAskUser, func() {
		slog.Warn("coghealth: breaker requesting user intervention")
	})
	gw.evolution.breaker.OnAction(cogmetrics.ActionDegradeToSimple, func() {
		slog.Warn("coghealth: breaker degrading to simple mode")
	})

	slog.Info("cognitive health checker and circuit breaker initialized")
}
