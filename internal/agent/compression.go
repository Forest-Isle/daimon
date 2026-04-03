package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// CompressionLayer is a single step in the progressive compression pipeline.
type CompressionLayer interface {
	Name() string
	Compress(ctx context.Context, sess *session.Session, systemPrompt string) error
}

// CompressionPipeline runs compression layers progressively based on context utilization.
type CompressionPipeline struct {
	layers        []layerEntry
	provider      Provider
	model         string
	cfg           config.CompressionConfig
	contextWindow int // model's context window in tokens
}

type layerEntry struct {
	thresholdPct int
	layer        CompressionLayer
}

// NewCompressionPipeline creates a pipeline with the standard 4 layers.
func NewCompressionPipeline(
	provider Provider,
	model string,
	cfg config.CompressionConfig,
	resultStore *tool.ResultStore,
	contextWindow int,
) *CompressionPipeline {
	if contextWindow <= 0 {
		contextWindow = 200000 // default for Claude
	}
	if cfg.TokenEstimateRatio <= 0 {
		cfg.TokenEstimateRatio = 0.25
	}

	p := &CompressionPipeline{
		provider:      provider,
		model:         model,
		cfg:           cfg,
		contextWindow: contextWindow,
	}

	// Register layers in order with their thresholds
	p.layers = []layerEntry{
		{cfg.Layers.ToolEvictionPct, &ToolEvictionLayer{resultStore: resultStore, thresholdBytes: 8192}},
		{cfg.Layers.SummarizePct, &TurnSummarizationLayer{provider: provider, model: model}},
		{cfg.Layers.SlimPromptPct, &OldContextRemovalLayer{}},
		{cfg.Layers.EmergencyPct, &EmergencyTruncationLayer{keepLastTurns: 10}},
	}

	return p
}

// Run executes the compression pipeline, running layers progressively.
// Returns nil if no compression was needed.
func (p *CompressionPipeline) Run(ctx context.Context, sess *session.Session, systemPrompt string) error {
	for _, entry := range p.layers {
		utilization := p.estimateUtilization(sess, systemPrompt)
		if utilization < float64(entry.thresholdPct)/100.0 {
			slog.Debug("compression: utilization below threshold, stopping",
				"utilization_pct", int(utilization*100),
				"threshold_pct", entry.thresholdPct,
			)
			return nil // early exit
		}

		slog.Info("compression: running layer",
			"layer", entry.layer.Name(),
			"utilization_pct", int(utilization*100),
			"threshold_pct", entry.thresholdPct,
		)

		if err := entry.layer.Compress(ctx, sess, systemPrompt); err != nil {
			slog.Warn("compression: layer failed", "layer", entry.layer.Name(), "err", err)
			// Continue to next layer on failure
		}
	}
	return nil
}

// estimateUtilization estimates context token usage as a fraction of the model's context window.
func (p *CompressionPipeline) estimateUtilization(sess *session.Session, systemPrompt string) float64 {
	totalChars := len(systemPrompt)
	for _, m := range sess.History() {
		totalChars += len(m.Content) + len(m.ToolInput) + 20 // 20 chars overhead for role/metadata
	}
	return EstimateUtilization(totalChars, p.cfg.TokenEstimateRatio, p.contextWindow)
}

// EstimateUtilization computes estimated token utilization from character count, ratio, and context window.
func EstimateUtilization(totalChars int, ratio float64, contextWindow int) float64 {
	return float64(totalChars) * ratio / float64(contextWindow)
}

// --- Layer 1: Tool Eviction ---

// ToolEvictionLayer replaces large inline tool results with truncated previews.
type ToolEvictionLayer struct {
	resultStore    *tool.ResultStore
	thresholdBytes int
}

func (l *ToolEvictionLayer) Name() string { return "tool_eviction" }

func (l *ToolEvictionLayer) Compress(_ context.Context, sess *session.Session, _ string) error {
	history := sess.History()
	modified := false

	for i, m := range history {
		if m.Role == "tool_result" && len(m.Content) > l.thresholdBytes {
			// Truncate inline — if resultStore is available, persist first
			if l.resultStore != nil && l.resultStore.ShouldPersist(m.Content) {
				stored, err := l.resultStore.Store(sess.ID, m.ID, m.Content)
				if err == nil {
					history[i].Content = stored.Preview
					modified = true
					continue
				}
				slog.Warn("compression: failed to persist tool result", "err", err)
			}
			// Fallback: just truncate
			preview := tool.TruncateAtLineBoundary(m.Content, 2000)
			history[i].Content = preview + "\n[TRUNCATED for context management]"
			modified = true
		}
	}

	if modified {
		// Rebuild session history since History() returns a copy
		sess.TrimHistory(0)
		for _, m := range history {
			sess.AddMessage(m)
		}
		slog.Info("compression: evicted large tool results")
	}
	return nil
}

// --- Layer 2: Turn Summarization ---

// TurnSummarizationLayer summarizes old conversation turns using an LLM call.
type TurnSummarizationLayer struct {
	provider Provider
	model    string
}

func (l *TurnSummarizationLayer) Name() string { return "turn_summarization" }

func (l *TurnSummarizationLayer) Compress(ctx context.Context, sess *session.Session, _ string) error {
	history := sess.History()
	if len(history) <= 10 {
		return nil // not enough history to summarize
	}

	// Summarize the older half, keeping recent messages intact
	cutoff := len(history) / 2

	// Don't split tool_use/tool_result pairs — collect tool_use IDs in the kept portion
	keepToolUseIDs := make(map[string]bool)
	for _, m := range history[cutoff:] {
		if m.Role == "tool_use" {
			keepToolUseIDs[m.ID] = true
		}
	}
	// Move cutoff forward past any orphaned tool_results
	for cutoff < len(history) {
		m := history[cutoff]
		if m.Role == "tool_result" && !keepToolUseIDs[m.ToolName] {
			cutoff++
			continue
		}
		break
	}

	oldMessages := history[:cutoff]
	var sb strings.Builder
	for _, m := range oldMessages {
		content := m.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, content))
	}

	req := CompletionRequest{
		Model:  l.model,
		System: "Summarize the following conversation history concisely, preserving key facts, decisions, and context needed for continuing the conversation.",
		Messages: []CompletionMessage{
			{Role: "user", Content: sb.String()},
		},
		MaxTokens: 1024,
	}

	resp, err := l.provider.Complete(ctx, req)
	if err != nil {
		return fmt.Errorf("summarization LLM call: %w", err)
	}

	// Replace old messages with summary
	remaining := history[cutoff:]
	sess.TrimHistory(0)
	sess.AddMessage(session.Message{
		ID:      fmt.Sprintf("compact_%d", cutoff),
		Role:    "user",
		Content: "[Previous conversation summary]: " + resp.Text,
	})
	for _, m := range remaining {
		sess.AddMessage(m)
	}

	slog.Info("compression: summarized old turns", "summarized_count", len(oldMessages))
	return nil
}

// --- Layer 3: Old Context Removal ---

// OldContextRemovalLayer removes old user/assistant message pairs to reduce context size.
// This trims messages that contain context which can be re-retrieved (memories, skill metadata).
type OldContextRemovalLayer struct{}

func (l *OldContextRemovalLayer) Name() string { return "old_context_removal" }

func (l *OldContextRemovalLayer) Compress(_ context.Context, sess *session.Session, _ string) error {
	history := sess.History()
	if len(history) <= 6 {
		return nil
	}

	// Remove the oldest third of messages (after any existing summary)
	removeCount := len(history) / 3
	if removeCount < 2 {
		removeCount = 2
	}

	remaining := history[removeCount:]
	sess.TrimHistory(0)
	sess.AddMessage(session.Message{
		ID:      "context_trimmed",
		Role:    "user",
		Content: "[Earlier context was trimmed to manage conversation length]",
	})
	for _, m := range remaining {
		sess.AddMessage(m)
	}

	slog.Info("compression: removed old context", "removed_count", removeCount)
	return nil
}

// --- Layer 4: Emergency Truncation ---

// EmergencyTruncationLayer drops the oldest messages, keeping only the last N turns.
type EmergencyTruncationLayer struct {
	keepLastTurns int
}

func (l *EmergencyTruncationLayer) Name() string { return "emergency_truncation" }

func (l *EmergencyTruncationLayer) Compress(_ context.Context, sess *session.Session, _ string) error {
	history := sess.History()
	keepLast := l.keepLastTurns * 2 // each turn is roughly 2 messages (user + assistant)
	if keepLast <= 0 {
		keepLast = 20
	}

	if len(history) <= keepLast {
		return nil
	}

	remaining := history[len(history)-keepLast:]
	sess.TrimHistory(0)
	sess.AddMessage(session.Message{
		ID:      "emergency_trim",
		Role:    "user",
		Content: "[Context was heavily truncated due to length. Only recent messages remain.]",
	})
	for _, m := range remaining {
		sess.AddMessage(m)
	}

	slog.Info("compression: emergency truncation", "kept", keepLast)
	return nil
}
