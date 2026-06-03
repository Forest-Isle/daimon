package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/config"
	ierrors "github.com/Forest-Isle/IronClaw/internal/errors"
	"github.com/Forest-Isle/IronClaw/internal/util"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

const (
	dynamicContextMarker = "<!-- DYNAMIC_CONTEXT -->"
	cacheBoundaryMarker  = "<!-- CACHE_BOUNDARY -->"
	compactionThreshold  = 40 // Trigger compaction when history exceeds this
)

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
	tokenizer       Tokenizer
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
		tokenizer:     NewTokenizer(model, ratio),
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
	if cm.tokenizer != nil {
		tokens := cm.tokenizer.CountMessages(sess.History(), systemPrompt)
		return float64(tokens) / float64(cm.contextWindow)
	}
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

// SplitSystemPrompt splits the prompt at a cache boundary marker.
// It checks for CACHE_BOUNDARY first, then falls back to DYNAMIC_CONTEXT.
// Everything before the marker is static (cacheable), everything after is dynamic.
// If neither marker is present the entire prompt is static.
func (cm *PipelineContextManager) SplitSystemPrompt(full string) (static, dynamic string) {
	// Prefer CACHE_BOUNDARY — it gives explicit control over where the split happens.
	if idx := strings.Index(full, cacheBoundaryMarker); idx >= 0 {
		return full[:idx], full[idx+len(cacheBoundaryMarker):]
	}
	if idx := strings.Index(full, dynamicContextMarker); idx >= 0 {
		return full[:idx], full[idx+len(dynamicContextMarker):]
	}
	return full, ""
}

// ReactiveCompressWithRetry implements a multi-level 413 recovery chain:
//  1. RunForced — run all compression layers unconditionally, then retry.
//  2. Reduce maxTokens — create a request with fewer output tokens.
//  3. Final error — if still failing, return an actionable error.
//
// The retryFn callback should re-send the API request and return the error
// (nil on success). The maxTokens parameter is the current request maxTokens.
func (cm *PipelineContextManager) ReactiveCompressWithRetry(
	ctx context.Context,
	sess *session.Session,
	systemPrompt string,
	maxTokens int,
	retryFn func(ctx context.Context, maxTokens int) error,
) error {
	// Level 1: forced compression → retry with original maxTokens.
	if err := cm.ReactiveCompress(ctx, sess, systemPrompt); err != nil {
		slog.Warn("reactive_compress_retry: forced compression failed", "err", err)
	}
	if err := retryFn(ctx, maxTokens); err == nil {
		slog.Info("reactive_compress_retry: succeeded after forced compression")
		return nil
	}

	// Level 2: halve maxTokens and retry.
	reducedMax := maxTokens / 2
	if reducedMax < 1024 {
		reducedMax = 1024
	}
	slog.Info("reactive_compress_retry: retrying with reduced maxTokens",
		"original", maxTokens, "reduced", reducedMax)
	if err := retryFn(ctx, reducedMax); err == nil {
		slog.Info("reactive_compress_retry: succeeded with reduced maxTokens", "max_tokens", reducedMax)
		return nil
	}

	// Level 3: give up with a descriptive error.
	util := cm.Utilization(sess, systemPrompt)
	return ierrors.Wrap(
		fmt.Errorf(
			"compression and token reduction failed "+
				"(utilization=%.0f%%, messages=%d, context_window=%d)",
			util*100, len(sess.History()), cm.contextWindow,
		),
		ierrors.KindContextLength,
		"context_length_exceeded",
	)
}

// aboveMinThreshold returns true if utilization exceeds the lowest layer threshold.
func (cm *PipelineContextManager) aboveMinThreshold(utilization float64) bool {
	if cm.minThresholdPct == 0 {
		return false
	}
	return utilization >= float64(cm.minThresholdPct)/100.0
}

// CompactHistory summarizes old messages to keep context manageable.
func CompactHistory(ctx context.Context, provider Provider, sess *session.Session, model string) error {
	history := sess.History()
	if len(history) <= compactionThreshold {
		return nil
	}

	// Take the older half of messages for summarization.
	// Find a safe cutoff that doesn't split tool_use/tool_result pairs.
	cutoff := len(history) / 2

	// Collect tool_use IDs in the portion we want to keep (cutoff onward)
	keepToolUseIDs := make(map[string]bool)
	for _, m := range history[cutoff:] {
		if m.Role == "tool_use" {
			keepToolUseIDs[m.ID] = true
		}
	}

	// Move cutoff forward to include any tool_result whose tool_use is in the kept portion,
	// and skip any orphaned tool_result at the boundary.
	for cutoff < len(history) {
		m := history[cutoff]
		if m.Role == "tool_result" && !keepToolUseIDs[m.ToolName] {
			// This tool_result's tool_use is in the old portion — include it in the summary
			cutoff++
			continue
		}
		break
	}

	oldMessages := history[:cutoff]

	// Build summarization prompt, incorporating previous summary for incremental updates
	var sb strings.Builder
	if prevSummary := sess.GetPreviousSummary(); prevSummary != "" {
		_, _ = fmt.Fprintf(&sb, "Here is the previous summary:\n%s\n\nPlease update it with the following new turns:\n", prevSummary)
	} else {
		sb.WriteString("Please summarize the following conversation turns:\n")
	}
	for _, m := range oldMessages {
		_, _ = fmt.Fprintf(&sb, "[%s]: %s\n", m.Role, util.TruncateStr(m.Content, 500))
	}

	req := CompletionRequest{
		Model:  model,
		System: "Summarize the following conversation history concisely, preserving key facts, decisions, and context needed for continuing the conversation. If a previous summary is provided, update it incrementally rather than rewriting from scratch.",
		Messages: []CompletionMessage{
			{Role: "user", Content: sb.String()},
		},
		MaxTokens: 1024,
	}

	resp, err := provider.Complete(ctx, req)
	if err != nil {
		return fmt.Errorf("compaction llm call: %w", err)
	}

	// Replace old messages with a single summary message
	sess.TrimHistory(len(history) - cutoff)

	// Prepend summary as a system-like user message
	summary := session.Message{
		ID:      fmt.Sprintf("compact_%d", cutoff),
		Role:    "user",
		Content: "[Previous conversation summary]: " + resp.Text,
	}

	// We need to insert at the beginning — rebuild
	remaining := sess.History()
	sess.TrimHistory(0) // clear
	sess.AddMessage(summary)
	for _, m := range remaining {
		sess.AddMessage(m)
	}

	// Persist summary for future incremental updates
	sess.SetPreviousSummary(resp.Text)

	slog.Info("history compacted", "session", sess.ID, "old_count", len(oldMessages), "summary_len", len(resp.Text))
	return nil
}
