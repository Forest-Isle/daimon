package gateway

import "github.com/Forest-Isle/IronClaw/internal/cogmetrics"

// initCogMetrics creates the cognitive-metrics collector and wires it into the
// evolution engine. It is idempotent: if the collector was already created (e.g.
// because setupDashboard ran first), it returns immediately without double-
// registering the hook.
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
}
