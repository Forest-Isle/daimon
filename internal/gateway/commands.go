package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/feature"
)

// handleFeature processes /feature [list|enable|disable] [name].
func (gw *Gateway) handleFeature(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	_ = ctx
	args := strings.TrimPrefix(msg.Text, "/feature")
	args = strings.TrimSpace(args)

	if gw.features == nil {
		return "Feature registry not initialized.", nil
	}

	if args == "" || args == "list" {
		return gw.featureListString(), nil
	}

	parts := strings.Fields(args)
	if len(parts) != 2 || (parts[0] != "enable" && parts[0] != "disable") {
		return "Usage: /feature [list|enable|disable] [name]", nil
	}
	name := parts[1]
	if !gw.features.Has(name) {
		return fmt.Sprintf("Unknown feature: %s", name), nil
	}
	enabled := parts[0] == "enable"
	state, err := loadFeatureState(defaultFeatureStatePath())
	if err != nil {
		return "", err
	}
	if state == nil {
		state = make(map[string]bool)
	}
	state[name] = enabled
	if err := saveFeatureState(defaultFeatureStatePath(), state); err != nil {
		return "", err
	}
	reason := "enabled by runtime override"
	if !enabled {
		reason = "disabled by runtime override"
	}
	if err := gw.features.Set(name, enabled, reason); err != nil {
		return "", err
	}
	action := "enabled"
	if !enabled {
		action = "disabled"
	}
	return fmt.Sprintf("Feature %s %s. Restart Daimon to apply subsystem wiring changes.", name, action), nil
}

// featureListString builds a formatted feature list string.
func (gw *Gateway) featureListString() string {
	features := gw.features.List()
	if len(features) == 0 {
		return "No features registered."
	}

	var enabled, disabled []feature.Info
	for _, f := range features {
		if f.Enabled {
			enabled = append(enabled, f)
		} else {
			disabled = append(disabled, f)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**Features** — %d active · %d inactive\n\n", len(enabled), len(disabled))

	writeGroup := func(items []feature.Info) {
		for _, f := range items {
			line := fmt.Sprintf("- **%s** — %s", f.Name, f.Description)
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
	b.WriteString("Feature state is controlled by config plus persisted runtime overrides in ~/.daimon/feature_state.json. Restart to apply subsystem wiring changes.")
	return b.String()
}

// handleConfig shows current effective configuration.
func (gw *Gateway) handleConfig(ctx context.Context, _ channel.Channel, _ channel.InboundMessage) (string, error) {
	var b strings.Builder
	b.WriteString("**Configuration**\n\n")
	cfg := gw.Config()
	fmt.Fprintf(&b, "  Provider:       %s\n", cfg.LLM.Provider)
	fmt.Fprintf(&b, "  Model:          %s\n", gw.agent.Model())
	fmt.Fprintf(&b, "  Max Tokens:     %d\n", cfg.LLM.MaxTokens)
	fmt.Fprintf(&b, "  Max Iterations: %d\n", cfg.Agent.MaxIterations)

	if gw.features != nil {
		enabled := 0
		for _, f := range gw.features.List() {
			if f.Enabled {
				enabled++
			}
		}
		fmt.Fprintf(&b, "  Features:       %d enabled\n", enabled)
	}

	return b.String(), nil
}

// handleCompact triggers manual context compression for the current session.
func (gw *Gateway) handleCompact(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	if gw.contextMgr == nil {
		return "Context compression is not configured.", nil
	}

	sess, err := gw.sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		return "", fmt.Errorf("failed to get session: %w", err)
	}

	beforeCount := len(sess.History())

	compressed, err := gw.contextMgr.Compress(ctx, sess, "")
	if err != nil {
		return "", fmt.Errorf("compression failed: %w", err)
	}

	afterCount := len(sess.History())

	if !compressed {
		return fmt.Sprintf("No compression needed (current: %d messages).", beforeCount), nil
	}

	if err := gw.sessions.Persist(ctx, sess); err != nil {
		slog.Warn("gateway: failed to persist after compact", "err", err)
	}

	return fmt.Sprintf("Compressed: %d → %d messages.", beforeCount, afterCount), nil
}

// handleModel shows or switches the current LLM model.
func (gw *Gateway) handleModel(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	args := strings.TrimPrefix(msg.Text, "/model")
	args = strings.TrimSpace(args)

	if args == "" {
		return fmt.Sprintf("Model: %s (provider: %s)", gw.agent.Model(), gw.Config().LLM.Provider), nil
	}

	old := gw.agent.Model()
	gw.agent.SetModel(args)
	return fmt.Sprintf("Model switched: %s → %s", old, args), nil
}

// handleReset resets the session to start a fresh conversation (/new or /start).
func (gw *Gateway) handleReset(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	if err := gw.sessions.Reset(ctx, msg.Channel, msg.ChannelID); err != nil {
		return "", fmt.Errorf("failed to reset session: %w", err)
	}
	return "New conversation started.", nil
}
