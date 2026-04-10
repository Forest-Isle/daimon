package hook

import (
	"context"
	"log/slog"
)

// MessagePreserver implements PreCompactHandler by marking messages that match
// configured patterns for preservation during context compression.
type MessagePreserver struct {
	patterns []string
}

// NewMessagePreserver creates a handler that preserves messages matching any of
// the given substring patterns.
func NewMessagePreserver(patterns []string) *MessagePreserver {
	return &MessagePreserver{patterns: patterns}
}

func (h *MessagePreserver) OnPreCompact(_ context.Context, event PreCompactEvent) (PreCompactResult, error) {
	slog.Debug("hook: pre-compact preserver check",
		"session_id", event.SessionID,
		"message_count", event.MessageCount,
		"patterns", len(h.patterns),
	)

	// Without access to actual message content in the event, we return an
	// empty preservation list. When the event is extended with message bodies,
	// pattern matching can be added here.
	return PreCompactResult{
		PreserveMessageIDs: nil,
	}, nil
}
