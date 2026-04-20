package agent

// DashboardEmitter emits agent lifecycle events for the web dashboard.
// Implementations must be safe for concurrent use. All methods are no-ops
// when the receiver is nil, so callers need not nil-check.
type DashboardEmitter interface {
	EmitPhaseStart(sessionID, phase string)
	EmitPhaseEnd(sessionID, phase string, durationMs int64)
	EmitToolStart(sessionID, toolName, input string)
	EmitToolEnd(sessionID, toolName string, succeeded bool, durationMs int64)
	EmitSessionStart(sessionID, channel string)
	EmitSessionEnd(sessionID string, succeeded bool, durationMs int64)
	EmitMetricsUpdate(sessionID string, iteration, maxIter int, utilization float64, inputTokens, outputTokens, cacheCreate, cacheRead int64, model, provider string)
}

// RuntimeMetrics is a point-in-time snapshot pushed to the TUI on every iteration.
type RuntimeMetrics struct {
	Iteration    int
	MaxIter      int
	Utilization  float64 // 0.0–1.0 context window usage
	CacheCreate  int64   // prompt cache creation tokens (cumulative)
	CacheRead    int64   // prompt cache read tokens (cumulative)
	InputTokens  int64   // total input tokens consumed (cumulative)
	OutputTokens int64   // total output tokens generated (cumulative)
	Model        string  // LLM model identifier
	Provider     string  // "claude" | "openai" | "openai-compatible"
}

// MetricsEmitter pushes runtime metrics (iteration progress, context usage,
// cache stats, token usage) to a consumer such as the TUI status bar.
type MetricsEmitter interface {
	SendMetrics(m RuntimeMetrics)
}

// multiEmitter fans out DashboardEmitter calls to multiple backends,
// allowing the web dashboard and TUI to receive events simultaneously.
type multiEmitter struct {
	targets []DashboardEmitter
}

// NewMultiEmitter combines multiple DashboardEmitter instances into one.
// Nil entries are silently dropped.
func NewMultiEmitter(emitters ...DashboardEmitter) DashboardEmitter {
	var live []DashboardEmitter
	for _, e := range emitters {
		if e == nil {
			continue
		}
		if me, ok := e.(*multiEmitter); ok {
			live = append(live, me.targets...)
		} else {
			live = append(live, e)
		}
	}
	switch len(live) {
	case 0:
		return nil
	case 1:
		return live[0]
	default:
		return &multiEmitter{targets: live}
	}
}

func (m *multiEmitter) EmitPhaseStart(sessionID, phase string) {
	for _, t := range m.targets {
		t.EmitPhaseStart(sessionID, phase)
	}
}

func (m *multiEmitter) EmitPhaseEnd(sessionID, phase string, durationMs int64) {
	for _, t := range m.targets {
		t.EmitPhaseEnd(sessionID, phase, durationMs)
	}
}

func (m *multiEmitter) EmitToolStart(sessionID, toolName, input string) {
	for _, t := range m.targets {
		t.EmitToolStart(sessionID, toolName, input)
	}
}

func (m *multiEmitter) EmitToolEnd(sessionID, toolName string, succeeded bool, durationMs int64) {
	for _, t := range m.targets {
		t.EmitToolEnd(sessionID, toolName, succeeded, durationMs)
	}
}

func (m *multiEmitter) EmitSessionStart(sessionID, channel string) {
	for _, t := range m.targets {
		t.EmitSessionStart(sessionID, channel)
	}
}

func (m *multiEmitter) EmitSessionEnd(sessionID string, succeeded bool, durationMs int64) {
	for _, t := range m.targets {
		t.EmitSessionEnd(sessionID, succeeded, durationMs)
	}
}

func (m *multiEmitter) EmitMetricsUpdate(sessionID string, iteration, maxIter int, utilization float64, inputTokens, outputTokens, cacheCreate, cacheRead int64, model, provider string) {
	for _, t := range m.targets {
		t.EmitMetricsUpdate(sessionID, iteration, maxIter, utilization, inputTokens, outputTokens, cacheCreate, cacheRead, model, provider)
	}
}
