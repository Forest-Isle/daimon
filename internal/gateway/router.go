package gateway

import (
	"context"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/channel"
)

// CommandHandler processes a slash command and returns a response string.
type CommandHandler func(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) (response string, err error)

type commandEntry struct {
	handler CommandHandler
	exact   bool // true = exact match only, false = prefix match (for commands with args)
}

// commandTable maps slash command names to their handlers.
// Populated in Gateway.New().
type commandTable map[string]commandEntry

// dispatch tries to match msg.Text against registered commands.
// Returns (response, true) if a command was matched, ("", false) otherwise.
func (ct commandTable) dispatch(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) (string, bool) {
	text := msg.Text

	// 1. Exact match
	if entry, ok := ct[text]; ok && entry.exact {
		resp, err := entry.handler(ctx, ch, msg)
		if err != nil {
			return "Error: " + err.Error(), true
		}
		return resp, true
	}

	// 2. Prefix match (commands with arguments like "/feature enable dashboard")
	for prefix, entry := range ct {
		if !entry.exact && strings.HasPrefix(text, prefix) {
			if text == prefix || strings.HasPrefix(text, prefix+" ") {
				resp, err := entry.handler(ctx, ch, msg)
				if err != nil {
					return "Error: " + err.Error(), true
				}
				return resp, true
			}
		}
	}

	return "", false
}
