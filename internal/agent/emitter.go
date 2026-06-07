package agent

// ObservabilityEmitter emits agent lifecycle events for observability consumers
// (e.g. the TUI status bar). Implementations must be safe for concurrent
// use. All methods are no-ops when the receiver is nil, so callers need not nil-check.
type ObservabilityEmitter interface {
	EmitToolStart(sessionID, toolName, input string)
	EmitToolEnd(sessionID, toolName string, succeeded bool, durationMs int64)
	EmitSubAgentSpawn(sessionID, parentSessionID, agentName, task string)
	EmitSubAgentComplete(sessionID, agentName string, succeeded bool, durationMs int64)
	EmitContextCompress(sessionID, reason string, layersRun int, beforePct, afterPct float64)
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

// multiEmitter fans out ObservabilityEmitter calls to multiple backends,
// allowing several consumers to receive events simultaneously.
type multiEmitter struct {
	targets []ObservabilityEmitter
}

// NewMultiEmitter combines multiple ObservabilityEmitter instances into one.
// Nil entries are silently dropped.
func NewMultiEmitter(emitters ...ObservabilityEmitter) ObservabilityEmitter {
	var live []ObservabilityEmitter
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

func (m *multiEmitter) EmitSubAgentSpawn(sessionID, parentSessionID, agentName, task string) {
	for _, t := range m.targets {
		t.EmitSubAgentSpawn(sessionID, parentSessionID, agentName, task)
	}
}

func (m *multiEmitter) EmitSubAgentComplete(sessionID, agentName string, succeeded bool, durationMs int64) {
	for _, t := range m.targets {
		t.EmitSubAgentComplete(sessionID, agentName, succeeded, durationMs)
	}
}

func (m *multiEmitter) EmitContextCompress(sessionID, reason string, layersRun int, beforePct, afterPct float64) {
	for _, t := range m.targets {
		t.EmitContextCompress(sessionID, reason, layersRun, beforePct, afterPct)
	}
}
