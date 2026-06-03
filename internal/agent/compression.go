package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
	tokenizer     Tokenizer
}

type layerEntry struct {
	thresholdPct int
	layer        CompressionLayer
}

// NewCompressionPipeline creates a pipeline with the standard 3 layers.
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
		tokenizer:     NewTokenizer(model, cfg.TokenEstimateRatio),
	}

	// Register layers in order with their thresholds.
	// Layer 0: Reduce tool outputs — merge prune + eviction into one pass.
	// Layer 1: Summarize old turns via LLM (preserves semantic meaning).
	// Layer 2: Emergency truncation — soft trim then hard cut.
	p.layers = []layerEntry{
		{cfg.Layers.ToolOutputReducePct, NewToolOutputReducer(resultStore, ToolOutputReduceConfig{
			TruncateChars: 2000,
			EvictBytes:    8192,
			KeepLastTurns: 4,
		})},
		{cfg.Layers.SummarizePct, &TurnSummarizationLayer{provider: provider, model: model}},
		{cfg.Layers.EmergencyPct, NewEmergencyTruncator(EmergencyTruncateConfig{
			SoftKeepTurns: 15,
			HardKeepTurns: 5,
		})},
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
			break // stop running layers, but always repair pairing below
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

	// After all compression layers, ensure tool_use/tool_result pairing integrity.
	// This runs unconditionally because any layer may have created orphans.
	// Prevents API errors like "No tool call found for function call output".
	ensureToolPairing(sess)

	return nil
}

// RunForced runs compression layers unconditionally, skipping threshold checks.
// Used for reactive compression after API errors (413, context_length_exceeded).
func (p *CompressionPipeline) RunForced(ctx context.Context, sess *session.Session, systemPrompt string) error {
	for _, entry := range p.layers {
		if err := entry.layer.Compress(ctx, sess, systemPrompt); err != nil {
			slog.Warn("compression: forced layer failed", "layer", entry.layer.Name(), "err", err)
		}
	}
	ensureToolPairing(sess)
	return nil
}

// estimateUtilization estimates context token usage as a fraction of the model's context window.
func (p *CompressionPipeline) estimateUtilization(sess *session.Session, systemPrompt string) float64 {
	if p.tokenizer != nil {
		tokens := p.tokenizer.CountMessages(sess.History(), systemPrompt)
		return float64(tokens) / float64(p.contextWindow)
	}
	return EstimateUtilization(countContextChars(sess, systemPrompt), p.cfg.TokenEstimateRatio, p.contextWindow)
}

// EstimateUtilization computes estimated token utilization from character count, ratio, and context window.
func EstimateUtilization(totalChars int, ratio float64, contextWindow int) float64 {
	return float64(totalChars) * ratio / float64(contextWindow)
}

// --- Tool Output Reducer ---

// ToolOutputReduceConfig configures the combined tool output reduction layer.
type ToolOutputReduceConfig struct {
	TruncateChars int // truncate tool outputs exceeding this char count
	EvictBytes    int // evict tool outputs exceeding this byte count to ResultStore
	KeepLastTurns int // keep recent turns untouched
}

// ToolOutputReducer merges former tool_output_prune + tool_eviction layers into
// a single pass: truncates old tool outputs, and evicts very large ones to disk.
type ToolOutputReducer struct {
	store  *tool.ResultStore
	config ToolOutputReduceConfig
}

// NewToolOutputReducer creates a ToolOutputReducer.
func NewToolOutputReducer(store *tool.ResultStore, cfg ToolOutputReduceConfig) *ToolOutputReducer {
	return &ToolOutputReducer{store: store, config: cfg}
}

func (r *ToolOutputReducer) Name() string { return "tool_output_reduce" }

func (r *ToolOutputReducer) Compress(_ context.Context, sess *session.Session, _ string) error {
	history := sess.History()
	if len(history) == 0 {
		return nil
	}

	// Count user messages from the end to find the recent-turns boundary.
	turnsSeen := 0
	pruneBefore := 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			turnsSeen++
			if turnsSeen >= r.config.KeepLastTurns {
				pruneBefore = i
				break
			}
		}
	}

	modified := false
	for i := 0; i < pruneBefore; i++ {
		msg := &history[i]
		if msg.Role != "tool_result" || len(msg.Content) <= r.config.TruncateChars {
			continue
		}
		if len(msg.Content) > r.config.EvictBytes && r.store != nil && r.store.ShouldPersist(msg.Content) {
			stored, err := r.store.Store(sess.ID, msg.ID, msg.Content)
			if err == nil {
				history[i].Content = stored.Preview
				modified = true
				continue
			}
			slog.Warn("compression: failed to store tool result", "err", err)
		}
		// Fallback: truncate with preview
		preview := msg.Content[:500]
		history[i].Content = fmt.Sprintf("%s\n...[truncated, original: %d chars]", preview, len(msg.Content))
		modified = true
	}

	if modified {
		sess.TrimHistory(0)
		for _, m := range history {
			sess.AddMessage(m)
		}
		slog.Info("compression: reduced tool outputs", "pruned_to", pruneBefore)
	}
	return nil
}

// --- Turn Summarization ---

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

	// Build prompt with previous summary for incremental updates
	if prevSummary := sess.GetPreviousSummary(); prevSummary != "" {
		_, _ = fmt.Fprintf(&sb, "Here is the previous summary:\n%s\n\nPlease update it with the following new turns:\n", prevSummary)
	} else {
		sb.WriteString("Please summarize the following conversation turns:\n")
	}
	for _, m := range oldMessages {
		content := m.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		_, _ = fmt.Fprintf(&sb, "[%s]: %s\n", m.Role, content)
	}

	req := CompletionRequest{
		Model:  l.model,
		System: "Summarize the following conversation history concisely, preserving key facts, decisions, and context needed for continuing the conversation. If a previous summary is provided, update it incrementally rather than rewriting from scratch.",
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

	// Persist summary for future incremental updates
	sess.SetPreviousSummary(resp.Text)

	slog.Info("compression: summarized old turns", "summarized_count", len(oldMessages))
	return nil
}

// --- Emergency Truncator ---

// EmergencyTruncateConfig configures the combined emergency truncation layer.
type EmergencyTruncateConfig struct {
	SoftKeepTurns int // soft trim: keep recent N turns
	HardKeepTurns int // hard cut: keep recent N turns when history is very large
}

// EmergencyTruncator merges old_context_removal + emergency_truncation layers.
// When the history is extremely large (over 4x SoftKeepTurns messages) it performs
// a hard cut to HardKeepTurns; otherwise it soft-trims to SoftKeepTurns.
type EmergencyTruncator struct {
	config EmergencyTruncateConfig
}

// NewEmergencyTruncator creates an EmergencyTruncator.
func NewEmergencyTruncator(cfg EmergencyTruncateConfig) *EmergencyTruncator {
	return &EmergencyTruncator{config: cfg}
}

func (e *EmergencyTruncator) Name() string { return "emergency_truncation" }

func (e *EmergencyTruncator) Compress(_ context.Context, sess *session.Session, _ string) error {
	history := sess.History()

	keep := e.config.SoftKeepTurns * 2
	// If history is extremely large, use the aggressive hard cut.
	if len(history) > e.config.SoftKeepTurns*4 {
		keep = e.config.HardKeepTurns * 2
	}

	if len(history) <= keep {
		return nil
	}

	remaining := history[len(history)-keep:]
	sess.TrimHistory(0)
	sess.AddMessage(session.Message{
		ID:      "emergency_trim",
		Role:    "user",
		Content: "[Context was trimmed to manage conversation length. Only recent messages remain.]",
	})
	for _, m := range remaining {
		sess.AddMessage(m)
	}

	slog.Info("compression: emergency truncation", "kept", keep, "removed", len(history)-keep)
	return nil
}

// --- Tool Use/Result Pairing Integrity ---

// ensureToolPairing repairs tool_use/tool_result pairing after compression.
//
// It enforces two invariants expected by LLM APIs:
//  1. Every tool_result must reference an existing tool_use (orphan results removed).
//  2. Every tool_use must be followed by a tool_result (stub inserted if missing).
//
// The ToolName field on tool_result messages stores the ID of the corresponding
// tool_use message, which is how pairing is determined.
func ensureToolPairing(sess *session.Session) {
	history := sess.History()
	if len(history) == 0 {
		return
	}

	// Pass 1: collect all tool_use IDs and all tool_result->tool_use mappings.
	toolUseIDs := make(map[string]bool, len(history)/4)
	matchedToolUses := make(map[string]bool, len(history)/4)
	for _, m := range history {
		switch m.Role {
		case "tool_use":
			toolUseIDs[m.ID] = true
		case "tool_result":
			matchedToolUses[m.ToolName] = true
		}
	}

	// Pass 2: rebuild the history, removing orphan results and inserting stubs.
	repaired := make([]session.Message, 0, len(history))
	changed := false

	for _, m := range history {
		switch m.Role {
		case "tool_result":
			if !toolUseIDs[m.ToolName] {
				// Orphan tool_result — its tool_use was removed by compression.
				slog.Debug("compression: removed orphan tool_result", "tool_use_id", m.ToolName)
				changed = true
				continue
			}
			repaired = append(repaired, m)

		case "tool_use":
			repaired = append(repaired, m)
			if !matchedToolUses[m.ID] {
				// Missing tool_result — insert a stub so the API doesn't error.
				repaired = append(repaired, session.Message{
					ID:        m.ID + "_stub",
					Role:      "tool_result",
					Content:   "[result pruned during compression]",
					ToolName:  m.ID,
					CreatedAt: time.Now(),
				})
				slog.Debug("compression: inserted stub tool_result", "tool_use_id", m.ID)
				changed = true
			}

		default:
			repaired = append(repaired, m)
		}
	}

	if changed {
		sess.TrimHistory(0)
		for _, m := range repaired {
			sess.AddMessage(m)
		}
		slog.Info("compression: repaired tool_use/tool_result pairing",
			"original_count", len(history),
			"repaired_count", len(repaired),
		)
	}
}
