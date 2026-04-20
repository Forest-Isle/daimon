package agent

import (
	"context"
	"log/slog"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

const dynamicContextMarker = "<!-- DYNAMIC_CONTEXT -->"

// ContextManager abstracts context compression and utilization tracking.
type ContextManager interface {
	Compress(ctx context.Context, sess *session.Session, systemPrompt string) (bool, error)
	ReactiveCompress(ctx context.Context, sess *session.Session, systemPrompt string) error
	Utilization(sess *session.Session, systemPrompt string) float64
	SplitSystemPrompt(full string) (static, dynamic string)
}

// PipelineContextManager wraps CompressionPipeline and TokenBudget into the
// ContextManager interface. When no pipeline is configured it falls back to
// the legacy CompactHistory path.
type PipelineContextManager struct {
	pipeline        *CompressionPipeline
	provider        Provider
	model           string
	contextWindow   int
	ratio           float64
	minThresholdPct int // pre-computed from pipeline layers
	dashEmitter     DashboardEmitter
}

// SetDashboardEmitter attaches a dashboard emitter for context compression events.
func (cm *PipelineContextManager) SetDashboardEmitter(e DashboardEmitter) { cm.dashEmitter = e }

// NewPipelineContextManager creates a PipelineContextManager. If cfg is non-nil
// and has a "layered" strategy, a CompressionPipeline is built internally.
// Otherwise the manager falls back to CompactHistory.
func NewPipelineContextManager(
	provider Provider,
	model string,
	cfg *config.CompressionConfig,
	contextWindow int,
	resultStore *tool.ResultStore,
) *PipelineContextManager {
	if contextWindow <= 0 {
		contextWindow = 200000
	}

	ratio := 0.25
	var pipeline *CompressionPipeline

	if cfg != nil {
		if cfg.TokenEstimateRatio > 0 {
			ratio = cfg.TokenEstimateRatio
		}
		if cfg.Strategy == "layered" {
			pipeline = NewCompressionPipeline(provider, model, *cfg, resultStore, contextWindow)
		}
	}

	cm := &PipelineContextManager{
		pipeline:      pipeline,
		provider:      provider,
		model:         model,
		contextWindow: contextWindow,
		ratio:         ratio,
	}

	if pipeline != nil && len(pipeline.layers) > 0 {
		cm.minThresholdPct = pipeline.layers[0].thresholdPct
		for _, entry := range pipeline.layers[1:] {
			if entry.thresholdPct < cm.minThresholdPct {
				cm.minThresholdPct = entry.thresholdPct
			}
		}
	}

	return cm
}

// Compress runs the compression pipeline if utilization exceeds the lowest
// configured threshold. Returns true if compression was performed.
func (cm *PipelineContextManager) Compress(ctx context.Context, sess *session.Session, systemPrompt string) (bool, error) {
	if cm.pipeline != nil {
		util := cm.Utilization(sess, systemPrompt)
		if !cm.aboveMinThreshold(util) {
			return false, nil
		}

		slog.Info("context_manager: running compression pipeline",
			"utilization_pct", int(util*100),
		)
		if err := cm.pipeline.Run(ctx, sess, systemPrompt); err != nil {
			return true, err
		}
		if cm.dashEmitter != nil {
			afterUtil := cm.Utilization(sess, systemPrompt)
			cm.dashEmitter.EmitContextCompress(sess.ID, "proactive", len(cm.pipeline.layers), util, afterUtil)
		}
		return true, nil
	}

	// Legacy fallback: CompactHistory uses its own internal threshold (40 messages).
	history := sess.History()
	if len(history) <= compactionThreshold {
		return false, nil
	}

	slog.Info("context_manager: falling back to CompactHistory",
		"history_len", len(history),
	)
	err := CompactHistory(ctx, cm.provider, sess, cm.model)
	return true, err
}

// ReactiveCompress runs aggressive compression after an API error (e.g., 413).
// When a pipeline is configured it runs all layers unconditionally via RunForced;
// otherwise it falls back to the legacy CompactHistory path.
func (cm *PipelineContextManager) ReactiveCompress(ctx context.Context, sess *session.Session, systemPrompt string) error {
	if cm.pipeline != nil {
		err := cm.pipeline.RunForced(ctx, sess, systemPrompt)
		if err == nil && cm.dashEmitter != nil {
			afterUtil := cm.Utilization(sess, systemPrompt)
			cm.dashEmitter.EmitContextCompress(sess.ID, "reactive_413", len(cm.pipeline.layers), 1.0, afterUtil)
		}
		return err
	}
	return CompactHistory(ctx, cm.provider, sess, cm.model)
}

// Utilization estimates the fraction of the context window currently consumed.
func (cm *PipelineContextManager) Utilization(sess *session.Session, systemPrompt string) float64 {
	return EstimateUtilization(countContextChars(sess, systemPrompt), cm.ratio, cm.contextWindow)
}

// countContextChars sums the estimated character count for a session + system prompt.
func countContextChars(sess *session.Session, systemPrompt string) int {
	totalChars := len(systemPrompt)
	for _, m := range sess.History() {
		totalChars += len(m.Content) + len(m.ToolInput) + 20
	}
	return totalChars
}

// SplitSystemPrompt splits the prompt at the DYNAMIC_CONTEXT marker.
// Everything before the marker is static (cacheable), everything after is dynamic.
// If the marker is absent the entire prompt is static.
func (cm *PipelineContextManager) SplitSystemPrompt(full string) (static, dynamic string) {
	idx := strings.Index(full, dynamicContextMarker)
	if idx < 0 {
		return full, ""
	}
	return full[:idx], full[idx+len(dynamicContextMarker):]
}

// aboveMinThreshold returns true if utilization exceeds the lowest layer threshold.
func (cm *PipelineContextManager) aboveMinThreshold(utilization float64) bool {
	if cm.minThresholdPct == 0 {
		return false
	}
	return utilization >= float64(cm.minThresholdPct)/100.0
}
