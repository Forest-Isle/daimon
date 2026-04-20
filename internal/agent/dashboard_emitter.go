package agent

// DashboardEmitter emits agent lifecycle events for the web dashboard.
// Implementations must be safe for concurrent use. All methods are no-ops
// when the receiver is nil, so callers need not nil-check.
type DashboardEmitter interface {
	EmitPhaseStart(sessionID, phase string)
	EmitPhaseEnd(sessionID, phase string, durationMs int64)
	EmitToolStart(sessionID, toolName, input string)
	EmitToolEnd(sessionID, toolName string, succeeded bool, durationMs int64)
}

// MetricsEmitter pushes runtime metrics (iteration progress, context usage,
// cache stats) to a consumer such as the TUI status bar.
type MetricsEmitter interface {
	SendMetrics(iteration, maxIter int, utilization float64, cacheCreate, cacheRead int64)
}
