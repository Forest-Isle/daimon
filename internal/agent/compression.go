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
		tokenizer:     NewTokenizer(model, cfg.TokenEstimateRatio),
	}

	// Register layers in order with their thresholds.
	// Layer 0: Pre-prune old tool outputs (cheap, runs first).
	// Layer 1: Evict large tool results to disk.
	// Layer 2: Summarize old turns via LLM.
	// Layer 3: Remove old context entirely.
	// Layer 4: Emergency truncation (keep only recent turns).
	p.layers = []layerEntry{
		{cfg.Layers.ToolEvictionPct, &ToolOutputPrePruneLayer{thresholdChars: 2000, keepRecentTurns: 4, previewChars: 500}},
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

// --- Layer 0: Tool Output Pre-Prune ---

// ToolOutputPrePruneLayer truncates old (non-recent) tool outputs that exceed a
// character threshold. This is a lightweight, non-destructive first pass that
// reduces context size before heavier compression layers kick in.
type ToolOutputPrePruneLayer struct {
	thresholdChars  int // tool outputs exceeding this are truncated
	keepRecentTurns int // number of recent turns to leave untouched
	previewChars    int // how many leading characters to preserve
}

func (l *ToolOutputPrePruneLayer) Name() string { return "tool_output_prune" }

func (l *ToolOutputPrePruneLayer) Compress(_ context.Context, sess *session.Session, _ string) error {
	history := sess.History()
	if len(history) == 0 {
		return nil
	}

	// Determine the boundary: messages before pruneBefore are eligible for pruning.
	// Count user messages from the end — each user message marks the start of a
	// conversation turn, regardless of how many tool calls follow.
	turnsSeen := 0
	pruneBefore := 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			turnsSeen++
			if turnsSeen >= l.keepRecentTurns {
				pruneBefore = i
				break
			}
		}
	}

	pruned := 0
	for i := 0; i < pruneBefore; i++ {
		m := &history[i]
		if m.Role != "tool_result" || len(m.Content) <= l.thresholdChars {
			continue
		}
		original := len(m.Content)
		preview := m.Content[:l.previewChars]
		m.Content = fmt.Sprintf("%s\n... [truncated, original: %d chars]", preview, original)
		pruned++
	}

	if pruned > 0 {
		// History() returns a copy, so we must rebuild the session messages.
		sess.TrimHistory(0)
		for _, m := range history {
			sess.AddMessage(m)
		}
		slog.Info("compression: pre-pruned old tool outputs", "truncated_count", pruned)
	}
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
// --- Context Budget Allocation ---

// ContextBudget defines per-complexity limits for context items injected into the cognitive state.
type ContextBudget struct {
	MemoryLimit           int
	KBLimit               int
	IncludeProjectContext bool
	IncludeGitState       bool
}

// ContextBudgetAllocator allocates context limits based on task complexity.
type ContextBudgetAllocator struct{}

// NewContextBudgetAllocator creates a new ContextBudgetAllocator.
func NewContextBudgetAllocator() *ContextBudgetAllocator {
	return &ContextBudgetAllocator{}
}

// Allocate returns the context budget for the given task complexity.
func (a *ContextBudgetAllocator) Allocate(complexity TaskComplexity) ContextBudget {
	switch complexity {
	case ComplexitySimple:
		return ContextBudget{
			MemoryLimit:           3,
			KBLimit:               0,
			IncludeProjectContext: true,
			IncludeGitState:       false,
		}
	case ComplexityComplex:
		return ContextBudget{
			MemoryLimit:           10,
			KBLimit:               5,
			IncludeProjectContext: true,
			IncludeGitState:       true,
		}
	default:
		return ContextBudget{
			MemoryLimit:           5,
			KBLimit:               3,
			IncludeProjectContext: true,
			IncludeGitState:       false,
		}
	}
}

// Apply truncates and prunes the cognitive state according to the budget
// derived from state.Goal.Complexity.
func (a *ContextBudgetAllocator) Apply(state *CognitiveState) {
	budget := a.Allocate(state.Goal.Complexity)

	if len(state.RelevantMemories) > budget.MemoryLimit {
		state.RelevantMemories = state.RelevantMemories[:budget.MemoryLimit]
	}

	if len(state.KnowledgeContext) > budget.KBLimit {
		state.KnowledgeContext = state.KnowledgeContext[:budget.KBLimit]
	}

	if !budget.IncludeProjectContext {
		state.ProjectCtx = nil
	}

	if !budget.IncludeGitState {
		state.GitState = nil
	}
}

func ensureToolPairing(sess *session.Session) {
	history := sess.History()
	if len(history) == 0 {
		return
	}

	// Pass 1: collect all tool_use IDs and all tool_result→tool_use mappings.
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
