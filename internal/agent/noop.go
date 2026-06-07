package agent

import (
	"context"

	"github.com/Forest-Isle/IronClaw/internal/session"
)

// discardEmitter is the zero-value ObservabilityEmitter. All methods are no-ops.
// Used when no observability emitter (e.g. the TUI) is configured.
type discardEmitter struct{}

func (discardEmitter) EmitToolStart(string, string, string)    {}
func (discardEmitter) EmitToolEnd(string, string, bool, int64) {}
func (discardEmitter) EmitSubAgentSpawn(string, string, string, string)          {}
func (discardEmitter) EmitSubAgentComplete(string, string, bool, int64)          {}
func (discardEmitter) EmitContextCompress(string, string, int, float64, float64) {}

// discardMetrics is the zero-value MetricsEmitter. All methods are no-ops.
type discardMetrics struct{}

func (discardMetrics) SendMetrics(RuntimeMetrics) {}

// noopContextManager implements ContextManager with no compression.
type noopContextManager struct{}

func (noopContextManager) Compress(_ context.Context, _ *session.Session, _ string) (bool, error) {
	return false, nil
}
func (noopContextManager) ReactiveCompress(_ context.Context, _ *session.Session, _ string) error {
	return nil
}
func (noopContextManager) Utilization(_ *session.Session, _ string) float64 { return 0 }
func (noopContextManager) SplitSystemPrompt(full string) (string, string)   { return full, "" }

// Compile-time interface checks
var _ ObservabilityEmitter = discardEmitter{}
var _ MetricsEmitter = discardMetrics{}
var _ ContextManager = noopContextManager{}
