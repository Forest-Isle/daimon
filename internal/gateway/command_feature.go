package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/feature"
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
		name := strings.TrimSpace(strings.TrimPrefix(args, "enable "))
		if err := gw.features.Enable(ctx, name); err != nil {
			gw.sendReply(ctx, ch, msg, fmt.Sprintf("❌ %v", err))
		} else {
			reply := fmt.Sprintf("✅ Feature %q enabled.", name)
			if info := gw.findFeatureInfo(name); info != nil && !info.HotReloadable {
				reply += "\n⚠️ Not hot-reloadable. Restart IronClaw for full effect."
			}
			gw.sendReply(ctx, ch, msg, reply)
		}

	case strings.HasPrefix(args, "disable "):
		name := strings.TrimSpace(strings.TrimPrefix(args, "disable "))
		if err := gw.features.Disable(ctx, name); err != nil {
			gw.sendReply(ctx, ch, msg, fmt.Sprintf("❌ %v", err))
		} else {
			reply := fmt.Sprintf("✅ Feature %q disabled.", name)
			if info := gw.findFeatureInfo(name); info != nil && !info.HotReloadable {
				reply += "\n⚠️ Not hot-reloadable. Restart IronClaw for full effect."
			}
			gw.sendReply(ctx, ch, msg, reply)
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
		icon := "❌"
		if f.Enabled {
			icon = "✅"
		}
		reload := ""
		if f.HotReloadable {
			reload = " 🔄"
		}
		line := fmt.Sprintf("  %s %-20s %s%s", icon, f.Name, f.Description, reload)
		if !f.Enabled && f.Reason != "" && f.Reason != "enabled" {
			line += fmt.Sprintf(" (%s)", f.Reason)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n🔄 = hot-reloadable (no restart needed)")
	b.WriteString("\nUse /feature enable <name> or /feature disable <name>")
	gw.sendReply(ctx, ch, msg, b.String())
}

func (gw *Gateway) findFeatureInfo(name string) *feature.FeatureInfo {
	for _, f := range gw.features.List() {
		if f.Name == name {
			return &f
		}
	}
	return nil
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
	gw.runtime.SetModel(args)
	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetModel(args)
	}
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
