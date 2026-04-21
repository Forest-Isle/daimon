package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/channel"
)

// handleFeatureCommand processes /feature [list|enable|disable] [name].
func (gw *Gateway) handleFeatureCommand(ctx context.Context, ch channel.Channel, msg channel.InboundMessage, args string) {
	if gw.features == nil {
		gw.sendReply(ctx, ch, msg, "Feature registry not initialized.")
		return
	}

	switch {
	case args == "" || args == "list":
		gw.sendFeatureList(ctx, ch, msg)

	case strings.HasPrefix(args, "enable "):
		name := strings.TrimPrefix(args, "enable ")
		if err := gw.features.Enable(ctx, strings.TrimSpace(name)); err != nil {
			gw.sendReply(ctx, ch, msg, fmt.Sprintf("❌ %v", err))
		} else {
			gw.sendReply(ctx, ch, msg, fmt.Sprintf("✅ Feature %q enabled.", strings.TrimSpace(name)))
		}

	case strings.HasPrefix(args, "disable "):
		name := strings.TrimPrefix(args, "disable ")
		if err := gw.features.Disable(ctx, strings.TrimSpace(name)); err != nil {
			gw.sendReply(ctx, ch, msg, fmt.Sprintf("❌ %v", err))
		} else {
			gw.sendReply(ctx, ch, msg, fmt.Sprintf("❌ Feature %q disabled.", strings.TrimSpace(name)))
		}

	default:
		gw.sendReply(ctx, ch, msg, "Usage: /feature [list|enable <name>|disable <name>]")
	}
}

// sendFeatureList formats and sends the current feature list.
func (gw *Gateway) sendFeatureList(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) {
	features := gw.features.List()
	if len(features) == 0 {
		gw.sendReply(ctx, ch, msg, "No features registered.")
		return
	}

	var b strings.Builder
	b.WriteString("📋 Features\n\n")
	for _, f := range features {
		if f.Enabled {
			fmt.Fprintf(&b, "  ✅ %s — %s\n", f.Name, f.Description)
		} else {
			line := fmt.Sprintf("  ❌ %s — %s", f.Name, f.Description)
			if f.Reason != "" {
				line += fmt.Sprintf(" (%s)", f.Reason)
			}
			b.WriteString(line + "\n")
		}
	}
	gw.sendReply(ctx, ch, msg, b.String())
}

// handleConfigCommand shows current effective configuration.
func (gw *Gateway) handleConfigCommand(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) {
	var b strings.Builder
	b.WriteString("⚙️ Current Configuration\n\n")
	fmt.Fprintf(&b, "  Provider:       %s\n", gw.cfg.LLM.Provider)
	fmt.Fprintf(&b, "  Model:          %s\n", gw.cfg.LLM.Model)
	fmt.Fprintf(&b, "  Max Tokens:     %d\n", gw.cfg.LLM.MaxTokens)
	fmt.Fprintf(&b, "  Agent Mode:     %s\n", gw.currentMode.Load().(string))
	fmt.Fprintf(&b, "  Max Iterations: %d\n", gw.cfg.Agent.MaxIterations)

	if gw.features != nil {
		enabled := 0
		for _, f := range gw.features.List() {
			if f.Enabled {
				enabled++
			}
		}
		fmt.Fprintf(&b, "  Features:       %d enabled\n", enabled)
	}

	gw.sendReply(ctx, ch, msg, b.String())
}

// handleCompactCommand triggers manual context compression for the current session.
func (gw *Gateway) handleCompactCommand(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) {
	if gw.contextMgr == nil {
		gw.sendReply(ctx, ch, msg, "Context compression is not configured.")
		return
	}

	sess, err := gw.sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		gw.sendReply(ctx, ch, msg, fmt.Sprintf("⚠️ Failed to get session: %v", err))
		return
	}

	beforeCount := len(sess.History())

	// TODO: pass actual system prompt once accessible from gateway context
	compressed, err := gw.contextMgr.Compress(ctx, sess, "")
	if err != nil {
		gw.sendReply(ctx, ch, msg, fmt.Sprintf("⚠️ Compression error: %v", err))
		return
	}

	afterCount := len(sess.History())

	if !compressed {
		gw.sendReply(ctx, ch, msg, fmt.Sprintf("ℹ️ No compression needed (current: %d messages).", beforeCount))
		return
	}

	if err := gw.sessions.Persist(ctx, sess); err != nil {
		slog.Warn("gateway: failed to persist after compact", "err", err)
	}

	gw.sendReply(ctx, ch, msg, fmt.Sprintf("✅ Compressed: %d → %d messages.", beforeCount, afterCount))
}

// handleModelCommand shows or switches the current LLM model.
func (gw *Gateway) handleModelCommand(ctx context.Context, ch channel.Channel, msg channel.InboundMessage, args string) {
	if args == "" {
		gw.sendReply(ctx, ch, msg, fmt.Sprintf("ℹ️ Current model: %s (provider: %s)", gw.cfg.LLM.Model, gw.cfg.LLM.Provider))
		return
	}

	old := gw.cfg.LLM.Model
	gw.cfg.LLM.Model = args
	gw.sendReply(ctx, ch, msg, fmt.Sprintf("✅ Model switched: %s → %s", old, args))
}

// sendReply is a convenience helper to send a text reply.
func (gw *Gateway) sendReply(ctx context.Context, ch channel.Channel, msg channel.InboundMessage, text string) {
	_ = ch.Send(ctx, channel.OutboundMessage{
		Channel:   msg.Channel,
		ChannelID: msg.ChannelID,
		Text:      text,
	})
}
