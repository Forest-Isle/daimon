package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/session"
)

const compactionThreshold = 40 // Trigger compaction when history exceeds this

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

	// Build a summary request
	var sb strings.Builder
	for _, m := range oldMessages {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, truncate(m.Content, 500)))
	}

	req := CompletionRequest{
		Model:  model,
		System: "Summarize the following conversation history concisely, preserving key facts, decisions, and context needed for continuing the conversation.",
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

	slog.Info("history compacted", "session", sess.ID, "old_count", len(oldMessages), "summary_len", len(resp.Text))
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
