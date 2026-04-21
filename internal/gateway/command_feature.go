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
			gw.sendReply(ctx, ch, msg, fmt.Sprintf("Error: %v", err))
		} else {
			gw.persistFeatureState()
			reply := fmt.Sprintf("Feature %q enabled.", name)
			if info := gw.findFeatureInfo(name); info != nil && !info.HotReloadable {
				reply += "\nNote: not hot-reloadable — restart IronClaw for full effect."
			}
			gw.sendReply(ctx, ch, msg, reply)
		}

	case strings.HasPrefix(args, "disable "):
		name := strings.TrimSpace(strings.TrimPrefix(args, "disable "))
		if err := gw.features.Disable(ctx, name); err != nil {
			gw.sendReply(ctx, ch, msg, fmt.Sprintf("Error: %v", err))
		} else {
			gw.persistFeatureState()
			reply := fmt.Sprintf("Feature %q disabled.", name)
			if info := gw.findFeatureInfo(name); info != nil && !info.HotReloadable {
				reply += "\nNote: not hot-reloadable — restart IronClaw for full effect."
			}
			gw.sendReply(ctx, ch, msg, reply)
		}

	default:
		gw.sendReply(ctx, ch, msg, "Usage: /feature [list|enable <name>|disable <name>]")
	}
}

// sendFeatureList formats and sends the current feature list as Markdown.
func (gw *Gateway) sendFeatureList(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) {
	features := gw.features.List()
	if len(features) == 0 {
		gw.sendReply(ctx, ch, msg, "No features registered.")
		return
	}

	var enabled, disabled []feature.FeatureInfo
	for _, f := range features {
		if f.Enabled {
			enabled = append(enabled, f)
		} else {
			disabled = append(disabled, f)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**Features** — %d active · %d inactive\n\n", len(enabled), len(disabled))

	writeGroup := func(items []feature.FeatureInfo) {
		for _, f := range items {
			hot := ""
			if f.HotReloadable {
				hot = " [live]"
			}
			line := fmt.Sprintf("- **%s**%s — %s", f.Name, hot, f.Description)
			if !f.Enabled && f.Reason != "" && f.Reason != "enabled" {
				line += fmt.Sprintf(" *(%s)*", f.Reason)
			}
			b.WriteString(line + "\n")
		}
	}

	if len(enabled) > 0 {
		b.WriteString("**Active**\n\n")
		writeGroup(enabled)
		b.WriteString("\n")
	}
	if len(disabled) > 0 {
		b.WriteString("**Inactive**\n\n")
		writeGroup(disabled)
		b.WriteString("\n")
	}

	b.WriteString("---\n")
	b.WriteString("[live] = hot-reloadable · /feature enable <name> · /feature disable <name>")
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
	b.WriteString("**Configuration**\n\n")
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
		gw.sendReply(ctx, ch, msg, fmt.Sprintf("Error: failed to get session: %v", err))
		return
	}

	beforeCount := len(sess.History())

	// TODO: pass actual system prompt once accessible from gateway context
	compressed, err := gw.contextMgr.Compress(ctx, sess, "")
	if err != nil {
		gw.sendReply(ctx, ch, msg, fmt.Sprintf("Error: compression failed: %v", err))
		return
	}

	afterCount := len(sess.History())

	if !compressed {
		gw.sendReply(ctx, ch, msg, fmt.Sprintf("No compression needed (current: %d messages).", beforeCount))
		return
	}

	if err := gw.sessions.Persist(ctx, sess); err != nil {
		slog.Warn("gateway: failed to persist after compact", "err", err)
	}

	gw.sendReply(ctx, ch, msg, fmt.Sprintf("Compressed: %d → %d messages.", beforeCount, afterCount))
}

// handleModelCommand shows or switches the current LLM model.
func (gw *Gateway) handleModelCommand(ctx context.Context, ch channel.Channel, msg channel.InboundMessage, args string) {
	if args == "" {
		gw.sendReply(ctx, ch, msg, fmt.Sprintf("Model: %s (provider: %s)", gw.cfg.LLM.Model, gw.cfg.LLM.Provider))
		return
	}

	old := gw.cfg.LLM.Model
	gw.cfg.LLM.Model = args
	gw.runtime.SetModel(args)
	if gw.cognitiveAgent != nil {
		gw.cognitiveAgent.SetModel(args)
	}
	gw.sendReply(ctx, ch, msg, fmt.Sprintf("Model switched: %s → %s", old, args))
}

// BuildArgCompleter returns an ArgCompleter function for the TUI's dynamic
// argument autocomplete. The returned function is safe to call concurrently.
// Signature matches tui.ArgCompleter: func(cmd, subCmd, argSoFar string) []string.
func (gw *Gateway) BuildArgCompleter() func(cmd, subCmd, argSoFar string) []string {
	return func(cmd, subCmd, argSoFar string) []string {
		if gw.features == nil {
			return nil
		}
		switch cmd {
		case "feature":
			switch subCmd {
			case "enable":
				// Suggest currently disabled features
				var names []string
				for _, f := range gw.features.List() {
					if !f.Enabled {
						names = append(names, f.Name)
					}
				}
				return names
			case "disable":
				// Suggest currently enabled features
				var names []string
				for _, f := range gw.features.List() {
					if f.Enabled {
						names = append(names, f.Name)
					}
				}
				return names
			}
		}
		return nil
	}
}

// persistFeatureState saves the current runtime feature overrides to disk.
// Logs a warning on failure but does not return an error to keep command handlers simple.
func (gw *Gateway) persistFeatureState() {
	if gw.featureStatePath == "" {
		return
	}
	overrides := gw.features.RuntimeOverrides()
	if err := feature.SaveOverrides(gw.featureStatePath, overrides); err != nil {
		slog.Warn("gateway: failed to persist feature state", "err", err)
	}
}

// sendReply is a convenience helper to send a text reply.
func (gw *Gateway) sendReply(ctx context.Context, ch channel.Channel, msg channel.InboundMessage, text string) {
	_ = ch.Send(ctx, channel.OutboundMessage{
		Channel:   msg.Channel,
		ChannelID: msg.ChannelID,
		Text:      text,
	})
}
